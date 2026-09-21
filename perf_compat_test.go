package mlog

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// This is the pre-optimization encoder configuration. Keep it independent of
// Encoder() so changes are checked against the established wire format.
func perfLegacyEncoder(c ZapConfig) zapcore.Encoder {
	ec := zapcore.EncoderConfig{
		TimeKey: "time", NameKey: "name", LevelKey: "level", CallerKey: "caller",
		MessageKey: "message", StacktraceKey: c.StacktraceKey, LineEnding: zapcore.DefaultLineEnding,
		EncodeTime: func(t time.Time, e zapcore.PrimitiveArrayEncoder) {
			e.AppendString(c.Prefix + t.Format("2006-01-02 15:04:05.000"))
		},
		EncodeLevel: c.LevelEncoder(), EncodeCaller: c.CallerEncoder(), EncodeDuration: zapcore.SecondsDurationEncoder,
	}
	if c.Format == "json" {
		return zapcore.NewJSONEncoder(ec)
	}
	return zapcore.NewConsoleEncoder(ec)
}

func TestPerfTimeWireCompatibility(t *testing.T) {
	for _, format := range []string{"json", "console"} {
		for _, prefix := range []string{"", "literal2006:", "quote\"slash\\\n", "日志\t"} {
			cfg := ZapConfig{Format: format, Prefix: prefix, StacktraceKey: "stack"}
			for _, when := range []time.Time{time.Time{}, time.Unix(-1, 999999999), time.Unix(0, 0), time.Date(2026, 9, 21, 14, 30, 5, 123456789, time.FixedZone("local", 8*60*60))} {
				e := zapcore.Entry{Level: zapcore.InfoLevel, Time: when, Message: "line\n\"break", LoggerName: "service", Stack: "stack\nline", Caller: zapcore.NewEntryCaller(1, "source.go", 7, true)}
				fields := []zap.Field{zap.String("unicode", "你好"), zap.Int("n", 42)}
				old, err := perfLegacyEncoder(cfg).EncodeEntry(e, fields)
				if err != nil {
					t.Fatal(err)
				}
				got, err := cfg.Encoder().EncodeEntry(e, fields)
				if err != nil {
					old.Free()
					t.Fatal(err)
				}
				equal := bytes.Equal(old.Bytes(), got.Bytes())
				if !equal {
					t.Errorf("wire format changed: %s %q\nold=%s\nnew=%s", format, prefix, old.String(), got.String())
				}
				old.Free()
				got.Free()
			}
		}
	}
}

func perfReadRecords(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var v map[string]any
		if err := json.Unmarshal(line, &v); err != nil {
			t.Fatal(err, string(line))
		}
		out = append(out, v)
	}
	return out
}

