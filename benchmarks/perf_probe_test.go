package benchmarks

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"
)

// This probe includes construction of the five typed fields inside the measured
// call, unlike the prebuilt fields slice used in the original benchmark.
func BenchmarkInlineFieldConstruction(b *testing.B) {
	for _, name := range []string{"mlog-sync", "zap", "zerolog"} {
		b.Run(name, func(b *testing.B) {
			s := newSubject(b, name)
			s.plain()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s.inlineStructured()
			}
			s.finish()
			b.StopTimer()
		})
	}
}

func TestInlineCompletedOutput(t *testing.T) {
	for _, name := range []string{"mlog-sync", "zap", "zerolog"} {
		t.Run(name, func(t *testing.T) {
			s := newSubject(t, name)
			var wg sync.WaitGroup
			for i := 0; i < 4; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for j := 0; j < 50; j++ {
						s.inlineStructured()
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
