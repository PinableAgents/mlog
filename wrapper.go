package mlog

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"syscall"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.yaml.in/yaml/v3"
)

var (
	stopFlag          int32
	stopNetFlag       int32
	zapConfig         ZapConfig
	atomicLevel       = zap.NewAtomicLevelAt(zapcore.InfoLevel)
	initialized       int32
	loggerPtr         atomic.Pointer[zap.Logger]
	callerVariantsPtr atomic.Pointer[callerVariants]
	debugEnabledCache int32
	infoEnabledCache  int32
	warnEnabledCache  int32
	errorEnabledCache int32
)

func LoadConfig(configPath string) (*ZapConfig, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var config ZapConfig
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	return &config, nil
}

func GetConfig() *ZapConfig {
	globalMutex.RLock()
	defer globalMutex.RUnlock()

	config := zapConfig
	return &config
}

// InitialZap retains the legacy panic-on-configuration-error API.
func InitialZap(name string, id uint64, logLevel string, zc *ZapConfig) {
	if err := InitialZapChecked(name, id, logLevel, zc); err != nil {
		panic(err)
	}
}

// InitialZapChecked validates a copied configuration and replaces the old
// lifecycle after draining it. Stop producers before reconfiguration when no
// rejected records are acceptable. It is safe (but not lossless) to race calls
// with reconfiguration or Close; stale cores reject writes with ErrClosed.
func InitialZapChecked(name string, id uint64, logLevel string, zc *ZapConfig) error {
	globalMutex.Lock()
	defer globalMutex.Unlock()
	cfg := zapConfig
	if zc != nil {
		cfg = *zc
	}
	if logLevel != "" {
		cfg.Level = logLevel
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if name != "" && !validComponent(name) {
		return fmt.Errorf("mlog: invalid service name %q", name)
	}
	closeLocked()
	zapConfig = cfg
	level, _ := zapcore.ParseLevel(cfg.Level)
	atomicLevel.SetLevel(level)
	logger, err := initZap(name, id)
	if err != nil {
		return err
	}
	callerVariantsPtr.Store(&callerVariants{base: logger, skip1: logger.WithOptions(zap.AddCallerSkip(1)), skip2: logger.WithOptions(zap.AddCallerSkip(2))})
	loggerPtr.Store(logger)
	zapLogger = logger
	zap.ReplaceGlobals(logger)
	atomic.StoreInt32(&initialized, 1)
	updateLevelCacheOptimized(level)
	if cfg.EnableAsync {
		globalAsyncLogger.Store(newAsyncLoggerFor(cfg.AsyncBufferSize, cfg.AsyncDropOnFull, logger, cfg.ShowLine))
	}
	if cfg.UseRelativePath {
		initPathCache()
		updateBuildRoot(cfg.BuildRootPath)
	} else {
		globalPathCache.Store(nil)
	}
	return nil
}

func GLOG() *zap.Logger {
	return getLoggerOptimized()
}

func updateLevelCacheOptimized(currentLevel zapcore.Level) {
	if currentLevel <= zapcore.DebugLevel {
		atomic.StoreInt32(&debugEnabledCache, 1)
	} else {
		atomic.StoreInt32(&debugEnabledCache, 0)
	}

	if currentLevel <= zapcore.InfoLevel {
		atomic.StoreInt32(&infoEnabledCache, 1)
	} else {
		atomic.StoreInt32(&infoEnabledCache, 0)
	}

	if currentLevel <= zapcore.WarnLevel {
		atomic.StoreInt32(&warnEnabledCache, 1)
	} else {
		atomic.StoreInt32(&warnEnabledCache, 0)
	}

	if currentLevel <= zapcore.ErrorLevel {
		atomic.StoreInt32(&errorEnabledCache, 1)
	} else {
		atomic.StoreInt32(&errorEnabledCache, 0)
	}
}

func getLoggerOptimized() *zap.Logger {
	if atomic.LoadInt32(&initialized) == 0 {
		return nil
	}
	return loggerPtr.Load()
}

func getLogger() (*zap.Logger, bool) {
	logger := getLoggerOptimized()
	return logger, logger != nil
}

func isDebugEnabledFast() bool {
	return atomic.LoadInt32(&debugEnabledCache) == 1
}

func isInfoEnabledFast() bool {
	return atomic.LoadInt32(&infoEnabledCache) == 1
}

func isWarnEnabledFast() bool {
	return atomic.LoadInt32(&warnEnabledCache) == 1
}

func isErrorEnabledFast() bool {
	return atomic.LoadInt32(&errorEnabledCache) == 1
}

func isInitialized() bool {
	return atomic.LoadInt32(&initialized) == 1
}

func UpdateLevel(logLevel string) {
	globalMutex.Lock()
	defer globalMutex.Unlock()

	zapUpdateLevel(logLevel)
	if atomicLevel.Level() != zapcore.InvalidLevel {
		updateLevelCacheOptimized(atomicLevel.Level())
	}
	UpdateAsyncLevelCache()
}

func CheckLevel(logLevel string) bool {
	return zapCheckLevel(logLevel)
}

func Close() { globalMutex.Lock(); defer globalMutex.Unlock(); closeLocked() }

func closeLocked() {
	// Detach the queue first. Workers hold a logger from their own generation.
	old := globalAsyncLogger.Swap(nil)
	if old != nil {
		old.Close()
	}
	atomic.StoreInt32(&initialized, 0)
	updateLevelCacheOptimized(zapcore.FatalLevel)
	loggerPtr.Store(nil)
	callerVariantsPtr.Store(nil)
	coreMutex.Lock()
	for _, core := range zapCores {
		if err := core.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "mlog close: %v\n", err)
		}
	}
	zapCores = nil
	coreMutex.Unlock()
	zapLogger = nil
	zap.ReplaceGlobals(zap.NewNop())
}

