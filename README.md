# mlog

基于Zap的Go应用日志封装：JSON/console、结构化字段、动态级别、服务/级别/业务目录、滚动文件与可选异步队列。

> 本次质量修复位于草稿PR #1，尚未自动合并。完整审计、迁移注意事项与实测见 [审计报告](docs/mlog-audit-20260921.md)。不要把100%语句覆盖率理解成100%安全。

## 使用

模块最低Go1.24；生产构建应使用仍受Go官方安全支持的工具链。评审分支：`audit/mlog-quality-20260921`。

```go
package main

import (
    "fmt"
    "os"

    "github.com/PinableAgents/mlog"
    "go.uber.org/zap"
)

func run() error {
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
    mlog.Info("port=%d", 8080)
    mlog.InfoW("request complete", zap.String("request_id", "req-123"), zap.Int("status", 200))
    return mlog.Flush()
}

func main() {
    if err := run(); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}
```

文件为`logs/1001/orders/app.log`。`Info/Debug/Warn/Error`是printf接口；`InfoW/...`接收Zap字段。`UpdateLevel("warn")`动态更新级别。
`SingleFile=false`时按级别输出；`business`、`folder`、`directory`字符串字段成为路由控制字段，且从payload移除。单文件模式下三者只是元数据。`GLOG().With(...)`是同步Zap接口，不经过异步队列。

## 并发、安全与可靠性

共享map、切片、结构体在读取/复制/日志调用期间必须由调用方同步。异步InfoW在返回前编码字段，返回后可复用数据，但不能与调用进行中的修改竞争。
SafeFormat对map仅输出类型，不读取内容/长度；不是通用深拷贝或自动锁。字段脱敏、输入大小限制、磁盘配额、防篡改并非本库能力。

异步设置`EnableAsync=true`、`AsyncBufferSize=4096`。`AsyncDropOnFull=false`缓冲满阻塞；true允许丢弃。`GetAsyncStats`提供Accepted/Processed/Dropped/Rejected；Processed是处理次数，不是成功持久化次数。退出前先停止生产者，再Flush/Close。Flush是队列屏障；当前rolling writer不提供fsync持久性承诺。

根目录必须由应用控制，新增目录0700，不会更改既有目录权限。目录遍历与现有符号链接会被拒绝，但不能抵御可写目录中的敌对本地进程TOCTOU。MaxRouteWriters是每core上限，多文件有七个core。不要把用户输入直接用作路由或字段名。

`InitialZapChecked`返回配置/初始化错误，原`InitialZap`仍panic。`LoadConfig`严格拒绝未知YAML字段。`EnableSplit`为历史未接入开关，不能依赖它禁用轮转。初始化前/关闭后便捷日志为no-op，GLOG可能为nil。更多兼容变更见报告。

## 质量和性能

实测合并覆盖1,045/1,045语句，完整race/vet通过。严格门禁在`.github/workflows/quality-audit.yml`；漏洞扫描以最新security job为准。

相同五字段JSON写文件，本机Go1.24.13、Xeon8573C、5轮中位数：Zerolog1196ns、Zap1662ns、mlog同步1806ns、mlog异步含排空2283ns、slog2779ns、Logrus5628ns。
异步不保证更快；公平条件、分配、并发结果和原始数据见审计报告，不把这些数字当作生产SLA。

```sh
go test -count=1 -covermode=atomic -coverpkg=./... -coverprofile=coverage.out ./...
python3 scripts/check_coverage.py coverage.out
go test -race -count=1 ./...
go vet ./...
cd benchmarks
go test -race -run '^TestCompletedOutput$' ./...
go test -run '^$' -bench . -benchmem -benchtime=200ms -count=5 -cpu=4 ./...
```
