package mlog

import (
	"sync"
	"testing"
	"time"
)

func TestConcurrentMapLogging(t *testing.T) {
	config := ZapConfig{
		Level:           "debug",
		Format:          "console",
		Director:        "./test_logs",
		ShowLine:        true,
		LogInConsole:    true,
		EnableAsync:     true,
		AsyncBufferSize: 10000,
		AsyncDropOnFull: false,
	}

	InitialZap("test_service", 1001, "debug", &config)
	defer Close()

	sharedMap := make(map[string]int)
	var mu sync.Mutex

	var wg sync.WaitGroup
	numWriters := 10
	numReaders := 10
	duration := 2 * time.Second

	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			start := time.Now()
			counter := 0
			for time.Since(start) < duration {
				mu.Lock()
				sharedMap[string(rune('A'+id))] = counter
				mu.Unlock()
				counter++
				time.Sleep(time.Microsecond * 100)
			}
		}(i)
	}

	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			start := time.Now()
			for time.Since(start) < duration {
				localMap := make(map[string]int)
				mu.Lock()
				for k, v := range sharedMap {
					localMap[k] = v
				}
				mu.Unlock()

				Info("测试并发日志记录 goroutine=%d map=%v", id, localMap)
				time.Sleep(time.Millisecond * 10)
			}
		}(i)
	}

	wg.Wait()

	time.Sleep(time.Second)

	t.Log("并发测试完成，没有发生 panic")
}

func TestConcurrentMapLoggingWithLockProtection(t *testing.T) {
	config := ZapConfig{
		Level:           "info",
		Format:          "console",
		Director:        "./test_logs",
		ShowLine:        true,
		LogInConsole:    false,
		EnableAsync:     true,
		AsyncBufferSize: 50000,
		AsyncDropOnFull: true,
	}

	InitialZap("test_service", 1002, "info", &config)
	defer Close()

	sharedMap := make(map[string]int)
	var mapMu sync.RWMutex

	var wg sync.WaitGroup
	duration := 1 * time.Second

	wg.Add(1)
	go func() {
		defer wg.Done()
		start := time.Now()
		counter := 0
		for time.Since(start) < duration {
			mapMu.Lock()
			sharedMap["key"] = counter
			mapMu.Unlock()
			counter++
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		start := time.Now()
		for time.Since(start) < duration {
			mapMu.RLock()
			mapCopy := make(map[string]int, len(sharedMap))
			for k, v := range sharedMap {
				mapCopy[k] = v
			}
			mapMu.RUnlock()

			Info("共享 map 状态: %v", mapCopy)
		}
	}()

	wg.Wait()
	time.Sleep(time.Second)

	t.Log("带锁保护的并发测试完成")
}

func TestAsyncLoggingPerformance(t *testing.T) {
	config := ZapConfig{
		Level:           "info",
		Format:          "console",
		Director:        "./test_logs",
		ShowLine:        true,
		LogInConsole:    false,
		EnableAsync:     true,
		AsyncBufferSize: 100000,
		AsyncDropOnFull: false,
	}

	InitialZap("test_service", 1003, "info", &config)
	defer Close()

	numLogs := 10000
	start := time.Now()

	for i := 0; i < numLogs; i++ {
		testMap := map[string]interface{}{
			"index":     i,
			"timestamp": time.Now().Unix(),
			"data":      "test data",
		}
		Info("性能测试日志 index=%d data=%v", i, testMap)
	}

	elapsed := time.Since(start)
	t.Logf("记录 %d 条日志耗时: %v (平均 %.2f μs/条)", numLogs, elapsed, float64(elapsed.Microseconds())/float64(numLogs))

	time.Sleep(2 * time.Second)
}

func TestComplexDataStructures(t *testing.T) {
	config := ZapConfig{
		Level:           "debug",
		Format:          "console",
		Director:        "./test_logs",
		ShowLine:        true,
		LogInConsole:    true,
		EnableAsync:     true,
		AsyncBufferSize: 10000,
		AsyncDropOnFull: false,
	}

	InitialZap("test_service", 1004, "debug", &config)
	defer Close()

	complexData := map[string]interface{}{
		"string": "test",
		"int":    123,
		"float":  45.67,
		"bool":   true,
		"slice":  []int{1, 2, 3, 4, 5},
		"nested_map": map[string]string{
			"key1": "value1",
			"key2": "value2",
		},
		"nil_value": nil,
	}

	var wg sync.WaitGroup
	numGoroutines := 5

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				Info("复杂数据结构测试 goroutine=%d iteration=%d data=%v", id, j, complexData)
				Debug("调试信息 goroutine=%d iteration=%d", id, j)
				Warn("警告信息 goroutine=%d data=%v", id, complexData)
			}
		}(i)
	}

	wg.Wait()
	time.Sleep(time.Second)

	t.Log("复杂数据结构测试完成")
}

// TestConcurrentMapLoggingWithoutLock verifies map TYPE-ONLY summaries.
// Neither map entries nor map length may be read in this mode. It does not
// authorize concurrent mutation of structs, slices, or unsafe fmt arguments.
func TestConcurrentMapLoggingWithoutLock(t *testing.T) {

	config := ZapConfig{
		Level:           "info",
		Format:          "console",
		Director:        "./test_logs",
		ShowLine:        false,
		LogInConsole:    false,
		EnableAsync:     true,
		AsyncBufferSize: 10000,
		AsyncDropOnFull: true,
	}

	InitialZap("test_service", 1005, "info", &config)
	defer Close()

	sharedMap := make(map[string]int)

	var wg sync.WaitGroup
	duration := 500 * time.Millisecond

	wg.Add(1)
	go func() {
		defer wg.Done()
		start := time.Now()
		counter := 0
		for time.Since(start) < duration {
			for i := 0; i < 10; i++ {
				sharedMap[string(rune('A'+i))] = counter
			}
			counter++
			if counter%10 == 0 {
				delete(sharedMap, "A")
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		start := time.Now()
		for time.Since(start) < duration {
			Info("危险测试: map=%v", sharedMap)
			time.Sleep(time.Microsecond * 100)
		}
	}()

	wg.Wait()
	time.Sleep(time.Second)

	t.Log("危险测试完成（如果能看到这条消息，说明修复有效）")
}
