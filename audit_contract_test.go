package mlog

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func auditObserve(t *testing.T) (*zap.Logger, *observer.ObservedLogs) {
	t.Helper()
	Close()
	SetLogSafetyMode(SafetyModeDefault)
	atomicLevel.SetLevel(zapcore.DebugLevel)
	c, o := observer.New(atomicLevel)
	l := zap.New(c)
	globalMutex.Lock()
	zapConfig = ZapConfig{Level: "debug", Format: "json", Director: t.TempDir()}
	zapLogger = l
	loggerPtr.Store(l)
	atomic.StoreInt32(&initialized, 1)
	updateLevelCacheOptimized(zapcore.DebugLevel)
	globalMutex.Unlock()
	t.Cleanup(func() {
		Close()
		SetLogSafetyMode(SafetyModeDefault)
		atomic.StoreInt32(&stopFlag, 0)
		atomic.StoreInt32(&stopNetFlag, 0)
	})
	return l, o
}
func auditPanic(t *testing.T, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Error("expected panic")
		}
	}()
	f()
}

func TestAuditFormattingContracts(t *testing.T) {
	sf := NewSafeFormatter()
	type S struct {
		Name   string
		hidden string
		Zero   int
		Other  any
		Child  *S
	}
	cycle := &S{Name: "node"}
	cycle.Child = cycle
	for _, tc := range []struct {
		v    any
		want string
	}{
		{nil, "<nil>"}, {true, "true"}, {42, "42"}, {[]byte("abc"), "[97 98 99]"}, {fmt.Errorf("broken"), "broken"},
		{map[string]int{"secret": 5}, "<map[string]int>"}, {map[string]int(nil), "nil"}, {[]int(nil), "<nil>"}, {[]int{1, 2}, "[1 2]"},
		{make([]int, 11), "[11 items of int]"}, {[2]int{3, 4}, "[3 4]"}, {(*int)(nil), "<nil>"},
		{make(chan int), "<chan int>"}, {func() {}, "<func()>"}, {uintptr(7), "7"}, {panicError{}, "<format panic>"},
	} {
		if got := sf.FormatSafely("%v", tc.v); got != tc.want {
			t.Errorf("%T: got %q want %q", tc.v, got, tc.want)
		}
	}
	if got := sf.FormatSafely("%v", cycle); !strings.Contains(got, "<max-depth>") {
		t.Fatal("cycle was not bounded")
	}
	remaining := 1024
	if sf.makeComplexArgSafeDepth(nil, 0, &remaining) != nil {
		t.Fatal("invalid reflect value")
	}
	fanout := make([]any, 10)
	for i := range fanout {
		fanout[i] = fanout
	}
	if result := sf.FormatSafely("%v", fanout); !strings.Contains(result, "<max-items>") {
		t.Fatal("unbounded traversal")
	}
	s := sf.FormatSafely("%v", S{Name: "visible", hidden: "secret", Other: 3})
	if strings.Contains(s, "secret") || !strings.Contains(s, "Name:visible") {
		t.Fatal(s)
	}
	for _, v := range []any{"", false, int(0), uint(0), float32(0), float64(0), []int{}, map[string]int(nil), (*int)(nil)} {
		if !isZeroValue(reflect.ValueOf(v)) {
			t.Errorf("expected zero: %T", v)
		}
	}
	for _, v := range []any{struct{}{}, complex(1, 2), true, uint(1), float64(1), "x"} {
		if isZeroValue(reflect.ValueOf(v)) {
			t.Errorf("expected nonzero: %T", v)
		}
	}
	var iface any
	if !isZeroValue(reflect.ValueOf(&iface).Elem()) {
		t.Error("nil interface")
	}
	if SafeFormat("literal %s") != "literal %s" {
		t.Fatal("literal formatting changed")
	}
	for _, tc := range []struct {
		f    string
		a    []any
		want string
	}{
		{"plain", []any{1, "x"}, "plain 1 x"}, {"%s", []any{"x"}, "x"}, {"%d", []any{3}, "3"}, {"%d", []any{int64(4)}, "4"},
		{"%v", []any{true}, "true"}, {"%s %d", []any{"x", 7}, "x 7"}, {"%s", []any{7}, "%!s(int=7)"}, {"%d", []any{"x"}, "%!d(string=x)"},
	} {
		var b strings.Builder
		formatToStringBuilder(&b, tc.f, tc.a...)
		if b.String() != tc.want {
			t.Fatalf("format: %q", b.String())
		}
	}
	for _, mode := range []LogSafetyMode{SafetyModeDefault, SafetyModeAlways, SafetyModeNever, 99} {
		SetLogSafetyMode(mode)
		for _, async := range []bool{false, true} {
			want := async
			if mode == SafetyModeAlways {
				want = true
			}
			if mode == SafetyModeNever {
				want = false
			}
			if shouldUseSafeFormat(async) != want {
				t.Fatal("mode policy")
			}
		}
	}
	SetLogSafetyMode(SafetyModeAlways)
	if !strings.Contains(formatMessage("%v", []any{map[int]int{}}, false), "map[int]int") {
		t.Fatal("safe mode ignored")
	}
	SetLogSafetyMode(SafetyModeDefault)
}

