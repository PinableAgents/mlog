package mlog

import (
	"bytes"
	"fmt"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"sync"
	"testing"
	"time"
)

func TestAuditAsyncContracts(t *testing.T) {
	_, logs := auditObserve(t)
	al := newAsyncLogger(4, false)
	globalAsyncLogger.Store(al)
	t.Cleanup(func() { Close() })
	Debug("printf %d", 1)
	Info("printf %d", 2)
	Warn("printf %d", 3)
	Error("printf %d", 4)
	DebugW("fields", zap.Int("id", 1))
	InfoW("fields", zap.Int("id", 2))
	WarnW("fields", zap.Int("id", 3))
	ErrorW("fields", zap.Int("id", 4))
	al.debugAsync("method %d", []any{1})
	al.infoAsync("method %d", []any{2})
	al.warnAsync("method %d", []any{3})
	al.errorAsync("method %d", []any{4})
	al.logAsync(zapcore.InfoLevel, "internal", nil)
	al.showLine = true // worker never reads this producer-only flag; no concurrent producers here.
	al.logAsyncWithSkip(zapcore.InfoLevel, "caller", nil, 0)
	al.logAsyncWithSkip(zapcore.InfoLevel, "deep", nil, 100000)
	for _, level := range []zapcore.Level{zapcore.DPanicLevel, zapcore.PanicLevel, zapcore.FatalLevel} {
		if !al.levelCache.isLevelEnabled(level) {
			t.Fatal("unexpected high severity filter")
		}
	}
	GetAsyncCacheStats()
	al.GetCacheStats()
	al.ClearCache()
	ClearAsyncCache()
	UpdateAsyncLevelCache()
	if err := Flush(); err != nil {
		t.Fatal(err)
	}
	stats := GetAsyncStats()
	if stats.Accepted != 15 || stats.Processed != 15 || stats.Dropped != 0 {
		t.Fatal(stats)
	}
	if logs.Len() != 15 {
		t.Fatal("drain lost records", logs.Len())
	}
	UpdateLevel("error")
	al.logAsync(zapcore.DebugLevel, "disabled", nil)
	al.Flush()
	if al.Stats().Accepted != 15 {
		t.Fatal("disabled record queued")
	}
	al.writeLogEntryWithCaller(al.logger, AsyncLogEntry{Level: zapcore.DebugLevel, Message: "disabled"})
	Close()
	al.Flush()
	al.close()
	al.logAsync(zapcore.ErrorLevel, "after close", nil)
	if al.Stats().Rejected != 1 {
		t.Fatal("closed queue accepted a record", al.Stats())
	}
	if GetAsyncStats() != (AsyncStats{}) {
		t.Fatal("closed global stats")
	}
	GetAsyncCacheStats()
	ClearAsyncCache()
	UpdateAsyncLevelCache()
	// Legacy fallbacks must retain printf arguments instead of dropping them.
	_, logs = auditObserve(t)
	for _, f := range []func(string, []any, ...zap.Field){debugAsync, infoAsync, warnAsync, errorAsync} {
		f("id=%d", []any{9}, zap.Int("field", 7))
	}
	for _, entry := range logs.All() {
		if entry.Message != "id=9" {
			t.Fatal(entry.Message)
		}
	}
	Close()
	lc := NewLevelCache()
	if lc.isLevelEnabled(zapcore.FatalLevel) {
		t.Fatal("uninitialized high severity")
	}
	for _, f := range []func(string, []any, ...zap.Field){debugAsync, infoAsync, warnAsync, errorAsync} {
		f("closed", nil)
	}
	al = newAsyncLoggerFor(-1, false, nil, false)
	al.logAsync(zapcore.InfoLevel, "no logger", nil)
	al.Close()
	if al.Stats().Processed != 1 {
		t.Fatal("worker did not drain")
	}
}

type auditBlockingSink struct {
	once    sync.Once
	started chan struct{}
	release chan struct{}
	mu      sync.Mutex
	buf     bytes.Buffer
}

func (s *auditBlockingSink) Write(p []byte) (int, error) {
	s.once.Do(func() { close(s.started); <-s.release })
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}
func (s *auditBlockingSink) Sync() error    { return nil }
func (s *auditBlockingSink) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.buf.String() }

func TestAuditAsyncOwnershipAndBackpressure(t *testing.T) {
	auditObserve(t)
	for _, drop := range []bool{false, true} {
		t.Run(fmt.Sprint(drop), func(t *testing.T) {
			sink := &auditBlockingSink{started: make(chan struct{}), release: make(chan struct{})}
			cfg := ZapConfig{Format: "json"}
			l := zap.New(zapcore.NewCore(cfg.Encoder(), sink, zapcore.DebugLevel))
			al := newAsyncLoggerFor(1, drop, l, false)
			al.logAsync(zapcore.InfoLevel, "blocked", nil)
			<-sink.started
			data := []byte("old")
			fields := []zap.Field{zap.Int("id", 7), zap.ByteString("data", data)}
			al.logAsync(zapcore.InfoLevel, "owned", nil, fields...)
			fields[0] = zap.Int("id", 999)
			data[0] = 'X'
			var send sync.WaitGroup
			if drop {
				al.logAsync(zapcore.InfoLevel, "drop", nil)
			} else {
				send.Add(1)
				go func() { defer send.Done(); al.logAsync(zapcore.InfoLevel, "backpressure", nil) }()
			}
			close(sink.release)
			send.Wait()
			al.Flush()
			al.Close()
			al.Close()
			out := sink.String()
			if !bytes.Contains([]byte(out), []byte(`"id":7`)) || !bytes.Contains([]byte(out), []byte(`"data":"old"`)) || bytes.Contains([]byte(out), []byte(`999`)) {
				t.Fatal("caller mutation leaked into queue", out)
			}
			stats := al.Stats()
			want := uint64(3)
			if drop {
				want = 2
				if stats.Dropped != 1 {
					t.Fatal("drop counter", stats)
				}
			}
			if stats.Accepted != want || stats.Processed != want {
				t.Fatal("incomplete drain", stats)
			}
		})
	}
}

func TestAuditConcurrentCloseAndReinitialize(t *testing.T) {
	cfg := ZapConfig{Director: t.TempDir(), Format: "json", SingleFile: true, EnableAsync: true, AsyncBufferSize: 2}
	InitialZap("", 0, "info", &cfg)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				Info("concurrent %d", j)
				InfoW("field", zap.Int("n", j))
				GetConfig()
			}
		}()
	}
	for i := 0; i < 5; i++ {
		InitialZap("", 0, "info", &cfg)
		if err := Flush(); err != nil {
			t.Error(err)
		}
	}
	wg.Wait()
	Close()
}

func TestAuditAsyncCallerAndTimestamp(t *testing.T) {
	_, logs := auditObserve(t)
	cfg := GetConfig()
	cfg.ShowLine = true
	globalMutex.Lock()
	zapConfig = *cfg
	globalMutex.Unlock()
	al := newAsyncLogger(2, false)
	globalAsyncLogger.Store(al)
	before := time.Now()
	Info("caller printf")
	InfoW("caller fields")
	al.Flush()
	after := time.Now()
	for _, e := range logs.All() {
		if !e.Caller.Defined || !bytes.Contains([]byte(e.Caller.File), []byte("audit_async_test.go")) {
			t.Fatal("wrong caller", e.Caller)
		}
		if e.Time.Before(before) || e.Time.After(after) {
			t.Fatal("wrong producer timestamp")
		}
	}
	Close()
}
