# Saker 功能验证用例

## 概述

本文档列出用于验证 saker 核心功能的集成测试和烟雾测试用例。

## 一、Go 集成测试

路径：`test/runtime/toolgroups/toolgroups_integration_test.go`

```bash
go test ./test/runtime/toolgroups/... -v -count=1
```

### 用例清单

| # | 测试名 | 验证内容 |
|---|--------|----------|
| 1 | TestCLIPreset_ExcludesCanvasAndBrowser | CLI 模式不注册 canvas/browser/webhook 工具 |
| 2 | TestPlatformPreset_IncludesCanvasAndBrowser | Platform 模式包含 canvas/browser，排除 interaction |
| 3 | TestCIPreset_MinimalTools | CI 模式只注册 core_io + bash_mgmt（最小集） |
| 4 | TestModePresetOverride | Options.ModePreset 覆盖 EntryPoint 的默认推断 |
| 5 | TestEnabledBuiltinToolsWhitelist | EnabledBuiltinTools 白名单正确过滤工具集 |
| 6 | TestDisallowedToolsBlacklist | DisallowedTools 黑名单正确移除指定工具 |
| 7 | TestRuntimeRun_ToolExecution | 完整 Run 循环：模型调用 file_read 工具 → 返回结果 |
| 8 | TestRuntimeRun_CIBashExecution | CI 模式下执行 bash 工具的完整流程 |
| 9 | TestRuntimeRunStream | RunStream 返回事件通道，至少有一个事件 |
| 10 | TestRuntimeMultipleRuns | 同一 Runtime 连续执行多次 Run 不报错 |
| 11 | TestAvailableToolsForWhitelist | AvailableToolsForWhitelist 交集过滤正确 |
| 12 | TestRuntimeRun_Timeout | context 超时能终止执行 |
| 13 | TestEnabledBuiltinToolKeys | EnabledBuiltinToolKeys 辅助函数返回正确的 key 列表 |

### 关键验证点

- **工具分组隔离**：CLI 不拿到 canvas/browser，CI 只拿最小集
- **运行时生命周期**：New → Run/RunStream → Close 完整路径
- **工具执行**：mock 模型发出 tool_call，runtime 正确分发执行
- **超时保护**：context cancel 能中断死循环

## 二、CLI 烟雾测试

路径：`test/cli_smoke_test.sh`

```bash
chmod +x test/cli_smoke_test.sh
./test/cli_smoke_test.sh ./bin/saker
```

### 用例清单

| # | 场景 | 验证方法 |
|---|------|----------|
| 1 | 二进制构建 | --help 输出 "Usage:" |
| 2 | --print 模式 | 发送 prompt，检查输出包含预期文本（需 API key） |
| 3 | --output-format json | 输出能被 python3 json.load 解析 |
| 4 | --entry 标志 | cli/ci/platform 三个值均被接受 |
| 5 | preset 单元测试 | go test 通过 |
| 6 | toolgroups 集成测试 | go test 通过 |
| 7 | server 模式启动 | 启动后 /health 或 / 可访问 |
| 8 | --api-only 模式 | 进程启动不崩溃 |
| 9 | --pipeline 模式 | 执行一步 bash echo，有输出 |

### 前置条件

- Go 1.26+
- 设置 `ANTHROPIC_API_KEY` 或 `OPENAI_API_KEY`（--print 测试需要）
- 端口 19876、19877 未被占用

## 三、已有测试套件

```bash
# 完整测试
go test ./... -count=1

# 核心 API 层
go test ./pkg/api/... -count=1

# Pipeline 场景
go test ./test/pipeline/... -count=1

# 子代理集成
go test ./test/runtime/... -count=1

# 安全测试
go test ./test/security/... -count=1
```

## 四、验证流程

1. **编译验证**：`go build ./...`
2. **单元测试**：`go test ./pkg/api/... ./pkg/tool/... -count=1`
3. **集成测试**：`go test ./test/runtime/toolgroups/... -v -count=1`
4. **烟雾测试**：`./test/cli_smoke_test.sh`
5. **全量回归**：`go test ./... -count=1`

## 五、常见问题

- **aigo 工具干扰断言**：设置了 DASHSCOPE_API_KEY 等会自动注册 aigo 工具。测试中用 `clearAigoEnv(t)` 清除。
- **端口冲突**：烟雾测试用 19876/19877 端口，确保未被占用。
- **无 API key**：--print 测试会自动跳过，不影响其他用例。
