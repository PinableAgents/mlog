package mlog

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestExtremeConcurrentMapAccess(t *testing.T) {
	config := ZapConfig{
		Level:           "info",
		Format:          "console",
		Director:        "./test_logs",
		ShowLine:        false,
		LogInConsole:    false,
		EnableAsync:     true,
		AsyncBufferSize: 100000,
		AsyncDropOnFull: true,
	}

	InitialZap("test_extreme", 9999, "info", &config)
	defer Close()

	maps := make([]map[string]interface{}, 10)
	for i := range maps {
		maps[i] = make(map[string]interface{})
	}

	var wg sync.WaitGroup
	stopCh := make(chan struct{})

	for i := 0; i < len(maps); i++ {
		wg.Add(1)
		go func(mapIndex int) {
			defer wg.Done()
			m := maps[mapIndex]
			counter := 0
			for {
				select {
				case <-stopCh:
					return
				default:
					for k := 0; k < 20; k++ {
						key := string(rune('A' + k))
						m[key] = counter + k
					}
					for k := 0; k < 10; k++ {
						delete(m, string(rune('A'+k)))
					}
					m["nested"] = map[string]int{
						"a": counter,
						"b": counter * 2,
						"c": counter * 3,
					}
					m["slice"] = []int{counter, counter + 1, counter + 2, counter + 3, counter + 4}
					m["complex"] = map[string]interface{}{
						"level1": map[string]int{"x": counter, "y": counter * 2},
						"level2": []string{"a", "b", "c"},
					}
					counter++
				}
			}
		}(i)
	}

	numLoggers := runtime.NumCPU() * 4
	for i := 0; i < numLoggers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stopCh:
					return
				default:
					for j, m := range maps {
						Info("极限测试 logger=%d map_id=%d data=%v", id, j, m)
						if j < len(maps)-1 {
							Info("多map测试 m1=%v m2=%v", m, maps[j+1])
						}
					}
				}
			}
		}(i)
	}

	duration := 5 * time.Second
	t.Logf("运行极限并发测试 %v (maps=%d, loggers=%d)", duration, len(maps), numLoggers)

	time.Sleep(duration)
	close(stopCh)
	wg.Wait()

	time.Sleep(2 * time.Second)

	t.Log("✅ 极限并发测试通过！系统在极端压力下保持稳定")
}

func TestConcurrentMapWithDifferentTypes(t *testing.T) {
	config := ZapConfig{
		Level:           "debug",
		Format:          "console",
		Director:        "./test_logs",
		ShowLine:        true,
		LogInConsole:    false,
		EnableAsync:     true,
		AsyncBufferSize: 50000,
		AsyncDropOnFull: false,
	}

	InitialZap("test_types", 8888, "debug", &config)
	defer Close()

	type ComplexStruct struct {
		Name    string
		Value   int
		SubMap  map[string]interface{}
		Slice   []int
		Channel chan int
	}

	sharedData := &ComplexStruct{
		Name:    "test",
		Value:   0,
		SubMap:  make(map[string]interface{}),
		Slice:   make([]int, 0),
		Channel: make(chan int, 10),
	}

	// Synchronize the caller-owned struct and slice while formatting. A logging
	// function cannot protect the argument expression *sharedData at its call site.
	var dataMu sync.RWMutex
	var wg sync.WaitGroup
	duration := 2 * time.Second
	start := time.Now()

	wg.Add(1)
	go func() {
		defer wg.Done()
		counter := 0
		for time.Since(start) < duration {
			dataMu.Lock()
			sharedData.Value = counter
			sharedData.Name = "test-" + string(rune('A'+counter%26))

			for i := 0; i < 10; i++ {
				sharedData.SubMap[string(rune('a'+i))] = counter + i
			}

			if counter%10 == 0 {
				sharedData.Slice = make([]int, counter%20)
				for i := range sharedData.Slice {
					sharedData.Slice[i] = i
				}
			}

			dataMu.Unlock()
			counter++
		}
	}()

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for time.Since(start) < duration {
				dataMu.RLock()
				Debug("结构体数据 id=%d data=%+v", id, sharedData)
				Info("嵌套map id=%d submap=%v", id, sharedData.SubMap)
				Warn("指针数据 id=%d ptr=%p value=%v", id, sharedData, *sharedData)
				dataMu.RUnlock()

				time.Sleep(time.Millisecond)
			}
		}(i)
	}

	wg.Wait()
	time.Sleep(time.Second)

	t.Log("✅ 不同类型的并发测试通过")
}

func BenchmarkSafeFormatterUnderPressure(b *testing.B) {
	sharedMap := make(map[string]interface{})

	stopCh := make(chan struct{})
	go func() {
		counter := 0
		for {
			select {
			case <-stopCh:
				return
			default:
				for i := 0; i < 100; i++ {
					sharedMap[string(rune('A'+i%26))] = counter
				}
				counter++
			}
		}
	}()
	defer close(stopCh)

	time.Sleep(10 * time.Millisecond)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = SafeFormat("压力测试 map=%v", sharedMap)
		}
	})
}
