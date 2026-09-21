package mlog

import (
	"go.uber.org/zap"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditSingleFields(t *testing.T) {
	d := t.TempDir()
	InitialZap("", 0, "info", &ZapConfig{Director: d, Format: "json", SingleFile: true})
	InfoW("audit", zap.Int("id", 42))
	Close()
	b, e := os.ReadFile(filepath.Join(d, "all.log"))
	if e != nil || !strings.Contains(string(b), `"id":42`) {
		t.Fatalf("structured field missing: %s err=%v", b, e)
	}
}
func TestAuditWithRouting(t *testing.T) {
	d := t.TempDir()
	InitialZap("", 0, "info", &ZapConfig{Director: d, Format: "json"})
	GLOG().With(zap.String("directory", "orders")).Info("audit", zap.Int("id", 42))
	Close()
	if _, e := os.Stat(filepath.Join(d, "orders", "info.log")); e != nil {
		t.Fatalf("With directory routing lost: %v", e)
	}
}
func TestAuditTraversal(t *testing.T) {
	base := t.TempDir()
	d := filepath.Join(base, "logs")
	InitialZap("", 0, "info", &ZapConfig{Director: d, Format: "json"})
	InfoW("audit", zap.String("directory", "../escaped"))
	Close()
	if _, e := os.Stat(filepath.Join(base, "escaped", "info.log")); e == nil {
		t.Fatal("untrusted route escaped log root")
	}
}
