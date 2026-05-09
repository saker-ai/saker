中文 | [English](README.md)

# saker 示例

十七个示例，均可在仓库根目录运行。

**环境配置**

1. 复制 `.env.example` 为 `.env` 并设置 API 密钥：
```bash
cp .env.example .env
# 编辑 .env 文件，设置 ANTHROPIC_API_KEY=sk-ant-your-key-here
```

2. 加载环境变量：
```bash
source .env
```

或者直接导出：
```bash
export ANTHROPIC_API_KEY=sk-ant-your-key-here
```

**学习路径**
- `01-basic`（~36 行）：单次 API 调用，最小用法，打印一次响应。
- `02-cli`（~93 行）：交互式 REPL，会话历史，可选读取 `.saker/settings.json`。
- `03-http`（~300 行）：REST + SSE 服务，监听 `:8080`，生产级组合。
- `04-advanced`（~1400 行）：全功能集成，包含 middleware、hooks、MCP、sandbox、skills、subagents。
- `05-custom-tools`（~58 行）：选择性内置工具和自定义工具注册。
- `06-embed`（~181 行）：通过 `go:embed` 嵌入 `.saker` 目录到二进制文件。
- `07-multimodel`（~130 行）：多模型池，分层路由和成本优化。
- `08-askuserquestion`（~474 行）：AskUserQuestion 工具集成，多种演示场景。
- `09-task-system`（~56 行）：任务跟踪与依赖管理。
- `10-hooks`（~85 行）：Hooks 系统，PreToolUse/PostToolUse shell 钩子。
- `11-reasoning`（~186 行）：推理模型支持（DeepSeek-R1 reasoning_content 透传）。
- `12-multimodal`（~135 行）：多模态内容块（文本 + 图片）。
- `13-govm-sandbox`（聚焦演示）：独立展示 govm sandbox 的只读/可写挂载与每会话工作区输出。
- `14-artifact-pipeline`（~65 行）：Artifact 多模态流水线，带 stub 工具。
- `15-resumable-review`（~130 行）：检查点与人工审核恢复。
- `16-timeline`（~100 行）：查看多模态执行时间线事件。
- `17-pipeline-cli`（~180 行）：Pipeline CLI 演示，缓存 + 时间线 + 血缘 DAG 导出（无需 API key）。

## 01-basic — 最小入门
- 目标：最快看到 SDK 核心循环，一次请求一次响应。
- 运行：
```bash
source .env
go run ./examples/01-basic
```

## 02-cli — 交互式 REPL
- 关键特性：交互输入、按会话保留历史、可选 `.saker/settings.json` 配置。
- 运行：
```bash
source .env
go run ./examples/02-cli --session-id demo --settings-path .saker/settings.json
```

## 03-http — REST + SSE
- 关键特性：`/health`、`/v1/run`（阻塞）、`/v1/run/stream`（SSE，15s 心跳）；默认端口 `:8080`。完全线程安全的 Runtime 自动处理并发请求。
- 运行：
```bash
source .env
go run ./examples/03-http
```

## 04-advanced — 全功能集成
- 关键特性：完整链路，涵盖 middleware 链、hooks、MCP 客户端、sandbox 控制、skills、subagents、流式输出。
- 运行：
```bash
source .env
go run ./examples/04-advanced --prompt "安全巡检" --enable-mcp=false
```

## 05-custom-tools — 自定义工具注册
- 关键特性：选择性内置工具（`EnabledBuiltinTools`）、自定义工具实现（`CustomTools`）、演示工具过滤与注册。
- 运行：
```bash
source .env
go run ./examples/05-custom-tools
```
- 详细用法和自定义工具实现指南见 [05-custom-tools/README.md](05-custom-tools/README.md)。

## 06-embed — 嵌入式文件系统
- 关键特性：`EmbedFS` 将 `.saker` 目录嵌入二进制文件，嵌入配置与磁盘配置的优先级解析。
- 运行：
```bash
source .env
go run ./examples/06-embed
```