// Flush waits for records accepted before its queue barrier, then invokes Sync.
// The bundled rolling writer does not expose fsync: this is NOT a disk-durability
// guarantee. Flush does not disable asynchronous logging.
func Flush() error {
	globalMutex.RLock()
	defer globalMutex.RUnlock()
	if al, ok := getAsyncLogger(); ok {
		al.Flush()
	}
	if logger := getLoggerOptimized(); logger != nil {
		return syncLoggerSafely(logger)
	}
	return nil
}

func Debug(msg string, args ...any) {
	if !isDebugEnabledFast() {
		return
	}
	zapDebug(msg, args...)
}

func DebugW(msg string, fields ...zap.Field) {
	if !isDebugEnabledFast() {
		return
	}
	if al, enabled := getAsyncLogger(); enabled {
		// One queue lookup, with the same public call-site caller depth.
		al.logAsyncWithSkip(zapcore.DebugLevel, msg, nil, 2, fields...)
		return
	}
	logger := getLoggerOptimized()
	if logger == nil {
		return
	}

	loggerWithSkip := withCachedCallerSkip(logger, 1)
	loggerWithSkip.Debug(msg, fields...)
}

func Info(msg string, args ...any) {
	if !isInfoEnabledFast() {
		return
	}
	zapInfo(msg, args...)
}

func InfoW(msg string, fields ...zap.Field) {
	if !isInfoEnabledFast() {
		return
	}
	if al, enabled := getAsyncLogger(); enabled {
		// One queue lookup, with the same public call-site caller depth.
		al.logAsyncWithSkip(zapcore.InfoLevel, msg, nil, 2, fields...)
		return
	}
	logger := getLoggerOptimized()
	if logger == nil {
		return
	}

	loggerWithSkip := withCachedCallerSkip(logger, 1)
	loggerWithSkip.Info(msg, fields...)
}

func Warn(msg string, args ...any) {
	if !isWarnEnabledFast() {
		return
	}
	zapWarn(msg, args...)
}

func WarnW(msg string, fields ...zap.Field) {
	if !isWarnEnabledFast() {
		return
	}
	if al, enabled := getAsyncLogger(); enabled {
		// One queue lookup, with the same public call-site caller depth.
		al.logAsyncWithSkip(zapcore.WarnLevel, msg, nil, 2, fields...)
		return
	}
	logger := getLoggerOptimized()
	if logger == nil {
		return
	}

	loggerWithSkip := withCachedCallerSkip(logger, 1)
	loggerWithSkip.Warn(msg, fields...)
}

func Error(arg0 string, args ...interface{}) {
	if !isErrorEnabledFast() {
		return
	}
	zapError(arg0, args...)
}

func ErrorW(msg string, fields ...zap.Field) {
	if !isErrorEnabledFast() {
		return
	}

	if al, enabled := getAsyncLogger(); enabled {
		// One queue lookup, with the same public call-site caller depth.
		al.logAsyncWithSkip(zapcore.ErrorLevel, msg, nil, 2, fields...)
		return
	}
	logger := getLoggerOptimized()
	if logger == nil {
		return
	}

	loggerWithSkip := withCachedCallerSkip(logger, 1)
	loggerWithSkip.Error(msg, fields...)
}

