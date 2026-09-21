package mlog

import (
	"encoding/json"
	"go.uber.org/zap"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestPerfCachedCallerAndPrefix(t *testing.T) {
	Close()
	defer Close()
	cfg := &ZapConfig{Director: t.TempDir(), Format: "json", Level: "info", SingleFile: true, SingleFileName: "app.log", ShowLine: true, Prefix: "literal2006:"}
	if err := InitialZapChecked("", 0, "info", cfg); err != nil {
		t.Fatal(err)
	}
	base := GLOG()
	v := callerVariantsPtr.Load()
	if withCachedCallerSkip(base, 1) != v.skip1 || withCachedCallerSkip(base, 2) != v.skip2 {
		t.Fatal("caller variants not reused")
	}
	if withCachedCallerSkip(base, 3) == base {
		t.Fatal("fallback failed")
	}
	other := zap.NewNop()
	if withCachedCallerSkip(other, 1) == other {
		t.Fatal("wrong-base fallback failed")
	}
	_, _, line, _ := runtime.Caller(0)
	InfoW("wrapped", zap.Int("v", 1))
	wrappedLine := line + 1
	_, _, line, _ = runtime.Caller(0)
	base.Info("raw", zap.Int("v", 2))
	rawLine := line + 1
	Close()
	if callerVariantsPtr.Load() != nil {
		t.Fatal("cache retained after Close")
	}
	if withCachedCallerSkip(other, 1) == other {
		t.Fatal("nil-cache fallback failed")
	}
	b, err := os.ReadFile(filepath.Join(cfg.Director, "app.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines", len(lines))
	}
	for i, s := range lines {
		var row map[string]any
		if err := json.Unmarshal([]byte(s), &row); err != nil {
			t.Fatal(err)
		}
		want := []int{wrappedLine, rawLine}[i]
		if !strings.HasSuffix(row["caller"].(string), "perf_caller_test.go:"+strconv.Itoa(want)) {
			t.Fatal("caller changed", row)
		}
		if !strings.HasPrefix(row["time"].(string), "literal2006:") {
			t.Fatal("prefix changed", row)
		}
	}
}
