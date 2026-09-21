# mlog：保持 Zap 与兼容性的性能优化

日期：2026-09-21。基线：`PinableAgents/mlog` 主分支 `1cf58794f1fd91893cb33ed6846adb7d15f4f94a`，版本 v0.0.21，已包含之前的安全和覆盖率修复。

## 1. 交付状态

本地实现与验证已完成。GitHub 的 create_tree 写入动作被工具拦截，本轮没有创建分支、提交或 PR，也未写入远端主分支。交付的是可应用的统一 diff，以及测试、基准和诊断证据。最后一次 GitHub 只读核对确认主分支仍为上述基线。

这不是换底层方案：生产 `go.mod`、`go.sum` 完全未变，仍使用 Zap v1.27.1；Zerolog v1.34.0 仍然只存在于独立 benchmarks 模块。没有新增链式日志 API、没有变更公开函数签名、配置字段或默认值；也没有启用采样、静默丢弃、缓冲延迟写入或 unsafe 转换来提高分数。现有业务调用不需要迁移。

## 2. 实际实现

### Caller 派生对象缓存

初始化时准备 AddCallerSkip(1/2) 的 logger，按基础 logger 身份绑定缓存；调用时复用，不再每条 WithOptions。GLOG 返回的原始 *zap.Logger 保持不变。旧生命周期、其他 logger 和非 1/2 的偏移继续走原有派生路径。Close 清理缓存，不能借用新一代 logger。

### 无中间字符串的时间编码

Prefix 为空时，使用 Zap TimeEncoderOfLayout 的追加编码路径。保留原毫秒格式、时区解释和精度；非空 Prefix 保留原来的字符串编码。仍在每次编码时观察配置接收者上的 Prefix，保持 Encoder 创建后按顺序修改 Prefix 的旧行为。任意前缀不是时间格式模板，包含 2006、引号、反斜线、控制符和中文的前缀均有测试。

### 异步状态原子读取

私有队列指针改为 atomic.Pointer；格式化和结构化日志查询队列不再获取全局读锁。结构化方法只获取一次队列，并调整内部 caller 深度以保留用户调用位置。globalMutex 仍管理初始化，acceptMu 仍保护接收与 Close，core 的关闭保护锁没有移除。异步仍在调用线程编码引用字段，Flush/Close 和丢弃策略未改变。

原有 audit_async_test.go 仅将两处私有测试夹具的锁内指针赋值改为原子 Store；原有断言没有删除或放宽。

### 路由字段按需复制

多文件模式只有出现 business/folder/directory 控制字段时才分配过滤切片；普通字段直接只读借用调用切片。出现控制字段时才复制，保留字段顺序、最后一个路由字段优先和空值语义，不修改调用方切片。With 和异步仍在返回前完成字段编码。

### 已验证路由的 sink 缓存

为已经成功构造的路由缓存 WriteSyncer，重复写入不再重复拼路径、校验和查找 writer。仍调用 Zap core 完成编码和写入，不引入自己的 JSON 编码器。首次创建、非法路径、文件系统错误和容量错误继续走原有路径；错误不缓存，缓存规模受原 writer 注册表限额约束，Close 清理缓存并阻止旧 With 句柄重新打开文件。

控制台路由刻意不使用此缓存，保留原有每次解析 os.Stdout 的行为，避免标准输出重定向后仍指向旧目标。

### printf 去掉重复字符串复制

正常 printf 路径直接返回最终格式化字符串，不再复制进另一个 strings.Builder。保留 %s/%d/%v 的旧快速分支、错误类型/数量的 fmt 行为、没有百分号时拼接参数的历史行为，以及 SafetyMode 对 SafeFormat 的选择。

## 3. 兼容性与质量验证

| 检查 | 本轮结果 |
|---|---|
| `go doc -all .` 公开 API 快照与基线逐字节比较 | 一致，diff 为空 |
| 根模块、examples 合并语句覆盖率 | **1,062/1,062，精确 100%** |
| 完整根模块测试 | PASS |
| 完整根模块 `go test -race` | PASS |
| benchmarks 全部输出契约测试与 race | PASS |
| `go vet ./...` | PASS |
| 路径 fuzz | **184,186 次执行，PASS** |
| `go mod verify` | all modules verified |
| Windows/amd64 生产包交叉编译 | PASS；不是 Windows 运行测试 |
| macOS/arm64 生产包交叉编译 | PASS；不是 macOS 运行测试 |

新增 11 个根模块兼容性测试，覆盖 JSON/console 完整字节输出、前缀变化、四个级别同步/异步精确 caller 行号、原始 GLOG 调用、路由过滤和输入不变、限额/失败缓存/Close、标准输出重定向、旧代 logger、32 个并发首次路由创建、旧异步 helper 和 printf 边界。

新增现场字段构造与路由基准及其输出正确性测试。质量工作流改为执行 benchmarks 的全部测试，而不只运行原来的 TestCompletedOutput。原有精确覆盖率门禁、race、vet、fuzz 和安全扫描工作流保留。