func ReturnError(msg string, args ...any) error {
	return zapReturnError(msg, args...)
}

func Lock(msg string, args ...any) {
	logger, ok := getLogger()
	if !ok {
		return
	}

	loggerWithSkip := withCachedCallerSkip(logger, 1)

	if len(args) == 0 {
		loggerWithSkip.Info(msg, zap.String("directory", "concurrent"))
		return
	}
	var sb strings.Builder
	formatToStringBuilder(&sb, msg, args...)
	loggerWithSkip.Info(sb.String(), zap.String("directory", "concurrent"))
}

func Critical(msg string, args ...any) {
	logger, ok := getLogger()
	if !ok {
		return
	}

	loggerWithSkip := withCachedCallerSkip(logger, 1)

	if len(args) == 0 {
		loggerWithSkip.Warn(msg, zap.String("directory", "emergency"))
		return
	}
	var sb strings.Builder
	formatToStringBuilder(&sb, msg, args...)
	loggerWithSkip.Warn(sb.String(), zap.String("directory", "emergency"))
}

func Disaster(msg string, args ...interface{}) {
	logger, ok := getLogger()
	if !ok {
		return
	}

	loggerWithSkip := withCachedCallerSkip(logger, 1)

	if len(args) == 0 {
		loggerWithSkip.Error(msg, zap.String("directory", "emergency"))
		return
	}
	var sb strings.Builder
	formatToStringBuilder(&sb, msg, args...)
	loggerWithSkip.Error(sb.String(), zap.String("directory", "emergency"))
}

func ExitGame(format string, args ...any) {
	if !isInitialized() {
		panic(fmt.Sprintf(format, args...))
	}
	var msg string
	if len(args) == 0 {
		msg = format
	} else {
		msg = fmt.Sprintf(format, args...)
	}

	if StopFlag() {
		Warn("[ExitGame] Has stopped,%s", msg)
		return
	}
	Disaster("%s", msg)
	_ = Flush()
	panic(msg)
}

func GrpcAssert(format string, args ...any) {
	if !isInfoEnabledFast() {
		return
	}

	_, src, line, _ := runtime.Caller(1)

	displayPath := src
	if GetConfig().UseRelativePath {
		displayPath = getRelativePath(src)
	}

	var msg string
	if len(args) == 0 {
		msg = fmt.Sprintf("%s:%d %s", displayPath, line, format)
	} else {
		msg = fmt.Sprintf("%s:%d %s", displayPath, line, fmt.Sprintf(format, args...))
	}

	buf := debug.Stack()
	stringStack := BytesToString(buf)

	if GetConfig().UseRelativePath {
		stringStack = convertStackPathsToRelative(stringStack)
	}

	stackMessage := fmt.Sprintf("[GrpcAssert] %s\n\nStack Trace:\n%s", msg, stringStack)

	logger := getLoggerOptimized()
	if logger != nil {
		loggerWithSkip := withCachedCallerSkip(logger, 1)
		loggerWithSkip.Info(stackMessage, zap.String("directory", "assert"))
	}
}

func AssertString(format string, args ...interface{}) {
	if !isInfoEnabledFast() {
		return
	}

	_, src, line, _ := runtime.Caller(1)

	displayPath := src
	if GetConfig().UseRelativePath {
		displayPath = getRelativePath(src)
	}

	var msg string
	if len(args) == 0 {
		msg = fmt.Sprintf("%s:%d %s", displayPath, line, format)
	} else {
		msg = fmt.Sprintf("%s:%d %s", displayPath, line, fmt.Sprintf(format, args...))
	}

	buf := debug.Stack()
	stringStack := BytesToString(buf)

	if GetConfig().UseRelativePath {
		stringStack = convertStackPathsToRelative(stringStack)
	}

	stackMessage := fmt.Sprintf("[Assert] %s\n\nStack Trace:\n%s", msg, stringStack)

	logger := getLoggerOptimized()
	if logger != nil {
		loggerWithSkip := withCachedCallerSkip(logger, 1)
		loggerWithSkip.Info(stackMessage, zap.String("directory", "assert"))
	}
}

func BytesToString(p []byte) string {
	for i, b := range p {
		if b == 0 {
			return string(p[:i])
		}
	}
	return string(p)
}

func convertStackPathsToRelative(stackTrace string) string {
	if globalPathCache.Load() != nil {
		return convertStackPathsToRelativeOptimized(stackTrace)
	}

	return convertStackPathsToRelativeLegacy(stackTrace)
}