func TestPerfPublicCallerCompatibility(t *testing.T) {
	for _, async := range []bool{false, true} {
		for _, show := range []bool{false, true} {
			cfg := ZapConfig{Director: t.TempDir(), Format: "json", SingleFile: true, SingleFileName: "app.log", ShowLine: show, EnableAsync: async, AsyncBufferSize: 8}
			if err := InitialZapChecked("", 0, "debug", &cfg); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(Close)
			var want []int
			_, _, ln, _ := runtime.Caller(0)
			Debug("printf %d", 1)
			want = append(want, ln+1)
			_, _, ln, _ = runtime.Caller(0)
			Info("printf %d", 2)
			want = append(want, ln+1)
			_, _, ln, _ = runtime.Caller(0)
			Warn("printf %d", 3)
			want = append(want, ln+1)
			_, _, ln, _ = runtime.Caller(0)
			Error("printf %d", 4)
			want = append(want, ln+1)
			_, _, ln, _ = runtime.Caller(0)
			DebugW("structured", zap.Int("n", 5))
			want = append(want, ln+1)
			_, _, ln, _ = runtime.Caller(0)
			InfoW("structured", zap.Int("n", 6))
			want = append(want, ln+1)
			_, _, ln, _ = runtime.Caller(0)
			WarnW("structured", zap.Int("n", 7))
			want = append(want, ln+1)
			_, _, ln, _ = runtime.Caller(0)
			ErrorW("structured", zap.Int("n", 8))
			want = append(want, ln+1)
			// GLOG remains synchronous: drain before mixing the raw path, so this
			// test does not impose a new total-order contract on async and raw calls.
			if err := Flush(); err != nil {
				t.Fatal(err)
			}
			_, _, ln, _ = runtime.Caller(0)
			GLOG().Info("raw")
			want = append(want, ln+1)
			Close()
			rows := perfReadRecords(t, filepath.Join(cfg.Director, "app.log"))
			if len(rows) != len(want) {
				t.Fatalf("record count %d", len(rows))
			}
			for i, row := range rows {
				caller, ok := row["caller"]
				if ok != show {
					t.Fatalf("unexpected caller presence: %v", row)
				}
				if show && !strings.HasSuffix(caller.(string), "perf_compat_test.go:"+strconv.Itoa(want[i])) {
					t.Fatalf("async=%v caller changed at %d: %v expected %d", async, i, row, want[i])
				}
			}
		}
	}
}

func TestPerfRouteCopyOnWrite(t *testing.T) {
	z := &ZapCore{}
	cases := []struct {
		fields  []zap.Field
		route   string
		keys    []string
		invalid bool
	}{
		{nil, "", nil, false},
		{[]zap.Field{zap.Int("a", 1), zap.String("b", "x")}, "", []string{"a", "b"}, false},
		{[]zap.Field{zap.String("directory", "orders"), zap.Int("a", 1)}, "orders", []string{"a"}, false},
		{[]zap.Field{zap.Int("a", 1), zap.String("folder", "orders"), zap.Int("b", 2)}, "orders", []string{"a", "b"}, false},
		{[]zap.Field{zap.Int("a", 1), zap.String("business", "orders")}, "orders", []string{"a"}, false},
		{[]zap.Field{zap.String("directory", "a"), zap.String("folder", "b"), zap.String("business", "")}, "", nil, false},
		{[]zap.Field{zap.String("directory", "a"), zap.Int("folder", 1)}, "", nil, true},
	}
	for _, tc := range cases {
		before := append([]zap.Field(nil), tc.fields...)
		route, got, err := z.routeFields(tc.fields)
		if (err != nil) != tc.invalid {
			t.Fatal(err)
		}
		if !tc.invalid {
			if route != tc.route {
				t.Fatal(route, tc.route)
			}
			var keys []string
			for _, f := range got {
				keys = append(keys, f.Key)
			}
			if !reflect.DeepEqual(keys, tc.keys) {
				t.Fatal(keys, tc.keys)
			}
		}
		if !reflect.DeepEqual(tc.fields, before) {
			t.Fatal("caller-owned fields mutated")
		}
	}
	fields := []zap.Field{zap.Int("a", 1), zap.Int("b", 2)}
	if allocs := testing.AllocsPerRun(1000, func() { z.routeFields(fields) }); allocs != 0 {
		t.Fatal("route-free filtering allocated", allocs)
	}
}