type panicError struct{}

func (panicError) Error() string { panic("user formatter") }

func TestAuditConfigurationAndLifecycle(t *testing.T) {
	Close()
	if GLOG() != nil || isInitialized() {
		t.Fatal("closed state")
	}
	if Flush() != nil {
		t.Fatal("closed flush")
	}
	if GetVersion() != Version || GetBuildInfo()["version"] != Version {
		t.Fatal("build metadata")
	}
	d := t.TempDir()
	f := filepath.Join(d, "config.yaml")
	if _, e := LoadConfig(f); e == nil {
		t.Fatal("missing config accepted")
	}
	for _, text := range []string{"level: [", "unknown-option: true"} {
		os.WriteFile(f, []byte(text), 0600)
		if _, e := LoadConfig(f); e == nil {
			t.Fatal("invalid config accepted")
		}
	}
	os.WriteFile(f, []byte("level: info\nformat: json\n"), 0600)
	cfg, e := LoadConfig(f)
	if e != nil || cfg.Level != "info" {
		t.Fatal(cfg, e)
	}
	if e = cfg.Validate(); e != nil || cfg.Director != "logs" || cfg.AsyncBufferSize != 10000 {
		t.Fatal(cfg, e)
	}
	for _, bad := range []ZapConfig{{Level: "bad"}, {Format: "yaml"}, {SingleFileName: "../x"}, {MaxSize: -1}, {MaxBackups: -1}, {RetentionDay: -1}, {AsyncBufferSize: -1}, {AsyncBufferSize: 1000001}, {MaxRouteWriters: -1}} {
		if bad.Validate() == nil {
			t.Errorf("invalid config accepted: %+v", bad)
		}
	}
	cfg = &ZapConfig{Director: d, Format: "json", SingleFile: true, ShowLine: true, UseRelativePath: true, BuildRootPath: d, EnableAsync: true, AsyncBufferSize: 2}
	if e = InitialZapChecked("service", 8, "debug", cfg); e != nil {
		t.Fatal(e)
	}
	copied := GetConfig()
	copied.Level = "fatal"
	if GetConfig().Level == "fatal" {
		t.Fatal("GetConfig aliases live config")
	}
	if GLOG() == nil || !CheckLevel("INFO") || CheckLevel("nonsense") {
		t.Fatal("initialization or level check")
	}
	UpdateLevel("warn")
	if CheckLevel("info") || !CheckLevel("error") {
		t.Fatal("level update")
	}
	UpdateLevel("bad")
	if CheckLevel("info") {
		t.Fatal("invalid update changed level")
	}
	UpdateLevel("debug")
	if e = InitialZapChecked("../escape", 0, "info", cfg); e == nil {
		t.Fatal("service traversal accepted")
	}
	auditPanic(t, func() { InitialZap("", 0, "invalid", cfg) })
	Info("queued")
	if e = Flush(); e != nil {
		t.Fatal(e)
	}
	if !isAsyncEnabled() {
		t.Fatal("Flush disabled async")
	}
	cfg.EnableAsync = false
	cfg.UseRelativePath = false
	InitialZap("service", 8, "info", cfg)
	if isAsyncEnabled() {
		t.Fatal("async survived sync reinitialization")
	}
	InitialZap("", 0, "", nil)
	Close()
	Close()
	notDir := filepath.Join(d, "notdir")
	os.WriteFile(notDir, []byte("x"), 0600)
	if e = InitialZapChecked("", 0, "info", &ZapConfig{Director: notDir}); e == nil {
		t.Fatal("file accepted as log directory")
	}
}

