package mlog

import (
	"bytes"
	"encoding/json"
	"errors"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type auditBadCloser struct{}

func (auditBadCloser) Write(p []byte) (int, error) { return len(p), nil }
func (auditBadCloser) Close() error                { return errors.New("close failed") }

func TestAuditFilesystemFailures(t *testing.T) {
	auditObserve(t)
	d := t.TempDir()
	z := &ZapCore{config: ZapConfig{Director: d, Format: "json", SingleFile: true, SingleFileName: "custom.log"}, specialLoggers: make(map[string]io.WriteCloser)}
	if z.getLogFileName() != "custom.log" {
		t.Fatal("custom filename")
	}
	bad := z.createWriteSyncer("../bad", 0)
	if bad.Sync() == nil {
		t.Fatal("bad service")
	}
	z.config.SingleFileName = "../bad"
	if z.WriteSyncer().Sync() == nil {
		t.Fatal("bad filename")
	}
	z.config.SingleFileName = "custom.log"
	block := filepath.Join(d, "blocked")
	os.WriteFile(block, []byte("x"), 0600)
	if z.createWriteSyncer("blocked", 0).Sync() == nil {
		t.Fatal("mkdir failure hidden")
	}
	if prepareLogDirectory("relative", d) == nil {
		t.Fatal("relative/absolute mismatch accepted")
	}
	if prepareLogDirectory(d, filepath.Dir(d)) == nil {
		t.Fatal("outside root accepted")
	}
	link := filepath.Join(d, "linked")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	if prepareLogDirectory(d, link) == nil {
		t.Fatal("symlink directory accepted")
	}
	logPath := filepath.Join(d, "custom.log")
	os.Symlink(filepath.Join(t.TempDir(), "log"), logPath)
	if z.WriteSyncer().Sync() == nil {
		t.Fatal("symlink logfile accepted")
	}
	os.Remove(logPath)
	z.config.LogInConsole = true
	w := z.WriteSyncer()
	if _, err := w.Write([]byte("audit console sink\n")); err != nil {
		t.Fatal(err)
	}
	z.lumberjackLogger.Close()
	// Error-injecting dependencies exercise real close/sync error propagation.
	failure := errors.New("disk I/O failed")
	z = &ZapCore{Core: &auditErrorCore{err: failure}, lumberjackLogger: auditBadCloser{}, specialLoggers: map[string]io.WriteCloser{"route": auditBadCloser{}}}
	if !errors.Is(z.Sync(), failure) {
		t.Fatal("sync failure hidden")
	}
	if !errors.Is(z.Close(), failure) {
		t.Fatal("close failure hidden")
	}
	coreMutex.Lock()
	zapCores = []*ZapCore{{Core: &auditErrorCore{err: failure}, specialLoggers: map[string]io.WriteCloser{}}}
	coreMutex.Unlock()
	Close()
	// Console error must never cause another file error in a multi-error to vanish.
	z = &ZapCore{Core: zap.NewNop().Core(), specialLoggers: map[string]io.WriteCloser{}}
	if z.Close() != nil {
		t.Fatal("noop close")
	}
}

func FuzzAuditRouteComponents(f *testing.F) {
	for _, s := range []string{"orders", "../escape", "a/b", "C:\\windows", "NUL.log"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if validComponent(s) {
			if filepath.IsAbs(s) || s == ".." || s == "." {
				t.Fatal("unsafe accepted component", s)
			}
		}
	})
}

func TestAuditWithEagerSnapshotAndInvalidContext(t *testing.T) {
	d := t.TempDir()
	cfg := ZapConfig{Director: d, Format: "json"}
	InitialZap("", 0, "info", &cfg)
	defer Close()
	bad := GLOG().Core().With([]zapcore.Field{zap.Int("directory", 3)})
	if err := bad.Write(zapcore.Entry{Level: zapcore.InfoLevel, Message: "bad"}, nil); err == nil {
		t.Fatal("invalid With context accepted")
	}
	values := map[string]int{"value": 7}
	l := GLOG().With(zap.Any("data", values), zap.String("directory", "snapshot"))
	values["value"] = 9
	l.Info("bound")
	Close()
	data, err := os.ReadFile(filepath.Join(d, "snapshot", "info.log"))
	if err != nil || !bytes.Contains(data, []byte(`"value":7`)) {
		t.Fatal("With retained mutable fields", string(data), err)
	}
}

func TestAuditAsyncReferenceSnapshot(t *testing.T) {
	d := t.TempDir()
	cfg := ZapConfig{Director: d, Format: "json", SingleFile: true, EnableAsync: true, AsyncBufferSize: 2}
	InitialZap("", 0, "info", &cfg)
	data := map[string]int{"value": 7}
	arr := []int{3, 4}
	InfoW("line\nbreak", zap.Any("data", data), zap.Ints("array", arr))
	data["value"] = 999
	arr[0] = 999
	if err := Flush(); err != nil {
		t.Fatal(err)
	}
	Close()
	b, err := os.ReadFile(filepath.Join(d, "all.log"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(b, []byte("\n")) != 1 || !bytes.Contains(b, []byte(`"value":7`)) || !bytes.Contains(b, []byte(`"array":[3,4]`)) {
		t.Fatal("snapshot or JSON framing failed", string(b))
	}
	var record map[string]any
	if err := json.Unmarshal(b, &record); err != nil {
		t.Fatal(err)
	}
}
