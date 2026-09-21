# mlog 使用、安全与性能审计

日期：2026-09-21。对象：`PinableAgents/mlog`，原版提交 `a92728a6ccd69370e78379a48c32b50e43ea6fb4`。
修复位于草稿 PR #1：<https://github.com/PinableAgents/mlog/pull/1>，不自动合并，不代表主分支已修复。

## 1. 结论与验证范围

mlog 是 Zap 的应用层封装，价值是统一初始化、动态级别、按服务/级别/业务目录分流、滚动文件和异步队列，不是独立编码器。
原版不应直接作为“并发绝对安全”或“已达到100%覆盖率”的库使用。本次修复了已复现问题，并建立严格质量门禁。

| 项目 | 原版基线 | 修复版本地实测 |
|---|---|---|
| 全根模块合并语句覆盖率，包含 examples | 33.9% | **1,045/1,045，精确100%** |
| 完整 `go test -race ./...` | **FAIL** | PASS |
| 单文件结构化字段/With目录/目录越界三个回归 | 三项FAIL | 三项PASS |
| `go vet ./...` | 见原CI日志 | PASS |
| 路径组件fuzz | 无新增审计用例 | 5秒目标，112,376次执行，PASS |
| 基准语义校验，六实现，每实现200条及全部字段 | 原版字段不等价 | PASS，含race |

原版CI运行：<https://github.com/PinableAgents/mlog/actions/runs/35560701642>。
**原诊断工作流使用 continue-on-error，工作流绿色不代表race通过。** 现在改为严格退出码门禁。
`go test -coverpkg=./...` 每个测试二进制的94.4%和32.7%分别相对整个根模块计数，合并profile后才是100%。
门禁按文件和语句块位置合并重复计数，拒绝空profile和任何未执行语句，不依赖四舍五入到100.0%的文本。
覆盖率不包含第三方依赖和独立 benchmarks 模块；后者是测试工具，不是mlog生产实现。没有用生产文件排除、覆盖率豁免或新增Skip凑指标。

100%是**语句覆盖**，不是分支/路径/输入空间100%，也不是无漏洞证明。故障注入使用测试writer/core；正常输出与回归使用真实临时文件。Windows/macOS运行时行为、本地敌对进程、断电恢复、长时间轮转压缩和多进程共写不在本次实测范围。

## 2. 已修复的问题

| 问题 | 影响 | 修复与回归 |
|---|---|---|
| 单文件Write传递空字段 | JSON字段静默丢失，审计信息不完整 | 保留全部结构化字段；精确验证值 |
| With返回底层Core | With中的business/directory分流失效 | boundCore保留路由、编码上下文和生命周期 |
| 路由/服务名/文件名缺少校验 | `../`可把日志写出根目录 | 可移植组件规则，拒绝越界/盘符/设备名/控制符及现有子目录符号链接 |
| 特殊writer重复创建且数量无界 | 竞争、文件句柄/磁盘资源放大 | 创建加锁，MaxRouteWriters默认128/每core |
| 异步保留调用方字段引用 | 调用返回后map/切片被修改，字段变化或竞态 | 生产者线程Core.With立即编码字段，入队只持有已编码上下文 |
| Close/重初始化与生产者竞争 | 重复关闭panic、旧队列写入新logger、关闭后重开文件 | 队列接收锁、幂等Close、generation绑定logger、关闭core拒绝写入 |
| Flush语义不明确 | 无法判断队列是否排空 | FIFO屏障，Flush不关闭异步；Close排空后关闭 |
| 静默队列丢弃 | 无法监测超载 | Accepted/Processed/Dropped/Rejected原子计数 |
| 安全模式和配置/cache数据竞争 | race报告 | 原子模式、配置副本、锁覆盖cache查找/计算/插入 |
| map安全格式化读取Len、夸大安全承诺 | 共享map仍有竞态风险 | map只输出类型，不读长度/条目；递归深度32、访问预算1024 |
| 宽泛忽略Sync错误 | 真实文件错误被控制台错误掩盖 | 仅忽略明确stdout/stderr不支持Sync的类型化错误，保留混合文件错误 |
| 示例用sleep等待、单文件目录说明错误 | 使用方式误导 | Close完成屏障、修正目录与元数据说明 |

原测试对共享结构体`*sharedData`的参数求值本身没有锁，日志库无法修复调用表达式的数据竞争，测试现由调用方RWMutex保护。map类型摘要测试则取消原先的Skip并实际执行。

## 3. 推荐使用

建议先用同步JSON结构化日志，合并并验证草稿分支后再升级业务依赖。新增API见根README完整可运行代码。

```go
cfg := &mlog.ZapConfig{
    Level: "info", Format: "json", Director: "./logs",
    SingleFile: true, SingleFileName: "app.log",
    ShowLine: false, LogInConsole: false,
    MaxSize: 100, MaxBackups: 5, RetentionDay: 7,
    EnableCompress: true, MaxRouteWriters: 32,
}
if err := mlog.InitialZapChecked("orders", 1001, "info", cfg); err != nil {
    return err
}
defer mlog.Close()
mlog.InfoW("request complete", zap.String("request_id", "req-123"), zap.Int("status", 200))
mlog.Info("port=%d", 8080)
```