func TestAuditWrapperContracts(t *testing.T) {
	_, o := auditObserve(t)
	for _, f := range []func(string, ...any){Debug, Info, Warn, Error, Lock, Critical, Disaster} {
		f("literal")
		f("value=%d", 9)
	}
	for _, f := range []func(string, ...zap.Field){DebugW, InfoW, WarnW, ErrorW} {
		f("structured", zap.Int("id", 9))
	}
	if ReturnError("no args").Error() != "no args" || ReturnError("id=%d", 7).Error() != "id=7" {
		t.Fatal("ReturnError text")
	}
	for _, f := range []func(string, ...any){GrpcAssert, AssertString} {
		f("literal")
		f("id=%d", 1)
	}
	globalMutex.Lock()
	zapConfig.UseRelativePath = true
	globalMutex.Unlock()
	initPathCache()
	GrpcAssert("relative")
	AssertString("relative %s", "path")
	if len(o.All()) < 20 {
		t.Fatal("missing wrapper output")
	}
	atomic.StoreInt32(&stopFlag, 0)
	atomic.StoreInt32(&stopNetFlag, 0)
	if StopFlag() || StopNetFlag() {
		t.Fatal("unexpected flags")
	}
	SetStopFlag()
	SetStopNetFlag()
	if !StopFlag() || !StopNetFlag() {
		t.Fatal("flags did not set")
	}
	ExitGame("already stopped %d", 1)
	atomic.StoreInt32(&stopFlag, 0)
	auditPanic(t, func() { ExitGame("exit") })
	auditPanic(t, func() { ExitGame("exit %d", 3) })
	UpdateLevel("fatal")
	before := o.Len()
	Debug("off")
	Info("off")
	Warn("off")
	Error("off")
	DebugW("off")
	InfoW("off")
	WarnW("off")
	ErrorW("off")
	GrpcAssert("off")
	AssertString("off")
	if o.Len() != before {
		t.Fatal("disabled logging emitted records")
	}
	Close()
	auditPanic(t, func() { ExitGame("uninitialized %d", 1) })
	// A logger can disappear after a fast-path check during shutdown.
	updateLevelCacheOptimized(zapcore.DebugLevel)
	for _, f := range []func(string, ...any){zapDebug, zapInfo, zapWarn, zapError, Lock, Critical, Disaster, GrpcAssert, AssertString} {
		f("closed")
	}
	for _, f := range []func(string, ...zap.Field){DebugW, InfoW, WarnW, ErrorW} {
		f("closed")
	}
	SetStopFlag()
	SetStopNetFlag()
}