覆盖率是语句覆盖，不是所有分支/输入组合/平台的穷尽验证。API 文档快照一致是静态表面检查，不能取代所有业务集成回归。运行测试在 Linux 完成；没有执行本补丁的远端 CI、Go 1.27.1 测试或新一轮 govulncheck，不能把旧 PR 的安全扫描结果当成本补丁结果。没有验证 Windows/macOS 运行行为、断电恢复、多进程共写或长时间轮转压缩。

## 4. 性能测试方法

Go 1.24.13，Linux/amd64，AMD EPYC 9V74 虚拟 CPU 环境，GOMAXPROCS=4。25 个选定用例，基线与优化版交替 AB/BA 运行十轮，各 10 次样本，目标 benchtime=200ms；表内为中位数。每个版本使用相同新增测试代码，只改变 mlog 的生产实现。

相同 JSON 字段、值、消息、级别和毫秒时间格式；相同 lumberjack v0.0.5；关闭 caller、控制台、压缩；预热打开文件，不测轮转。异步禁止丢弃，队列排空与 Close 计入计时。真实文件写入包含 OS 页缓存，不是逐条 fsync。四并发 ns/op 是总时间/操作数，不是单条尾延迟。

“复用字段”只复用 zap.Field 描述，仍逐条编码 JSON；“现场构造”把五个字段和可变参数存储的成本算入。“每条传入固定业务目录”是每条带路由控制字段，但目录值固定、writer 已预热，不是持续创建新目录。With 构建不计入稳态路由测试。

原生 Zap 的对照编码器现在固定在 benchmarks 代码里，使用相同格式的 TimeEncoderOfLayout，不再继承 mlog Encoder 的变化；这个对照在两个版本中完全相同。不要将本轮 Zap 对照直接与旧报告中使用旧 cfg.Encoder 的数值混算。

环境并非专用性能机。即使原生对照实现不变，也出现了数个百分点的漂移；其四并发 Zap 中位数漂移约 7.3%。本报告提供描述性结果和原始范围，不宣称统计置信区间或生产 SLA，尤其不把异步几个百分点的变化视为稳定提升保证。

## 5. mlog 优化前后

| 场景 | 基线 ns/op | 优化 ns/op | 耗时降低 | B/op 变化 | allocs/op 变化 |
|---|---:|---:|---:|---:|---:|
| 普通同步日志 | 1442 | 1167 | 19.1% | 168 → 0 | 3 → 0 |
| 同步五字段：复用字段描述 | 1649 | 1392.5 | 15.6% | 168 → 0 | 3 → 0 |
| 同步五字段：调用时构造 | 1760 | 1582 | 10.1% | 488 → 320 | 4 → 1 |
| 同步 printf | 1669.5 | 1420.5 | 14.9% | 216 → 24 | 5 → 1 |
| 多文件，无业务路由字段 | 1848.5 | 1492 | 19.3% | 488 → 0 | 4 → 0 |
| 每条传入固定业务目录 | 2710.5 | 1715.5 | 36.7% | 800 → 320 | 13 → 1 |
| 已有 With 绑定固定目录 | 2532.5 | 1523 | 39.9% | 592 → 0 | 11 → 0 |
| 四并发同步五字段 | 1905.5 | 1694.5 | 11.1% | 168 → 0 | 3 → 0 |
| 异步五字段，含排空 | 1758.5 | 1671 | 5.0% | 1288 → 1264 | 7 → 6 |

普通同步、复用字段同步和无控制字段的多文件稳态路径达到本次测量的零分配。**现场构造五字段仍有 320 B、1 次分配**，不能把复用字段的结果套在这种常见写法上。编译器诊断记录了对应可变参数存储的堆逃逸。

异步五字段的分配从 1,288 B/7 次降到 1,264 B/6 次，但安全快照仍有成本；无字段异步中位数甚至略有变慢（1,486.5 → 1,491.5 ns/op），不宣称所有场景都更快。

## 6. 与 Zerolog 的剩余差距

| 同一优化阶段的对照 | mlog 同步 | 原生 Zap | Zerolog |
|---|---:|---:|---:|
| 五字段，复用字段描述 ns/op | 1,392.5 | 1,356 | 1,077 |
| 五字段，现场构造 ns/op | 1,582 | 1,507.5 | 1,065.5 |

复用字段路径中，mlog 的耗时比原生 Zap 高约 2.7%，比 Zerolog 高约 29.3%；现场构造时比 Zerolog 高约 48.5%。因此结论是**降低了封装与路由成本，接近同配置原生 Zap，但没有追平 Zerolog**。

独立 CPU 剖析（不混入上表计时）中，syscall.Syscall6 平坦样本约 38.2%，时间格式与 Zap 字符串编码也是主要热点。累计时间存在嵌套，不能把这些累计百分比相加。本轮保留 Zap 编码、现有时间格式和同步写入语义，不靠更换实现或默默批量缓冲来夸大收益。

## 7. 应用与复现

推荐在上述主分支基线上应用补丁，在独立分支评审，不直接改主分支。包内 apply-and-test.sh 会核对基线、拒绝覆盖已修改的跟踪文件，创建新分支并运行检查；它不会提交、推送或合并。