输出：`./logs/1001/orders/app.log`。ID=0或服务名为空时省略对应层级。
`Info/Debug/Warn/Error`是printf式接口；`InfoW/...`接收Zap字段。优先明确类型字段，避免把整个请求、配置、token或大对象用`zap.Any`倾倒进日志。

单文件模式下，business/folder/directory是保留在输出中的元数据，不创建业务目录。
多文件模式下，这三个字段是受保留的路由控制字段，必须是字符串，写入子目录后从payload移除；日志每级别一个文件，不重复写到其他严重级别文件。

`GLOG().With(...)`保留路由，但`GLOG()`是原始Zap指针，直接调用**绕过mlog异步队列**；初始化前/关闭后可能为nil。
`Lock/Critical/Disaster/AssertString/GrpcAssert`也是兼容同步辅助路径，不应当作通用并发快照工具。
`InitialZap`保留错误时panic行为，推荐`InitialZapChecked`显式处理错误。配置校验失败保留原logger；文件系统初始化失败可能发生在旧logger已经关闭之后，并非事务式热切换。
`LoadConfig`严格拒绝未知YAML字段，只解析并返回配置，不隐式修改运行中的logger。
`UpdateLevel("warn")`动态修改过滤级别。非法更新保留当前级别（兼容stderr文本仍含“默认info”，不要把该文本当作状态）。
`EnableSplit`是历史保留但当前未接入的开关；不要依赖它禁用轮转，实际写入器由MaxSize等参数控制。

### 异步仅在需要时开启

设置EnableAsync=true、AsyncBufferSize=4096。AsyncDropOnFull=false表示缓冲满后阻塞生产者；true表示允许丢弃并增加Dropped。
读取`GetAsyncStats()`监测队列。先停止业务生产者，再Flush、读取最后stats、Close。Close后全局stats返回零，新一代logger重新计数。
`Processed`只是worker处理/尝试写入次数，不代表写入成功或已持久化。调用`InfoW`没有error返回，磁盘错误须监控stderr；强审计链路需要额外的可靠投递/持久化设计。

## 4. 安全边界与迁移注意事项

**所有日志库都不能替调用方锁住正在变化的数据。** 异步字段在调用线程编码意味着调用返回后可以复用数据，但调用进行期间仍不可无锁并发修改；对map/切片/struct复制时也必须先加锁。
SafeFormat的map是类型摘要，不再展示内容和长度。大切片摘要与循环限制意味着它不是完整序列化器；字节切片、字符串及用户Error/Stringer方法仍可能很大或阻塞。应用应限制输入长度和对象大小。

JSON编码回归验证了换行会被转义成单条记录，但没有提供自动凭据/PII脱敏、加密、防篡改或重复字段冲突策略。字段名必须受控；请求内容不要覆盖level/time/message等可信字段。生产建议关闭彩色编码和不必要的绝对源码路径。

新目录权限0700不会收紧既有目录权限。根目录及父目录必须由服务控制，不向其他用户开放写权限；现有子目录/日志符号链接检查只是防御加固，不能消除本地敌对进程在检查与打开之间替换路径的TOCTOU。多进程不得假定可以安全共用同一滚动日志文件。

MaxRouteWriters是每core上限；多文件模式有七个core，默认最多可扩展为896个路由writer加主writer，并不是全局128。用小而有限的业务路由集合，不要按请求ID/用户输入无限分目录。磁盘配额、保留天数、轮转和容量告警必须由部署层配置。

**Flush/Close不是fsync持久化承诺。** 当前lumberjack v0.0.5没有提供文件Sync/fsync，zapcore.AddSync包装的Sync不能保证断电后数据仍在。需要强持久化的审计记录应另设支持fsync/可靠确认的存储路径。

Go 1.24是兼容最低版本，不应被当作2026年的生产安全版本。官方当前稳定版为1.27.1，安全策略只维护最近两个大版本；本次CI另设Go1.27.1的race和govulncheck门禁。govulncheck的JSON输出模式即使有发现也可能退出0，因此门禁使用普通文本模式和pipefail。
安全扫描结果以本PR最新security job及其artifact为准；静态分析无发现也不等于不存在漏洞，且扫描使用执行时工具链和漏洞库。

## 5. 性能测试方法

