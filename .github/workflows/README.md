# mlog 自动发布与严格质量门禁

自动发布入口为 `release.yml`。它只接受 `main` / `master` 的 push，保留仅文档修改的忽略规则、`[skip release]` / `[no release]` 标记和版本提交循环保护。

## 依赖链

```text
preflight（只读，检查是否需要发布）
  └─ audit（只读，调用同一提交的 quality-audit.yml）
       ├─ quality：格式、脚本测试、依赖校验、精确100%覆盖、race、vet、fuzz、基准正确性与基准运行
       ├─ security：工作流语法、受支持Go的race、govulncheck
       └─ gate：两个结果均success，且两个audited_sha均等于触发SHA
            └─ release（唯一contents:write作业）
                 ├─ 检查主分支没有前进；只在本地更新版本并创建候选提交
                 ├─ 候选提交完整重跑quality + security
                 ├─ 保存候选验证证据；检查SHA和工作区、再次检查远端分支
                 ├─ 原子推送候选提交和标签（无强推）
                 └─ 使用已存在的标签创建GitHub Release
```

`quality-audit.yml` 同时提供 PR、只读手动触发和 `workflow_call`。无需独立添加 push 检查：Auto Release 本身调用它，并通过 `needs` 与明确的成功条件阻断发布，不是与检查并行启动。

检查失败、取消、跳过、缺少结果、引用其他提交的成功结果、工具安装失败或漏洞库访问失败都不能放行。`always()` 仅用于汇总和保存证据，不用于发布。不存在 `continue-on-error` 或跳过质量检查的输入。

## 为什么检查两次

第一次检查实际触发提交；原流程随后会修改 `version.go`。为防止“测A发B”，第二次针对本地版本候选提交完整执行相同脚本，成功后才允许将该候选提交和标签推送到远端。发布流程不会运行自动格式化、自动修复依赖或修改其他生产代码。

门禁脚本分别为 `scripts/quality_gate.sh` 和 `scripts/security_gate.sh`。根模块Go兼容基线保持1.24，检查使用Go1.24.13；安全检查使用Go1.27.1和govulncheck1.8.0。更新工具链时同时调整质量工作流、发布候选工作流和手动入口的固定版本。

## 版本与兼容

保留原有版本策略：`feat!` / `feature!` / `BREAKING CHANGE` 为major，普通feat/feature为minor，其余为patch。自动递增只使用正式的 `vX.Y.Z` 标签；首次分别为v1.0.0 / v0.1.0 / v0.0.1。不改变mlog的公开API、配置、Zap依赖或运行时行为。

## 手动发布

`./release.sh init "修复说明"`、`minor`、`major`、显式版本号等原入口保留。手动脚本在版本变更提交之后也调用两套完整门禁；确认发布前后验证候选SHA和工作区，推送分支及标签时使用 `git push --atomic`，推送失败会返回非零，不会再报告成功。

此脚本需要Bash、Git、Python3、Go、C编译器（race）及下载固定Go工具链/扫描器的网络。缺少工具或网络故障即停止；不要通过删测试或改覆盖率门槛来绕过。

## 证据与失败恢复

Actions保存 `quality-results`、`security-results`、`release-candidate-evidence`（14天）。其中包含覆盖率原始文件、精确计数、race/vet/fuzz、基准输出、扫描结果和经过验证的SHA。

若源分支在检查期间前进，旧候选不发布；让新提交触发流程。若分支或标签冲突，原子推送失败，不强推、不覆盖已发布标签。若标签已推送而GitHub Release API失败，已推送的标签仍是经过验证的候选；核对原运行证据和标签SHA后，只补建该标签的Release。不要删除重打标签，也不要用新main伪装成同一版本。

这约束仓库自带的自动与手动发布路径，不是管理员无法绕过的权限隔离。拥有写权限的人仍可能直接git push标签/API创建Release；需要组织级禁止绕过时，应另配GitHub标签ruleset、分支保护和发布环境审批。当前改动不擅自修改仓库管理员权限。

官方机制说明：https://docs.github.com/en/actions/how-tos/reuse-automations/reuse-workflows