```sh
git apply --check /path/to/mlog-zap-compat-performance.patch
git apply /path/to/mlog-zap-compat-performance.patch
go test -count=1 -timeout=180s -covermode=atomic -coverpkg=./... -coverprofile=coverage.out ./...
python3 scripts/check_coverage.py coverage.out
go test -race -count=1 -timeout=180s ./...
go vet ./...
(cd benchmarks && go test -race -count=1 ./...)
```

原始采样在 results/base-bench.txt、results/opt-bench.txt；摘要在 benchmark-summary.json；CPU 剖析、编译器逃逸诊断、覆盖 profile、API 快照和测试输出一并交付。reproduce-benchmarks.sh 使用临时源码副本和同一比较模块，可复现 AB/BA 基准而不改变工作区。

## 8. 全部采样范围（每侧 n=10）

| Benchmark | 基线中位数 ns/op | 优化中位数 ns/op | 基线 min–max | 优化 min–max |
|---|---:|---:|---:|---:|
| `BenchmarkFile/plain/mlog-sync-4` | 1442 | 1167 | 1350–6515 | 1133–1229 |
| `BenchmarkFile/plain/mlog-async-drained-4` | 1486.5 | 1491.5 | 1285–4201 | 1262–1758 |
| `BenchmarkFile/plain/zap-4` | 1116.5 | 1120 | 1086–2662 | 1045–1273 |
| `BenchmarkFile/plain/zerolog-4` | 935.2 | 949.4 | 891.8–2924 | 892–1004 |
| `BenchmarkFile/five-fields/mlog-sync-4` | 1649 | 1392.5 | 1586–4557 | 1363–1487 |
| `BenchmarkFile/five-fields/mlog-async-drained-4` | 1758.5 | 1671 | 1650–3694 | 1519–1799 |
| `BenchmarkFile/five-fields/zap-4` | 1417 | 1356 | 1311–3802 | 1310–1527 |
| `BenchmarkFile/five-fields/zerolog-4` | 1086.5 | 1077 | 1045–2243 | 1001–1138 |
| `BenchmarkFile/printf/mlog-sync-4` | 1669.5 | 1420.5 | 1540–4602 | 1314–1551 |
| `BenchmarkFile/printf/mlog-async-drained-4` | 1600 | 1465 | 1402–3873 | 1246–1714 |
| `BenchmarkFile/printf/zap-4` | 1340 | 1338 | 1274–2928 | 1267–1587 |
| `BenchmarkFile/printf/zerolog-4` | 1137 | 1139.5 | 1096–5385 | 1090–1473 |
| `BenchmarkFile/disabled/mlog-sync-4` | 1.8365 | 1.7675 | 1.793–5.689 | 1.684–2.011 |
| `BenchmarkFile/disabled/mlog-async-drained-4` | 1.808 | 1.725 | 1.775–11.29 | 1.673–1.922 |
| `BenchmarkFile/disabled/zap-4` | 5.948 | 5.9645 | 5.668–8.795 | 5.639–6.719 |
| `BenchmarkFile/disabled/zerolog-4` | 5.744 | 5.965 | 5.454–6.588 | 5.709–6.444 |
| `BenchmarkInlineFieldConstruction/mlog-sync-4` | 1760 | 1582 | 1715–1918 | 1549–2036 |
| `BenchmarkInlineFieldConstruction/zap-4` | 1541 | 1507.5 | 1494–2066 | 1465–1626 |
| `BenchmarkInlineFieldConstruction/zerolog-4` | 1066.5 | 1065.5 | 1025–1155 | 1023–1257 |
| `BenchmarkRouting/multi-no-route-4` | 1848.5 | 1492 | 1761–2148 | 1422–1632 |
| `BenchmarkRouting/route-dynamic-4` | 2710.5 | 1715.5 | 2539–3144 | 1678–1803 |
| `BenchmarkRouting/route-bound-4` | 2532.5 | 1523 | 2469–2788 | 1453–1832 |
| `BenchmarkFileParallel/mlog-sync-4` | 1905.5 | 1694.5 | 1753–2125 | 1578–1838 |
| `BenchmarkFileParallel/zap-4` | 1600 | 1717.5 | 1414–1763 | 1430–1914 |
| `BenchmarkFileParallel/zerolog-4` | 1248.5 | 1222 | 1084–1310 | 1141–3437 |

## 参考

- 已读取的 GitHub 主分支、树和原始实现：PinableAgents/mlog，提交 1cf58794f1fd91893cb33ed6846adb7d15f4f94a。
- Zap TimeEncoderOfLayout 官方 API：https://pkg.go.dev/go.uber.org/zap/zapcore#TimeEncoderOfLayout
- Go 官方诊断工具：https://go.dev/doc/diagnostics
- Go benchstat 方法说明：https://pkg.go.dev/golang.org/x/perf/cmd/benchstat 。本报告没有假称运行了 benchstat，使用的是十轮原始样本中位数及观察范围。