func TestAuditEncodersAndPaths(t *testing.T) {
	Close()
	oldWD := workingDir
	t.Cleanup(func() { workingDir = oldWD; globalPathCache.Store(nil) })
	cfg := ZapConfig{Format: "json", Prefix: "PREFIX-"}
	for _, lv := range []string{"LowercaseLevelEncoder", "LowercaseColorLevelEncoder", "CapitalLevelEncoder", "CapitalColorLevelEncoder", "other"} {
		cfg.EncodeLevel = lv
		b, e := cfg.Encoder().EncodeEntry(zapcore.Entry{Level: zapcore.WarnLevel, Time: time.Unix(0, 0), Message: "m"}, nil)
		if e != nil || !strings.Contains(b.String(), "PREFIX-") {
			t.Fatal(e)
		}
		b.Free()
	}
	cfg.Format = "console"
	b, e := cfg.Encoder().EncodeEntry(zapcore.Entry{Level: zapcore.InfoLevel, Time: time.Now(), Message: "console"}, nil)
	if e != nil || !strings.Contains(b.String(), "console") {
		t.Fatal(e)
	}
	b.Free()
	cfg.UseRelativePath = true
	cfg.BuildRootPath = "/src/app"
	enc := cfg.CallerEncoder()
	a := &auditArray{}
	enc(zapcore.EntryCaller{}, a)
	if a.Strings[0] != "undefined" {
		t.Fatal(a)
	}
	enc(zapcore.NewEntryCaller(1, "/src/app/a.go", 7, true), a)
	if a.Strings[1] != "a.go:7" {
		t.Fatal(a)
	}
	globalMutex.Lock()
	zapConfig = ZapConfig{BuildRootPath: "/src/app"}
	globalMutex.Unlock()
	globalPathCache.Store(nil)
	a = &auditArray{}
	RelativeCallerEncoder(zapcore.EntryCaller{}, a)
	RelativeCallerEncoder(zapcore.NewEntryCaller(0, "/src/app/a.go", 8, true), a)
	if a.Strings[1] != "a.go:8" {
		t.Fatal(a)
	}
	for _, tc := range []struct{ abs, root, want string }{{"/src/app/a.go", "/src/app", "a.go"}, {"/other/a.go", "/src/app", ""}, {"/src/app2/a.go", "/src/app", ""}, {"/src/app", "/src/app", ""}} {
		if x := getRelativePathFromBuildRoot(tc.abs, tc.root); x != tc.want {
			t.Errorf("root path %q", x)
		}
	}
	globalMutex.Lock()
	zapConfig.BuildRootPath = ""
	globalMutex.Unlock()
	for _, tc := range []struct{ wd, path, want string }{{"", "/x/aimmo/a.go", "aimmo/a.go"}, {"/src", "/src/a.go", "a.go"}, {"/src", "/other/a.go", "/other/a.go"}, {"/src/app", "/src/app2/a.go", "/src/app2/a.go"}, {"foo", "/foo/a.go", "foo/a.go"}} {
		workingDir = tc.wd
		if x := getRelativePathLegacy(tc.path); x != tc.want {
			t.Errorf("legacy got %q want %q", x, tc.want)
		}
	}
	if extractRelativeFromPath("a.go") != "a.go" || extractRelativeFromPath("/a/b.go") != "a/b.go" {
		t.Fatal("extract")
	}
	var nilCache *PathCache
	nilCache.ClearCache()
	nilCache.UpdateWorkingDirectory("x")
	if h, m := nilCache.GetCacheStats(); h != 0 || m != 0 {
		t.Fatal("nil cache stats")
	}
	globalPathCache.Store(nil)
	updateBuildRoot("/none")
	workingDir = "/src"
	initPathCache()
	pc := globalPathCache.Load()
	updateBuildRoot("/src/app")
	if x := getRelativePath("/src/app/a.go"); x != "a.go" {
		t.Fatal(x)
	}
	if x := getRelativePath("/src/app/a.go"); x != "a.go" {
		t.Fatal(x)
	}
	pc.UpdateWorkingDirectory("/src")
	pc.ClearCache()
	pc.getRelativePathCached("/outside/a.go")
	if h, _ := pc.GetCacheStats(); h != 1 {
		t.Fatal("cache occupancy")
	}
	for _, v := range []string{"/src/app/a.go", "/other/a.go", "/src/app2/a.go", "/src/app"} {
		pc.getRelativePathFromBuildRootCached(v)
	}
	pc.buildRoot = ""
	for _, tc := range []struct{ wd, path, want string }{{"", "/x/mlog/a.go", "mlog/a.go"}, {"/src", "/src/a.go", "a.go"}, {"/src/app", "/src/app2/a.go", "/src/app2/a.go"}, {"foo", "/foo/a.go", "foo/a.go"}} {
		pc.workDir = tc.wd
		if x := pc.computeRelativePath(tc.path); x != tc.want {
			t.Errorf("cached %q", x)
		}
	}
	for _, x := range []string{"a.go", "pkg/a.go", "/pkg/a.go"} {
		if pc.extractLastTwoSegments(x) != x && x != "/pkg/a.go" {
			t.Error("last segments")
		}
	}
	if !pc.isProjectFile("/x/plugin/a.go") || pc.isProjectFile("/none/a.go") {
		t.Fatal("project detection")
	}
	pc.workDir = "/src"
	pc.buildRoot = "/src"
	stack := "f()\n\t/src/a.go:17 +0x12"
	if !strings.Contains(convertStackPathsToRelative(stack), "a.go:17") {
		t.Fatal("optimized stack")
	}
	globalPathCache.Store(nil)
	if !strings.Contains(convertStackPathsToRelativeOptimized(stack), "a.go:17") {
		t.Fatal("fallback stack")
	}
	convertStackPathsToRelative(stack)
	for _, x := range []string{"plain", "/src/a.go:7", "/src/a.go +0x3", "/src/a.go"} {
		replaceAbsolutePathInLine(x)
		replacePathInField(x)
	}
	if BytesToString([]byte{'a', 0, 'b'}) != "a" || BytesToString([]byte("ab")) != "ab" {
		t.Fatal("byte conversion")
	}
}

