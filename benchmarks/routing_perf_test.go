package benchmarks

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/PinableAgents/mlog"
	"go.uber.org/zap"
)

// This suite compares mlog's routing before/after. It does not pretend that a
// raw Zerolog file writer implements mlog's routing/lifecycle contract.
func routeSubject(t testing.TB, workload string) *subject {
	t.Helper()
	dir := t.TempDir()
	cfg := mlog.ZapConfig{Director: dir, Format: "json", MaxSize: 1024, MaxBackups: 1}
	mlog.InitialZap("", 0, "info", &cfg)
	s := &subject{finish: mlog.Close, path: filepath.Join(dir, "info.log")}
	switch workload {
	case "multi-no-route":
		s.structured = func() { mlog.InfoW("request complete", fields...) }
	case "route-dynamic":
		f := append([]zap.Field{zap.String("directory", "orders")}, fields...)
		s.structured = func() { mlog.InfoW("request complete", f...) }
		s.path = filepath.Join(dir, "orders", "info.log")
	case "route-bound":
		l := mlog.GLOG().With(zap.String("directory", "orders"))
		s.structured = func() { l.Info("request complete", fields...) }
		s.path = filepath.Join(dir, "orders", "info.log")
	default:
		t.Fatalf("unknown routing workload %q", workload)
	}
	return s
}

func BenchmarkRouting(b *testing.B) {
	for _, name := range []string{"multi-no-route", "route-dynamic", "route-bound"} {
		b.Run(name, func(b *testing.B) {
			s := routeSubject(b, name)
			s.structured()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s.structured()
			}
			s.finish()
			b.StopTimer()
		})
	}
}

func TestRoutingCompletedOutput(t *testing.T) {
	for _, name := range []string{"multi-no-route", "route-dynamic", "route-bound"} {
		t.Run(name, func(t *testing.T) {
			s := routeSubject(t, name)
			t.Cleanup(s.finish)
			for i := 0; i < 200; i++ {
				s.structured()
			}
			s.finish()
			f, err := os.Open(s.path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			scanner := bufio.NewScanner(f)
			n := 0
			for scanner.Scan() {
				var v map[string]any
				if err := json.Unmarshal(scanner.Bytes(), &v); err != nil {
					t.Fatal(err)
				}
				if v["request_id"] != "req-123" || v["user_id"] != float64(42) || v["method"] != "GET" || v["status"] != float64(200) || v["cached"] != true || v["message"] != "request complete" || v["level"] != "info" {
					t.Fatal("lost routed fields", v)
				}
				if _, ok := v["directory"]; ok {
					t.Fatal("routing field leaked", v)
				}
				n++
			}
			if scanner.Err() != nil || n != 200 {
				t.Fatal("incomplete routed output", n, scanner.Err())
			}
		})
	}
}