func convertStackPathsToRelativeOptimized(stackTrace string) string {
	pc := globalPathCache.Load()
	if pc == nil {
		return convertStackPathsToRelativeLegacy(stackTrace)
	}
	return pc.stackPathRegex.ReplaceAllStringFunc(stackTrace, func(match string) string {
		// Use the final colon so a Windows drive prefix is not split as a line number.
		i := strings.LastIndex(match, ":")
		return pc.getRelativePathCached(match[:i]) + match[i:]
	})
}

func convertStackPathsToRelativeLegacy(stackTrace string) string {
	lines := strings.Split(stackTrace, "\n")
	for i, line := range lines {
		if strings.Contains(line, "/") && (strings.Contains(line, ".go:") || strings.Contains(line, ".go ")) {
			lines[i] = replaceAbsolutePathInLine(line)
		}
	}
	return strings.Join(lines, "\n")
}

func replaceAbsolutePathInLine(line string) string {
	if !strings.Contains(line, "/") || !strings.Contains(line, ".go") {
		return line
	}

	var result strings.Builder
	result.Grow(len(line))

	fields := strings.Fields(line)
	for i, field := range fields {
		if i > 0 {
			result.WriteByte(' ')
		}

		if strings.Contains(field, "/") && strings.Contains(field, ".go") {
			result.WriteString(replacePathInField(field))
		} else {
			result.WriteString(field)
		}
	}

	return result.String()
}

func replacePathInField(field string) string {
	if colonIndex := strings.Index(field, ":"); colonIndex != -1 {
		filePath := field[:colonIndex]
		suffix := field[colonIndex:]
		relativePath := getRelativePath(filePath)
		return relativePath + suffix
	}

	if spaceIndex := strings.Index(field, " "); spaceIndex != -1 {
		filePath := field[:spaceIndex]
		suffix := field[spaceIndex:]
		relativePath := getRelativePath(filePath)
		return relativePath + suffix
	}

	return getRelativePath(field)
}

func syncLoggerSafely(logger *zap.Logger) error {
	err := logger.Sync()
	if isHarmlessSyncError(err) {
		return nil
	}
	return err
}

func isInteractiveTerminal() bool {
	if fileInfo, err := os.Stdout.Stat(); err == nil {
		return (fileInfo.Mode() & os.ModeCharDevice) != 0
	}
	return false
}

func isHarmlessSyncError(err error) bool {
	if err == nil {
		return true
	}
	switch e := err.(type) {
	case interface{ Unwrap() []error }:
		for _, child := range e.Unwrap() {
			if !isHarmlessSyncError(child) {
				return false
			}
		}
		return true
	case *os.PathError:
		if e.Op != "sync" {
			return false
		}
		if e.Path != "/dev/stdout" && e.Path != "/dev/stderr" && e.Path != "stdout" && e.Path != "stderr" {
			return false
		}
		return errors.Is(e.Err, syscall.EINVAL) || errors.Is(e.Err, syscall.ENOTTY) || errors.Is(e.Err, syscall.ENOTSUP)
	case interface{ Unwrap() error }:
		return isHarmlessSyncError(e.Unwrap())
	default:
		return false
	}
}

func StopFlag() bool {
	return atomic.LoadInt32(&stopFlag) == 1
}

func SetStopFlag() {
	logger := getLoggerOptimized()
	if logger != nil {
		loggerWithSkip := withCachedCallerSkip(logger, 1)
		loggerWithSkip.Info("[SetStopFlag] start")
	}
	atomic.StoreInt32(&stopFlag, 1)
}

func StopNetFlag() bool {
	return atomic.LoadInt32(&stopNetFlag) == 1
}

func SetStopNetFlag() {
	logger := getLoggerOptimized()
	if logger != nil {
		loggerWithSkip := withCachedCallerSkip(logger, 1)
		loggerWithSkip.Info("[SetStopNetFlag] start")
	}
	atomic.StoreInt32(&stopNetFlag, 1)
}

// callerVariants is immutable after publication. Base identity prevents a
// stale generation from borrowing caller options from the replacement logger.
type callerVariants struct {
	base, skip1, skip2 *zap.Logger
}

func withCachedCallerSkip(logger *zap.Logger, skip int) *zap.Logger {
	if v := callerVariantsPtr.Load(); v != nil && v.base == logger {
		if skip == 1 {
			return v.skip1
		}
		if skip == 2 {
			return v.skip2
		}
	}
	// Custom/internal test loggers and lifecycle races retain legacy behavior.
	return logger.WithOptions(zap.AddCallerSkip(skip))
}
