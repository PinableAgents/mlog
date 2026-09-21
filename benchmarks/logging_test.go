package benchmarks

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PinableAgents/mlog"
	"github.com/ai-mmo/lumberjack"
	"github.com/rs/zerolog"
	"github.com/sirupsen/logrus"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const timeLayout = "2006-01-02 15:04:05.000"

var fields = []zap.Field{zap.String("request_id", "req-123"), zap.Int("user_id", 42), zap.String("method", "GET"), zap.Int("status", 200), zap.Bool("cached", true)}
var logFields = logrus.Fields{"request_id": "req-123", "user_id": 42, "method": "GET", "status": 200, "cached": true}
var attrs = []slog.Attr{slog.String("request_id", "req-123"), slog.Int("user_id", 42), slog.String("method", "GET"), slog.Int("status", 200), slog.Bool("cached", true)}
var names = []string{"mlog-sync", "mlog-async-drained", "zap", "slog", "zerolog", "logrus"}

type subject struct {
	plain, structured, disabled, formatted func()
	finish                                 func()
	path                                   string
}

// All end-to-end subjects write JSON through the SAME rolling writer version,
// using the SAME rotation limits, five values, severity threshold and time
// layout. No subject enables caller capture, compression or per-record fsync.
// slog's ReplaceAttr cost is intentionally included to match this schema.
func newSubject(t testing.TB, name string) *subject {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "all.log")
	cfg := mlog.ZapConfig{Director: dir, Format: "json", Level: "info", SingleFile: true, SingleFileName: "all.log", MaxSize: 1024, MaxBackups: 1}
	s := &subject{path: path}
	if strings.HasPrefix(name, "mlog") {
		cfg.EnableAsync = name == "mlog-async-drained"
		cfg.AsyncBufferSize = 4096
		cfg.AsyncDropOnFull = false
		mlog.InitialZap("", 0, "info", &cfg)
		s.plain = func() { mlog.Info("request complete") }
		s.structured = func() { mlog.InfoW("request complete", fields...) }
		s.disabled = func() { mlog.Debug("request %d", 42) }
		s.formatted = func() { mlog.Info("request %d status %s", 42, "ok") }
		s.finish = func() { mlog.Close() }
		return s
	}
	writer := &lumberjack.Logger{Filename: path, MaxSize: 1024, MaxBackups: 1, LocalTime: true}
	s.finish = func() {
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	}
	switch name {
	case "zap":
		l := zap.New(zapcore.NewCore(cfg.Encoder(), zapcore.AddSync(writer), zapcore.InfoLevel))
		sugar := l.Sugar()
		s.plain = func() { l.Info("request complete") }
		s.structured = func() { l.Info("request complete", fields...) }
		s.disabled = func() { sugar.Debugf("request %d", 42) }
		s.formatted = func() { sugar.Infof("request %d status %s", 42, "ok") }
	case "slog":
		l := slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelInfo, ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 {
				switch a.Key {
				case slog.TimeKey:
					return slog.String("time", a.Value.Time().Format(timeLayout))
				case slog.LevelKey:
					return slog.String("level", strings.ToLower(a.Value.String()))
				case slog.MessageKey:
					a.Key = "message"
				}
			}
			return a
		}}))
		s.plain = func() { l.Info("request complete") }
		s.structured = func() { l.LogAttrs(context.Background(), slog.LevelInfo, "request complete", attrs...) }
		// slog has no printf API: explicitly gate formatting for an equivalent disabled path.
		s.disabled = func() {
			if l.Enabled(context.Background(), slog.LevelDebug) {
				l.Debug(fmt.Sprintf("request %d", 42))
			}
		}
		s.formatted = func() { l.Info(fmt.Sprintf("request %d status %s", 42, "ok")) }
	case "zerolog":
		// Set only package-level constants once before any parallel work starts.
		zerolog.TimeFieldFormat = timeLayout
		l := zerolog.New(writer).Level(zerolog.InfoLevel).With().Timestamp().Logger()
		s.plain = func() { l.Info().Msg("request complete") }
		s.structured = func() {
			l.Info().Str("request_id", "req-123").Int("user_id", 42).Str("method", "GET").Int("status", 200).Bool("cached", true).Msg("request complete")
		}
		s.disabled = func() { l.Debug().Msgf("request %d", 42) }
		s.formatted = func() { l.Info().Msgf("request %d status %s", 42, "ok") }
	case "logrus":
		l := logrus.New()
		l.SetOutput(writer)
		l.SetLevel(logrus.InfoLevel)
		l.SetFormatter(&logrus.JSONFormatter{TimestampFormat: timeLayout, FieldMap: logrus.FieldMap{logrus.FieldKeyMsg: "message"}})
		s.plain = func() { l.Info("request complete") }
		s.structured = func() { l.WithFields(logFields).Info("request complete") }
		s.disabled = func() { l.Debugf("request %d", 42) }
		s.formatted = func() { l.Infof("request %d status %s", 42, "ok") }
	default:
		t.Fatalf("unknown subject %q", name)
	}
	return s
}