func TestPerfRouteSinkCacheLifecycle(t *testing.T) {
	cfg := ZapConfig{Director: t.TempDir(), Format: "json", MaxRouteWriters: 1}
	z := newZapCoreWithConfig(zapcore.InfoLevel, "svc", 7, cfg, zap.NewAtomicLevelAt(zapcore.InfoLevel))
	defer z.Close()
	e := zapcore.Entry{Level: zapcore.InfoLevel, Time: time.Now(), Message: "cached"}
	for i := 0; i < 3; i++ {
		if err := z.Write(e, []zap.Field{zap.String("directory", "orders"), zap.Int("n", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := z.routeSyncers.Load("orders"); !ok {
		t.Fatal("sink not cached")
	}
	for _, route := range []string{"../bad", "second"} {
		if err := z.Write(e, []zap.Field{zap.String("directory", route)}); err == nil {
			t.Fatal("invalid/capacity failure lost")
		}
		if _, ok := z.routeSyncers.Load(route); ok {
			t.Fatal("failure cached")
		}
	}
	oldBound := z.With([]zap.Field{zap.String("directory", "orders"), zap.Int("bound", 1)})
	if err := oldBound.Write(e, nil); err != nil {
		t.Fatal(err)
	}
	if len(z.specialLoggers) != 1 {
		t.Fatal("writer limit changed")
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := z.routeSyncers.Load("orders"); ok {
		t.Fatal("cache retained on Close")
	}
	if !errors.Is(oldBound.Write(e, nil), ErrClosed) {
		t.Fatal("old bound core reopened")
	}
	if len(perfReadRecords(t, filepath.Join(cfg.Director, "7", "svc", "orders", "info.log"))) != 4 {
		t.Fatal("records lost")
	}
}

func TestPerfRoutedConsoleResolvesStdout(t *testing.T) {
	original := os.Stdout
	defer func() { os.Stdout = original }()
	d := t.TempDir()
	a, err := os.Create(filepath.Join(d, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := os.Create(filepath.Join(d, "b.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	os.Stdout = a
	cfg := ZapConfig{Director: filepath.Join(d, "logs"), Format: "json", LogInConsole: true, MaxRouteWriters: 1}
	z := newZapCoreWithConfig(zapcore.InfoLevel, "", 0, cfg, zap.NewAtomicLevelAt(zapcore.InfoLevel))
	defer z.Close()
	for i, f := range []*os.File{a, b} {
		os.Stdout = f
		if err := z.Write(zapcore.Entry{Level: zapcore.InfoLevel, Time: time.Now(), Message: "console"}, []zap.Field{zap.String("directory", "orders"), zap.Int("n", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := z.routeSyncers.Load("orders"); ok {
		t.Fatal("stdout incorrectly captured")
	}
	for i, path := range []string{a.Name(), b.Name()} {
		rows := perfReadRecords(t, path)
		if len(rows) != 1 || rows[0]["n"] != float64(i) {
			t.Fatal("stdout routing changed", rows)
		}
	}
}

func TestPerfCachedCallerGeneration(t *testing.T) {
	cfg := ZapConfig{Director: t.TempDir(), Format: "json", SingleFile: true}
	if err := InitialZapChecked("", 0, "info", &cfg); err != nil {
		t.Fatal(err)
	}
	defer Close()
	old := GLOG()
	oldWrapped := withCachedCallerSkip(old, 1)
	cfg.Director = t.TempDir()
	if err := InitialZapChecked("", 0, "info", &cfg); err != nil {
		t.Fatal(err)
	}
	if withCachedCallerSkip(old, 1) == withCachedCallerSkip(GLOG(), 1) {
		t.Fatal("borrowed replacement logger")
	}
	if !errors.Is(oldWrapped.Core().Write(zapcore.Entry{Level: zapcore.InfoLevel}, nil), ErrClosed) {
		t.Fatal("old generation reopened")
	}
	InfoW("new", zap.Int("n", 1))
	Close()
	if len(perfReadRecords(t, filepath.Join(cfg.Director, "all.log"))) != 1 {
		t.Fatal("wrong generation output")
	}
}

func TestPerfEncoderReceiverPrefixCompatibility(t *testing.T) {
	for _, format := range []string{"json", "console"} {
		cfg := ZapConfig{Format: format}
		enc := cfg.Encoder()
		entry := zapcore.Entry{Level: zapcore.InfoLevel, Time: time.Unix(0, 0), Message: "prefix"}
		for _, prefix := range []string{"", "changed2006:", "", "quote\"\n"} {
			// Sequential receiver changes were observed by the original encoder.
			// This does not promise safety for unsynchronized concurrent mutation.
			cfg.Prefix = prefix
			got, err := enc.EncodeEntry(entry, nil)
			if err != nil {
				t.Fatal(err)
			}
			old, err := perfLegacyEncoder(cfg).EncodeEntry(entry, nil)
			if err != nil {
				got.Free()
				t.Fatal(err)
			}
			if !bytes.Equal(got.Bytes(), old.Bytes()) {
				t.Errorf("receiver prefix behavior changed: old=%s new=%s", old.String(), got.String())
			}
			old.Free()
			got.Free()
		}
	}
}

func TestPerfLegacyAsyncHelperPaths(t *testing.T) {
	cfg := ZapConfig{Director: t.TempDir(), Format: "json", SingleFile: true, EnableAsync: true, AsyncBufferSize: 8}
	if err := InitialZapChecked("", 0, "debug", &cfg); err != nil {
		t.Fatal(err)
	}
	defer Close()
	for _, f := range []func(string, []any, ...zap.Field){debugAsync, infoAsync, warnAsync, errorAsync} {
		f("n=%d", []any{7}, zap.Int("field", 9))
	}
	if err := Flush(); err != nil {
		t.Fatal(err)
	}
	if GetAsyncStats().Processed != 4 {
		t.Fatal("helper records lost")
	}
	Close()
	for _, row := range perfReadRecords(t, filepath.Join(cfg.Director, "all.log")) {
		if row["message"] != "n=7" || row["field"] != float64(9) {
			t.Fatal("helper formatting changed", row)
		}
	}
}

func TestPerfPrintfCompatibility(t *testing.T) {
	SetLogSafetyMode(SafetyModeNever)
	defer SetLogSafetyMode(SafetyModeDefault)
	for _, tc := range []struct {
		format string
		args   []any
		want   string
	}{
		{"plain", []any{1, "x"}, "plain 1 x"},
		{"literal %s", nil, "literal %s"},
		{"%s", []any{"abc"}, "abc"}, {"%s", []any{7}, "%!s(int=7)"},
		{"%d", []any{3}, "3"}, {"%d", []any{int64(4)}, "4"}, {"%d", []any{"x"}, "%!d(string=x)"},
		{"%v", []any{true}, "true"}, {"id=%d status=%s", []any{7, "ok"}, "id=7 status=ok"},
		{"%02d", []any{7}, "07"}, {"%[2]s/%[1]d", []any{7, "ok"}, "ok/7"},
		{"%%", []any{1}, "%%!(EXTRA int=1)"},
	} {
		if got := formatMessage(tc.format, tc.args, false); got != tc.want {
			t.Fatalf("%q: %q != %q", tc.format, got, tc.want)
		}
	}
}

func TestPerfConcurrentRouteCacheMiss(t *testing.T) {
	cfg := ZapConfig{Director: t.TempDir(), Format: "json", MaxRouteWriters: 1}
	z := newZapCoreWithConfig(zapcore.InfoLevel, "", 0, cfg, zap.NewAtomicLevelAt(zapcore.InfoLevel))
	defer z.Close()
	const n = 32
	start := make(chan struct{})
	done := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			<-start
			done <- z.Write(zapcore.Entry{Level: zapcore.InfoLevel, Time: time.Now(), Message: "race"}, []zap.Field{zap.String("directory", "orders"), zap.Int("id", i)})
		}(i)
	}
	close(start)
	for i := 0; i < n; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if len(z.specialLoggers) != 1 {
		t.Fatal("duplicate writer on concurrent miss")
	}
	z.Close()
	rows := perfReadRecords(t, filepath.Join(cfg.Director, "orders", "info.log"))
	seen := make(map[float64]bool)
	for _, row := range rows {
		id := row["id"].(float64)
		if seen[id] {
			t.Fatal("duplicate record")
		}
		seen[id] = true
	}
	if len(seen) != n {
		t.Fatal("records lost", len(seen))
	}
}
