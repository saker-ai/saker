# R4-#6: //nolint 指令审计

## Scope

`grep -r "//\s*nolint"` 在 main 分支共 **69 处** 命中（task #63 估计 68 — 接近），
分布在 32 个文件。本次审计逐条判定保留/删除/修复。

## 处置结论

| 类别 | 总数 | 保留 | 删除 | 注 |
|---|---:|---:|---:|---|
| `nolint:errcheck`（生产代码 best-effort） | ~14 | 14 | 0 | 全部有明确"可丢弃"理由（spool / cleanup / 通知） |
| `nolint:errcheck`（test 文件） | ~25 | 0 | 25 | `.golangci.yml` 已对 `_test\.go` 全局豁免 errcheck，nolint 完全冗余 — 本提交清理 |
| `nolint:gosec` | 8 | 8 | 0 | 全部 sandbox 文件权限 0o666 + LDAP InsecureSkipVerify，注释解释充分 |
| `nolint:staticcheck` | 11 | 11 | 0 | SA1012 测试 nil-context、Anthropic SDK 弃用 model ID（API 仍接受）、`net.OpError.Temporary()`（重试链路依赖） |

净删除 25 行噪音指令，零生产逻辑变更。

## 本次清理

### `pkg/tool/output_test.go`：18 处全部删除

发现 11 处奇葩的 **双重指令** `//nolint:errcheck //nolint:errcheck`（典型复制粘贴
事故，golangci-lint 解析时会无视后一条）。剩余 7 处单写也同样冗余。

根因：`.golangci.yml::issues.exclusions.rules[0]` 已对 `_test\.go` 排除 errcheck，
所有测试文件中的 `//nolint:errcheck` 都不会被触发。

清理后 `go test -run TestSpoolWriter ./pkg/tool/` 仍全 PASS。

## 保留指令的逐条理由（生产代码）

### `pkg/api/runtime_helpers.go:579, 597`（2 处）

```go
held := existing.(*gateEntry) //nolint:errcheck // sync.Map guarantees type safety...
```

注释技术上不准（errcheck 不检查类型断言；这是 forcetypeassert 的领地），但
`.golangci.yml::errcheck.check-type-assertions: false` 已全局关闭该检查，所以
nolint 是 *防御性* 的，留以应对未来配置收紧。**保留 + 后续可改注释**。

### `pkg/api/runtime_helpers.go:436`、`pkg/api/agent_execute.go:228`、`pkg/api/agent_run.go:215`

事件总线 `Publish` / `cleanupToolOutputSessionDir` 失败属于"通知/清理类"非关键路径，
单纯打 log 即可，不应破坏调用链。**保留**。

### `pkg/sandbox/{landlock,gvisor,host}env/environment.go`（4 处 gosec）

`os.WriteFile(path, data, 0o666)` 故意保留 0o666 让 OS umask 生效（容器化
saker 通常 022，落盘 0644）。gosec G306 默认要求 0o600，与设计不符。**保留**。

### `pkg/server/auth_ldap.go:97`（gosec InsecureSkipVerify）

`tls.Config{InsecureSkipVerify: p.cfg.InsecureSkipVerify}` 是用户主动配置项
（私有 LDAP 自签证书场景）。gosec G402 误报。**保留**。

### `pkg/model/anthropic_request.go:508-524`（4 处 staticcheck）

Anthropic SDK 已标记 `ModelClaude3OpusLatest` 等为弃用，但 API 服务端仍接受
（且我们的兼容层需要把别名映射到实际模型）。直接删 import 会破坏向下兼容。
**保留**。

### `pkg/model/{openai_client,anthropic_request}.go::net.OpError.Temporary`

`net.OpError.Temporary()` 在 Go 1.18 标记弃用，但其语义对于"非超时类
transient 错误是否值得重试"无替代方案。tests 也依赖此行为。**保留**。

### `pkg/tool/registry_mcp.go::TODO`（已修）

R3 plan 列出的唯一真实 TODO（`mcp.BuildSessionTransport` → `mcp.ConnectSession`
迁移）已经在前置提交中清理，当前仓库 grep 不到该 nolint。**已完成**。

### `pkg/tool/builtin/{stream_monitor_*,bash_stream,grep,glob,killtask}.go`（10 处 errcheck）

任务存储更新、JSONL spool、gitignore matcher 重建 — 全部 best-effort 链路，
失败不应影响主流程，注释明确。**保留**。

### `pkg/sessiondb/store.go::defer tx.Rollback() //nolint:errcheck`（2 处）

经典 `defer tx.Rollback()` 模式 —— Commit 成功后 Rollback 返回 `ErrTxDone`
属预期，不需检查。Go 社区惯用法。**保留**。

## 后续建议

1. R5 / 后续 sprint：把 `pkg/api/runtime_helpers.go` 两处 nolint 注释改为
   `// errcheck.check-type-assertions=false; defensive nolint if config tightens`，
   或换成更精确的 `//nolint:forcetypeassert` 待 gocritic 启用相关 rule 时生效。
2. 当 R3 引入的 `gocritic`（已 enable）开始报 `typeAssertChain`/`appendAssign`
   时，按报告逐条决定是修代码还是加 nolint —— 不要批量噤声。
3. 任何新增 `//nolint:` 必须带 `:linter // 原因` 完整三段式，单纯 `//nolint` 拒收。