func BenchmarkFile(b *testing.B) {
	for _, workload := range []string{"plain", "five-fields", "printf", "disabled"} {
		for _, name := range names {
			b.Run(workload+"/"+name, func(b *testing.B) {
				s := newSubject(b, name)
				s.plain() // Warm open/initialization equally for every subject.
				log := s.plain
				switch workload {
				case "five-fields":
					log = s.structured
				case "printf":
					log = s.formatted
				case "disabled":
					log = s.disabled
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					log()
				}
				// Completion, including async drain and close, is inside the timing window.
				s.finish()
				b.StopTimer()
			})
		}
	}
}

func BenchmarkFileParallel(b *testing.B) {
	for _, name := range names {
		b.Run(name, func(b *testing.B) {
			s := newSubject(b, name)
			s.plain()
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					s.structured()
				}
			})
			s.finish()
			b.StopTimer()
		})
	}
}

// Validate semantic equivalence and completed line count before timing. An
// implementation which silently drops five fields is NOT a valid comparator.
func TestCompletedOutput(t *testing.T) {
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			s := newSubject(t, name)
			var wg sync.WaitGroup
			for i := 0; i < 4; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for j := 0; j < 50; j++ {
						s.structured()
					}
				}()
			}
			wg.Wait()
			s.disabled()
			s.finish()
			f, e := os.Open(s.path)
			if e != nil {
				t.Fatal(e)
			}
			defer f.Close()
			scanner := bufio.NewScanner(f)
			n := 0
			for scanner.Scan() {
				var v map[string]any
				if e = json.Unmarshal(scanner.Bytes(), &v); e != nil {
					t.Fatal(e)
				}
				if v["request_id"] != "req-123" || v["user_id"] != float64(42) || v["method"] != "GET" || v["status"] != float64(200) || v["cached"] != true || v["message"] != "request complete" || v["level"] != "info" {
					t.Fatalf("non-equivalent output: %s", scanner.Bytes())
				}
				if _, ok := v["caller"]; ok {
					t.Fatal("unexpected caller overhead")
				}
				if _, e = time.Parse(timeLayout, v["time"].(string)); e != nil {
					t.Fatal(e)
				}
				n++
			}
			if scanner.Err() != nil || n != 200 {
				t.Fatal("incomplete output", n, scanner.Err())
			}
		})
	}
}

// Native codec/API microbenchmarks intentionally exclude mlog: its public API
// owns file routing, so replacing its core with io.Discard would hide that cost.
// This separate table is NOT directly comparable to BenchmarkFile.
func BenchmarkNativeDiscard(b *testing.B) {
	for _, name := range []string{"zap", "slog", "zerolog", "logrus"} {
		b.Run(name, func(b *testing.B) {
			var log func()
			switch name {
			case "zap":
				cfg := zap.NewProductionEncoderConfig()
				cfg.TimeKey = ""
				l := zap.New(zapcore.NewCore(zapcore.NewJSONEncoder(cfg), zapcore.AddSync(io.Discard), zapcore.InfoLevel))
				log = func() { l.Info("request complete", fields...) }
			case "slog":
				l := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
					if a.Key == slog.TimeKey {
						return slog.Attr{}
					}
					return a
				}}))
				log = func() { l.LogAttrs(context.Background(), slog.LevelInfo, "request complete", attrs...) }
			case "zerolog":
				l := zerolog.New(io.Discard)
				log = func() {
					l.Info().Str("request_id", "req-123").Int("user_id", 42).Str("method", "GET").Int("status", 200).Bool("cached", true).Msg("request complete")
				}
			case "logrus":
				l := logrus.New()
				l.SetOutput(io.Discard)
				l.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
				log = func() { l.WithFields(logFields).Info("request complete") }
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				log()
			}
		})
	}
}