以下为本次本地**修复版**实测，不是库README里的宣传数据。Go1.24.13、linux/amd64、Intel Xeon Platinum8573C、GOMAXPROCS=4。每用例200ms目标时长、重复五次取中位数；min–max仅是五次观察范围，不是统计置信区间。
Zap1.27.1、Zerolog1.34.0、Logrus1.9.4；slog随Go1.24.13；全部真实写文件对比共用lumberjack0.0.5。
统一JSON、info阈值、同五字段类型和值、毫秒时间格式、关闭caller/控制台/压缩。slog为匹配相同字段名/级别/时间格式使用ReplaceAttr，成本计入，不能据此断言slog所有配置都更慢。
写入最大1024MB、每轮独立临时目录并预热文件打开，不测轮转。异步buffer4096、禁止丢弃、**排空与Close计入计时**；其他实现Close同样计时。
真实文件测试包含OS页缓存，不是逐条fsync落盘延迟；未测p95/p99、远程sink、慢盘、突发负载或长时间饱和。并发ns/op是总墙钟时间除操作数，不是单请求尾延迟。
原版单文件丢结构化字段，不能与正确保存字段的库当作等价高性能结果比较。旧`TestAsyncLoggingPerformance`只测发送循环，不用作本报告吞吐证据。

### 五字段JSON写文件

| 实现 | 单线程五字段 ns/op | min–max | B/op | allocs/op | 四并发五字段 ns/op |
|---|---:|---:|---:|---:|---:|
| zerolog | 1196 | 1147–1226 | 0 | 0 | 1421 |
| zap | 1662 | 1603–1690 | 24 | 1 | 1869 |
| mlog-sync | 1806 | 1758–1849 | 168 | 3 | 2120 |
| mlog-async-drained | 2283 | 2235–2335 | 1288 | 7 | 2416 |
| slog | 2779 | 2503–3167 | 48 | 4 | 2811 |
| logrus | 5628 | 5378–6501 | 2281 | 35 | 6841 |

在此配置下，mlog同步比Zap耗时高约8.7%，比Zerolog高约51%；吞吐按时间倒数计算，约为Logrus的3.12倍、slog的1.54倍。这是本机短基准结果，不是所有环境的性能承诺。
异步五字段比同步慢约26.4%，每条分配1288B/7次而非168B/3次，来源包括生产者侧编码快照和队列传递。四并发下异步仍慢，不能宣称“开启异步必定提速”。

### 其他写文件工作负载

| 实现 | 无字段 ns/op | printf ns/op | 被禁用日志 ns/op |
|---|---:|---:|---:|
| zerolog | 1048 | 1232 | 6.92 |
| zap | 1421 | 1527 | 6.313 |
| mlog-sync | 1635 | 1737 | 2.336 |
| mlog-async-drained | 1761 | 1861 | 2.376 |
| slog | 2100 | 2226 | 6.09 |
| logrus | 2916 | 3060 | 3.466 |

禁用路径全部零分配。slog测试显式检查Enabled后才构造printf消息，因为slog没有原生printf接口；该表不能比较不等价的提前格式化调用。
另有独立NativeDiscard微基准只测四个原生库，取消时间字段，不包含mlog；把mlog的实际文件路由Core替换成io.Discard会隐去封装成本，所以不能把该表与上面的文件结果混排。

## 6. 选型建议

继续使用mlog：需要统一文件组织、服务路由和现有兼容API，并愿意维护这些生命周期规则。建议默认同步+InfoW。
直接Zap：需要更少封装成本、明确的Core/字段控制，且可以在应用统一配置rolling sink。
Zerolog：偏好链式JSON API并追求较低分配，本次同条件最快，但迁移成本与既有Zap字段生态需要评估。
slog：优先Go标准库API和Handler可替换性，不把本次特定ReplaceAttr配置的性能外推到所有Handler。
Logrus：现有生态/Hook兼容优先时可保留；官方处于维护模式，不作为新高吞吐链路的首选。

## 7. 复现

```sh
go test -count=1 -timeout=180s -covermode=atomic -coverpkg=./... -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
python3 scripts/check_coverage.py coverage.out
go test -race -count=1 -timeout=180s ./...
go vet ./...
go test -run '^$' -fuzz '^FuzzAuditRouteComponents$' -fuzztime=5s ./
cd benchmarks
go test -race -count=1 -run '^TestCompletedOutput$' ./...
go test -run '^$' -bench . -benchmem -benchtime=200ms -count=5 -cpu=4 ./...
```

公开基准原文、环境和精确覆盖率结果在`docs/audit-results/`，完整coverage.out与HTML随交付包提供，CI每次重新生成artifact。版本、测试依赖和第三方日志库锁定在go.mod/go.sum；benchmarks是隔离模块，不给mlog使用者增加这些日志库依赖。

## 官方资料

- Go security policy: <https://go.dev/doc/security/policy>
- Go当前发行版本: <https://go.dev/dl/?mode=json>
- Race detector与限制: <https://go.dev/doc/articles/race_detector>
- govulncheck行为与退出码: <https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck>
- Zap: <https://pkg.go.dev/go.uber.org/zap>
- slog: <https://pkg.go.dev/log/slog>
- Zerolog: <https://github.com/rs/zerolog>
- Logrus: <https://github.com/sirupsen/logrus>
