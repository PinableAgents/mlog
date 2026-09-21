package mlog

import (
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Publication is atomic; the queue's acceptMu still serializes acceptance
// against Close, and globalMutex still owns initialization/reconfiguration.
var globalAsyncLogger atomic.Pointer[AsyncLogger]

type AsyncLogEntry struct {
	preparedCore zapcore.Core // immutable encoded field context, owned by this entry
	Level        zapcore.Level
	Message      string
	Fields       []zap.Field
	Extras       []any
	Caller       zapcore.EntryCaller
	barrier      chan struct{}
	Timestamp    time.Time
}

type OptimizedSkipCache struct {
	mu      sync.Mutex
	cache   sync.Map
	maxSize int64
	size    int64

	hits   int64
	misses int64
}

type StringBuilderPool struct {
	pool sync.Pool
}

func NewStringBuilderPool() *StringBuilderPool {
	return &StringBuilderPool{
		pool: sync.Pool{
			New: func() interface{} {
				sb := &strings.Builder{}
				sb.Grow(256)
				return sb
			},
		},
	}
}

func (p *StringBuilderPool) Get() *strings.Builder {
	return p.pool.Get().(*strings.Builder)
}

func (p *StringBuilderPool) Put(sb *strings.Builder) {
	sb.Reset()
	p.pool.Put(sb)
}

type LevelCache struct {
	debugEnabled int32
	infoEnabled  int32
	warnEnabled  int32
	errorEnabled int32
}

func NewLevelCache() *LevelCache {
	lc := &LevelCache{
		debugEnabled: 1,
		infoEnabled:  1,
		warnEnabled:  1,
		errorEnabled: 1,
	}
	lc.updateCache()
	return lc
}

func (lc *LevelCache) updateCache() {
	logger := getLoggerOptimized()
	if logger == nil {
		return
	}

	core := logger.Core()
	atomic.StoreInt32(&lc.debugEnabled, boolToInt32(core.Enabled(zapcore.DebugLevel)))
	atomic.StoreInt32(&lc.infoEnabled, boolToInt32(core.Enabled(zapcore.InfoLevel)))
	atomic.StoreInt32(&lc.warnEnabled, boolToInt32(core.Enabled(zapcore.WarnLevel)))
	atomic.StoreInt32(&lc.errorEnabled, boolToInt32(core.Enabled(zapcore.ErrorLevel)))
}

func (lc *LevelCache) isDebugEnabled() bool {
	return atomic.LoadInt32(&lc.debugEnabled) == 1
}

func (lc *LevelCache) isInfoEnabled() bool {
	return atomic.LoadInt32(&lc.infoEnabled) == 1
}

func (lc *LevelCache) isWarnEnabled() bool {
	return atomic.LoadInt32(&lc.warnEnabled) == 1
}

func (lc *LevelCache) isErrorEnabled() bool {
	return atomic.LoadInt32(&lc.errorEnabled) == 1
}

func (lc *LevelCache) isLevelEnabled(level zapcore.Level) bool {
	switch level {
	case zapcore.DebugLevel:
		return lc.isDebugEnabled()
	case zapcore.InfoLevel:
		return lc.isInfoEnabled()
	case zapcore.WarnLevel:
		return lc.isWarnEnabled()
	case zapcore.ErrorLevel:
		return lc.isErrorEnabled()
	default:
		logger, ok := getLogger()
		if !ok {
			return false
		}
		return logger.Core().Enabled(level)
	}
}

func boolToInt32(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

type AsyncLogger struct {
	acceptMu   sync.RWMutex
	closed     bool
	logger     *zap.Logger
	showLine   bool
	accepted   atomic.Uint64
	processed  atomic.Uint64
	dropped    atomic.Uint64
	rejected   atomic.Uint64
	logChan    chan AsyncLogEntry
	done       chan struct{}
	wg         sync.WaitGroup
	dropOnFull bool
	skipCache  *OptimizedSkipCache
	sbPool     *StringBuilderPool
	levelCache *LevelCache
}

func NewOptimizedSkipCache(maxSize int64) *OptimizedSkipCache {
	return &OptimizedSkipCache{
		maxSize: maxSize,
	}
}

func (c *OptimizedSkipCache) Get(pc uintptr) (int, bool) {
	if value, ok := c.cache.Load(pc); ok {
		atomic.AddInt64(&c.hits, 1)
		return value.(int), true
	}
	atomic.AddInt64(&c.misses, 1)
	return 0, false
}

func (c *OptimizedSkipCache) Set(pc uintptr, skip int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if atomic.LoadInt64(&c.size) < c.maxSize {
		if _, loaded := c.cache.LoadOrStore(pc, skip); !loaded {
			atomic.AddInt64(&c.size, 1)
		}
	}
}

func (c *OptimizedSkipCache) GetStats() (hits, misses int64, size int64, hitRate float64) {
	hits = atomic.LoadInt64(&c.hits)
	misses = atomic.LoadInt64(&c.misses)
	size = atomic.LoadInt64(&c.size)

	total := hits + misses
	if total > 0 {
		hitRate = float64(hits) / float64(total)
	}

	return hits, misses, size, hitRate
}

func (c *OptimizedSkipCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache.Range(func(key, value interface{}) bool {
		c.cache.Delete(key)
		return true
	})
	atomic.StoreInt64(&c.size, 0)
	atomic.StoreInt64(&c.hits, 0)
	atomic.StoreInt64(&c.misses, 0)
}

func newAsyncLogger(bufferSize int, dropOnFull bool) *AsyncLogger {
	cfg := GetConfig()
	return newAsyncLoggerFor(bufferSize, dropOnFull, getLoggerOptimized(), cfg.ShowLine)
}
func newAsyncLoggerFor(bufferSize int, dropOnFull bool, logger *zap.Logger, showLine bool) *AsyncLogger {
	if bufferSize < 0 {
		bufferSize = 0
	}
	al := &AsyncLogger{logChan: make(chan AsyncLogEntry, bufferSize), done: make(chan struct{}),
		dropOnFull: dropOnFull, logger: logger, showLine: showLine, skipCache: NewOptimizedSkipCache(1000),
		sbPool: NewStringBuilderPool(), levelCache: NewLevelCache()}
	al.wg.Add(1)
	go al.processLogs()
	return al
}

func (al *AsyncLogger) processLogEntry(entry AsyncLogEntry) {
	if entry.barrier != nil {
		close(entry.barrier)
		return
	}
	if al.logger != nil {
		al.writeLogEntryWithCaller(al.logger, entry)
	}
	// Processed counts write attempts, not fsync or successful durable writes.
	al.processed.Add(1)
}
func (al *AsyncLogger) processLogs() {
	defer al.wg.Done()
	defer close(al.done)
	for entry := range al.logChan {
		al.processLogEntry(entry)
	}
}

func (al *AsyncLogger) logAsync(level zapcore.Level, msg string, args []any, fields ...zap.Field) {
	al.logAsyncWithSkip(level, msg, args, 3, fields...)
}

func (al *AsyncLogger) logAsyncWithSkip(level zapcore.Level, msg string, args []any, skip int, fields ...zap.Field) {
	if !al.levelCache.isLevelEnabled(level) {
		return
	}

	timestamp := time.Now()

	// Caller skip is explicit; no repeated stack scans or PC-only cache keys.
	caller := zapcore.EntryCaller{}
	if al.showLine {
		if pc, file, line, ok := runtime.Caller(skip); ok {
			caller = zapcore.NewEntryCaller(pc, file, line, true)
		}
	}
	// Formatting happens before return. Mutable caller values still need locks.
	formattedMsg := formatMessage(msg, args, true)
	var prepared zapcore.Core
	if len(fields) > 0 && al.logger != nil {
		// Core.With encodes reference-backed fields on the producer thread. This
		// prevents maps, slices, objects or byte buffers being read after return.
		// It still cannot make concurrent mutation DURING this call safe.
		prepared = al.logger.Core().With(fields)
	}

	entry := AsyncLogEntry{
		Level:        level,
		Message:      formattedMsg,
		Fields:       nil,
		preparedCore: prepared,
		Extras:       nil,
		Caller:       caller,
		Timestamp:    timestamp,
	}

	al.acceptMu.RLock()
	defer al.acceptMu.RUnlock()
	if al.closed {
		al.rejected.Add(1)
		return
	}
	if al.dropOnFull {
		select {
		case al.logChan <- entry:
			al.accepted.Add(1)
		default:
			al.dropped.Add(1)
		}
	} else {
		al.logChan <- entry
		al.accepted.Add(1)
	}
}

func (al *AsyncLogger) writeLogEntryWithCaller(logger *zap.Logger, entry AsyncLogEntry) {
	zapEntry := zapcore.Entry{
		Level:      entry.Level,
		Time:       entry.Timestamp,
		LoggerName: "",
		Message:    entry.Message,
		Caller:     entry.Caller,
		Stack:      "",
	}

	core := entry.preparedCore
	if core == nil {
		core = logger.Core()
	}
	if ce := core.Check(zapEntry, nil); ce != nil {
		ce.Write(entry.Fields...)
	}
}

func (al *AsyncLogger) GetCacheStats() (hits, misses int64, size int64, hitRate float64) {
	return al.skipCache.GetStats()
}

func (al *AsyncLogger) ClearCache() {
	al.skipCache.Clear()
}

func (al *AsyncLogger) UpdateLevelCache() {
	al.levelCache.updateCache()
}

func (al *AsyncLogger) Close() {
	al.acceptMu.Lock()
	if !al.closed {
		al.closed = true
		close(al.logChan)
	}
	al.acceptMu.Unlock()
	al.wg.Wait()
}

// Flush is a FIFO barrier. Producers may continue after the barrier is queued.
func (al *AsyncLogger) Flush() {
	barrier := make(chan struct{})
	al.acceptMu.RLock()
	if al.closed {
		al.acceptMu.RUnlock()
		al.wg.Wait()
		return
	}
	al.logChan <- AsyncLogEntry{barrier: barrier}
	al.acceptMu.RUnlock()
	<-barrier
}

// AsyncStats counts accepted records, worker write attempts and rejected/dropped
// records. None of these counters promises physical disk durability.
type AsyncStats struct{ Accepted, Processed, Dropped, Rejected uint64 }

func (al *AsyncLogger) Stats() AsyncStats {
	return AsyncStats{al.accepted.Load(), al.processed.Load(), al.dropped.Load(), al.rejected.Load()}
}
func GetAsyncStats() AsyncStats {
	if al, ok := getAsyncLogger(); ok {
		return al.Stats()
	}
	return AsyncStats{}
}

func (al *AsyncLogger) close() {
	al.Close()
}

func (al *AsyncLogger) debugAsync(msg string, args []any, fields ...zap.Field) {
	al.logAsyncWithSkip(zapcore.DebugLevel, msg, args, 5, fields...)
}

func (al *AsyncLogger) infoAsync(msg string, args []any, fields ...zap.Field) {
	al.logAsyncWithSkip(zapcore.InfoLevel, msg, args, 5, fields...)
}

func (al *AsyncLogger) warnAsync(msg string, args []any, fields ...zap.Field) {
	al.logAsyncWithSkip(zapcore.WarnLevel, msg, args, 5, fields...)
}

func (al *AsyncLogger) errorAsync(msg string, args []any, fields ...zap.Field) {
	al.logAsyncWithSkip(zapcore.ErrorLevel, msg, args, 5, fields...)
}

func getAsyncLogger() (*AsyncLogger, bool) {
	logger := globalAsyncLogger.Load()
	return logger, logger != nil
}

func debugAsync(msg string, args []any, fields ...zap.Field) {
	if logger, ok := getAsyncLogger(); ok {

		logger.logAsyncWithSkip(zapcore.DebugLevel, msg, args, 3, fields...)
	} else {
		if logger := getLoggerOptimized(); logger != nil {
			withCachedCallerSkip(logger, 2).Debug(formatMessage(msg, args, false), fields...)
		}
	}
}

func infoAsync(msg string, args []any, fields ...zap.Field) {
	if logger, ok := getAsyncLogger(); ok {
		logger.logAsyncWithSkip(zapcore.InfoLevel, msg, args, 3, fields...)
	} else {
		if logger := getLoggerOptimized(); logger != nil {
			withCachedCallerSkip(logger, 2).Info(formatMessage(msg, args, false), fields...)
		}
	}
}

func warnAsync(msg string, args []any, fields ...zap.Field) {
	if logger, ok := getAsyncLogger(); ok {
		logger.logAsyncWithSkip(zapcore.WarnLevel, msg, args, 3, fields...)
	} else {
		if logger := getLoggerOptimized(); logger != nil {
			withCachedCallerSkip(logger, 2).Warn(formatMessage(msg, args, false), fields...)
		}
	}
}

func errorAsync(msg string, args []any, fields ...zap.Field) {
	if logger, ok := getAsyncLogger(); ok {
		logger.logAsyncWithSkip(zapcore.ErrorLevel, msg, args, 3, fields...)
	} else {
		if logger := getLoggerOptimized(); logger != nil {
			withCachedCallerSkip(logger, 2).Error(formatMessage(msg, args, false), fields...)
		}
	}
}

func GetAsyncCacheStats() (hits, misses int64, size int64, hitRate float64) {
	if logger, ok := getAsyncLogger(); ok {
		return logger.GetCacheStats()
	}
	return 0, 0, 0, 0
}

func ClearAsyncCache() {
	if logger, ok := getAsyncLogger(); ok {
		logger.ClearCache()
	}
}

func UpdateAsyncLevelCache() {
	logger := globalAsyncLogger.Load()

	if logger != nil {
		logger.UpdateLevelCache()
	}
}

func isAsyncEnabled() bool {
	_, enabled := getAsyncLogger()
	return enabled
}