## 07-multimodel — 多模型支持
- 关键特性：模型池配置、分层模型路由（low/mid/high）、子代理-模型映射、成本优化。
- 运行：
```bash
source .env
go run ./examples/07-multimodel
```
- 配置示例和最佳实践见 [07-multimodel/README.md](07-multimodel/README.md)。

## 08-askuserquestion — AskUserQuestion 工具
- 关键特性：通过 build tag 选择三种演示模式。
- 运行：
```bash
source .env
(cd examples/08-askuserquestion && go run .)                  # 完整 agent 场景
(cd examples/08-askuserquestion && go run -tags demo_llm .)   # LLM 集成测试
(cd examples/08-askuserquestion && go run -tags demo_simple .) # 纯工具测试（无需 API key）
```
- 详细用法和实现模式见 [08-askuserquestion/README.md](08-askuserquestion/README.md)。

## 09-task-system — 任务跟踪
- 关键特性：任务创建、依赖管理、通过内置任务工具进行状态跟踪。
- 运行：
```bash
source .env
go run ./examples/09-task-system
```

## 10-hooks — Hooks 系统
- 关键特性：`PreToolUse`/`PostToolUse` shell 钩子、异步执行、单次去重。
- 运行：
```bash
source .env
go run ./examples/10-hooks
```

## 11-reasoning — 推理模型
- 关键特性：思维模型的 `reasoning_content` 透传（DeepSeek-R1）、流式支持、多轮对话。
- 运行：
```bash
export OPENAI_API_KEY=your-key
export OPENAI_BASE_URL=https://api.deepseek.com/v1
go run ./examples/11-reasoning
```

## 12-multimodal — 多模态内容
- 关键特性：文本 + 图片内容块（base64 和 URL）、`api.Request` 中的 `ContentBlocks`。
- 运行：
```bash
source .env
go run ./examples/12-multimodal
```

## 13-govm-sandbox — 独立 govm sandbox 示例
- 关键特性：只读 `/inputs`、可写 `/shared`、自动创建 `workspace/<session-id>` 会话工作区、host 侧文件回收验证。
- 运行：
```bash
go run ./examples/13-govm-sandbox
```
- 预期输出和生成文件见 [13-govm-sandbox/README.md](13-govm-sandbox/README.md)。

## 14-artifact-pipeline — 多模态 Artifact
- 关键特性：流水线驱动执行、带 kind/source 元数据的 artifact 引用、跨步骤的血缘边追踪。
- 运行：
```bash
source .env
go run ./examples/14-artifact-pipeline
```

## 15-resumable-review — 检查点与恢复
- 关键特性：流水线中途创建检查点、从检查点 ID 恢复、人工审核门控模式。
- 运行：
```bash
source .env
go run ./examples/15-resumable-review
```

## 16-timeline — 执行时间线
- 关键特性：结构化时间线，包含 10 种事件类型（tool_call、tool_result、cache_hit/miss、latency_snapshot、input/generated artifact、checkpoint、token_snapshot）。
- 运行：
```bash
source .env
go run ./examples/16-timeline
```

## 17-pipeline-cli — Pipeline CLI 演示
- 关键特性：从 JSON 文件加载声明式流水线、fan-out 带并发控制、时间线事件追踪、血缘 DAG 导出（Graphviz DOT 格式）。**无需 API key** — 所有工具均为本地 stub。
- 运行：
```bash
# 基础运行
go run ./examples/17-pipeline-cli --pipeline examples/17-pipeline-cli/pipeline.json

# 显示时间线事件
go run ./examples/17-pipeline-cli --pipeline examples/17-pipeline-cli/pipeline.json --timeline

# 输出血缘图（DOT 格式）
go run ./examples/17-pipeline-cli --pipeline examples/17-pipeline-cli/pipeline.json --lineage dot

# 全部功能
go run ./examples/17-pipeline-cli --pipeline examples/17-pipeline-cli/pipeline.json --timeline --lineage dot

# 通过 Makefile
make demo-pipeline
```
- 详细用法、Pipeline JSON 格式和预期输出见 [17-pipeline-cli/README.md](17-pipeline-cli/README.md)。