// Embedding supplies unused primitive methods; caller encoders only append strings.
type auditArray struct {
	zapcore.PrimitiveArrayEncoder
	Strings []string
}

func (a *auditArray) AppendString(s string) { a.Strings = append(a.Strings, s) }

func TestAuditSyncErrors(t *testing.T) {
	harmless := &os.PathError{Op: "sync", Path: "/dev/stdout", Err: syscall.EINVAL}
	disk := &os.PathError{Op: "sync", Path: "data.log", Err: syscall.EINVAL}
	for _, tc := range []struct {
		err error
		ok  bool
	}{{nil, true}, {harmless, true}, {fmt.Errorf("wrap: %w", harmless), true}, {errors.Join(harmless, harmless), true}, {disk, false}, {errors.Join(harmless, disk), false}, {fmt.Errorf("wrap: %w", errors.Join(harmless, disk)), false}, {&os.PathError{Op: "write", Path: "/dev/stdout", Err: syscall.EINVAL}, false}, {errors.New("invalid argument"), false}} {
		if isHarmlessSyncError(tc.err) != tc.ok {
			t.Fatalf("misclassified %v", tc.err)
		}
	}
	for _, err := range []error{nil, harmless, disk} {
		got := syncLoggerSafely(zap.New(&auditErrorCore{err: err}))
		if (got == nil) != isHarmlessSyncError(err) {
			t.Fatal("sync error hidden")
		}
	}
	old := os.Stdout
	f, e := os.CreateTemp(t.TempDir(), "stdout")
	if e != nil {
		t.Fatal(e)
	}
	os.Stdout = f
	if isInteractiveTerminal() {
		t.Error("regular file is not a terminal")
	}
	f.Close()
	if isInteractiveTerminal() {
		t.Error("closed output is not terminal")
	}
	os.Stdout = old
}

type auditErrorCore struct{ err error }

func (c *auditErrorCore) Enabled(zapcore.Level) bool        { return true }
func (c *auditErrorCore) With([]zapcore.Field) zapcore.Core { return c }
func (c *auditErrorCore) Check(e zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	return ce.AddCore(e, c)
}
func (c *auditErrorCore) Write(zapcore.Entry, []zapcore.Field) error { return c.err }
func (c *auditErrorCore) Sync() error                                { return c.err }

func TestAuditConcurrentSettings(t *testing.T) {
	auditObserve(t)
	initPathCache()
	pc := globalPathCache.Load()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				SetLogSafetyMode(LogSafetyMode(j % 3))
				GetLogSafetyMode()
				formatMessage("%d", []any{j}, false)
				updateBuildRoot("/src")
				pc.UpdateWorkingDirectory("/src")
				pc.getRelativePathCached("/src/a.go")
				GetConfig()
				UpdateLevel([]string{"info", "warn"}[j%2])
			}
		}(i)
	}
	wg.Wait()
}

func TestAuditCacheUtilities(t *testing.T) {
	p := NewStringBuilderPool()
	b := p.Get()
	b.WriteString("secret")
	p.Put(b)
	b = p.Get()
	if b.Len() != 0 {
		t.Fatal("builder contents retained")
	}
	p.Put(b)
	c := NewOptimizedSkipCache(2)
	if _, ok := c.Get(1); ok {
		t.Fatal("unexpected hit")
	}
	c.Set(1, 3)
	c.Set(1, 9)
	c.Set(2, 4)
	c.Set(3, 5)
	if v, ok := c.Get(1); !ok || v != 3 {
		t.Fatal("cache replaced")
	}
	h, m, n, r := c.GetStats()
	if h != 1 || m != 1 || n != 2 || r != 0.5 {
		t.Fatal(h, m, n, r)
	}
	c.Clear()
	_, _, n, r = c.GetStats()
	if n != 0 || r != 0 {
		t.Fatal("cache not cleared")
	}
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); c.Set(uintptr(i), i) }(i)
	}
	wg.Wait()
	_, _, n, _ = c.GetStats()
	if n > 2 {
		t.Fatal("cache capacity raced")
	}
}

func TestAuditRouteAndCoreContracts(t *testing.T) {
	auditObserve(t)
	for _, v := range []string{"", ".", "..", "../x", "a/b", "a\\b", "C:", "x\x00", "x\n", "CON", "nul.log", "COM1", "LPT9.txt", "a.", "a "} {
		if validComponent(v) {
			t.Errorf("unsafe component %q", v)
		}
	}
	for _, v := range []string{"orders", "order-1.log", "COM0", "LPT10"} {
		if !validComponent(v) {
			t.Errorf("safe component %q", v)
		}
	}
	if validateRoute("orders/eu") != nil || validateRoute("") != nil || validateRoute("/root") == nil {
		t.Fatal("route validation")
	}
	d := t.TempDir()
	globalMutex.Lock()
	zapConfig = ZapConfig{Director: d, Format: "json", MaxRouteWriters: 1}
	globalMutex.Unlock()
	z := NewZapCoreWithService(zapcore.InfoLevel, "svc", 2)
	t.Cleanup(func() { z.Close() })
	if z.getLogFileName() != "info.log" || !z.Enabled(zapcore.InfoLevel) || z.Enabled(zapcore.DebugLevel) {
		t.Fatal("severity routing")
	}
	e := zapcore.Entry{Level: zapcore.InfoLevel, Message: "hello", Time: time.Now()}
	if z.Check(zapcore.Entry{Level: zapcore.DebugLevel}, nil) != nil {
		t.Fatal("disabled check")
	}
	b := z.With([]zapcore.Field{zap.String("directory", "orders"), zap.Int("first", 1)}).With([]zapcore.Field{zap.Int("second", 2)})
	if b.Check(zapcore.Entry{Level: zapcore.DebugLevel}, nil) != nil || !b.Enabled(zapcore.InfoLevel) {
		t.Fatal("bound level")
	}
	b.Check(e, nil).Write(zap.Int("third", 3))
	if err := b.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := z.Write(e, []zapcore.Field{zap.String("folder", "orders")}); err != nil {
		t.Fatal(err)
	}
	if err := z.Write(e, []zapcore.Field{zap.String("business", "second")}); err == nil {
		t.Fatal("route writer limit ignored")
	}
	if err := z.Write(e, []zapcore.Field{zap.Int("directory", 7)}); err == nil {
		t.Fatal("non-string route accepted")
	}
	if err := z.Write(e, []zapcore.Field{zap.String("directory", "../escape")}); err == nil {
		t.Fatal("traversal accepted")
	}
	if err := z.Write(e, nil); err != nil {
		t.Fatal(err)
	}
	z.Close()
	z.Close()
	if !errors.Is(z.Write(e, nil), ErrClosed) || !errors.Is(z.Sync(), ErrClosed) {
		t.Fatal("closed core reopened")
	}
	if _, err := z.WriteSyncer("new").Write([]byte("x")); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(d, "2", "svc", "orders", "info.log"))
	if err != nil || !bytes.Contains(data, []byte(`"third":3`)) {
		t.Fatal(string(data), err)
	}
}
