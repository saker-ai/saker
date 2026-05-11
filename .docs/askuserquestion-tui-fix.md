# 修复 AskUserQuestion 在 CLI/TUI 模式下的"伪成功"幻觉 bug

## 背景

`AskUserQuestion` 工具在 saker 的 web 模式下工作正常（`pkg/server/handler_turn.go:250`
通过 `WithAskQuestionFunc` 注入了 askFn）。但在 TUI 模式下，LLM 调用此工具时：

1. 终端只显示一行 `AskUserQuestion — 13 lines`，**没有任何可交互面板弹出**；
2. 工具却返回 `Success: true` + 题面文本作为 Output；
3. LLM 收到"成功但无答案"的回执后**幻觉用户的选择**继续往下生成。

这是一个相当严重的正确性 bug —— 在用户没看到问题、没做出选择的情况下，模型继续推进
就等于在"用户的名义"下完成了未授权的操作。

## 三个叠加缺陷

| # | 文件 | 问题 |
|---|---|---|
| 1 | `pkg/tool/builtin/askuserquestion.go:80-108` | `askFn==nil` 时既不报错也不告知 LLM "拿不到答案"，反而返回 `Success:true` + 题面文本 |
| 2 | `pkg/clikit/runtime_adapter.go` | CLI/TUI 链路从未调用 `WithAskQuestionFunc(ctx, ...)` |
| 3 | `pkg/clikit/tui/` | TUI 缺乏问答面板组件（只有 `SidePanel` 服务 `/btw` 和 `/im`） |

## 解决方案

按依赖顺序拆 3 个 commit：

### Step 1 — askuserquestion.go 兜底（P0）

把"`askFn==nil` 时悄悄成功"改成"明确报错给 LLM"：

```go
if len(answers) == 0 {
    askFn := AskQuestionFuncFromContext(ctx)
    if askFn == nil {
        return &tool.ToolResult{
            Success: false,
            Output:  "AskUserQuestion is not available in this environment ...",
            Data:    map[string]interface{}{"questions": questions},
        }, nil
    }
    answers, err = askFn(ctx, questions)
    if err != nil {
        return nil, fmt.Errorf("ask user question: %w", err)
    }
}
```

这样即使 TUI/legacy REPL 没接通，LLM 至少会被明确告知"工具不可用"，不会幻觉用户选择。

### Step 2 — RuntimeAdapter 注入 askFn

在 `pkg/clikit/` 新增：

- **`pkg/clikit/ask.go`**：定义 `AskQuestionRegistrar` 接口，避免 `tui` 直接强依赖
- **`pkg/clikit/runtime_adapter.go`**：新增 `askQuestionFunc` 字段、`SetAskQuestionFunc`
  setter、`withAskQuestion(ctx)` 包装；`RunStream`/`RunStreamForked`/`Run` 都用它包装 ctx

线程安全：用 `sync.RWMutex` 保护，方便从 bubbletea 主线程注入、从工具执行 goroutine 读取。

### Step 3 — TUI QuestionPanel 组件

#### 3a. `pkg/clikit/tui/question_panel.go`（新增）

- 字段：`questions`, `currentIdx`, `cursor`, `selected map[int]map[int]bool`,
  `mode (qmodeSelect/qmodeOtherInput)`, `otherText`, `outcome chan`
- `HandleKey`：↑/↓ 移光标、Space 多选切换、Enter 单选/多选提交、Esc 取消、
  Backspace 编辑 Other 输入；rune 字符直接拼到 `otherText`
- "Other..." 菜单项始终位于选项末尾（index = `len(q.Options)`），
  Enter 进入文本输入子模式
- 多选 Enter 无任何选中时，把光标行隐式选中（"快速选这一个"友好交互）
- `Cancel()` 幂等：第二次调用是 no-op
- `View()` 用 `lipgloss` 渲染：题号进度、题面、提示行、选项列表、Hint bar

#### 3b. App 集成（`pkg/clikit/tui/app.go`）

新增字段：

```go
questionPanel    *QuestionPanel
questionOutcome  <-chan QuestionPanelOutcome
questionDeliver  chan<- QuestionPanelOutcome
prevInputEnabled bool
program          *tea.Program  // 保存以供跨线程 program.Send()
```

`Run()` 中保存 program 引用并通过类型断言注入 askFn：

```go
app.program = p
if r, ok := cfg.Engine.(clikit.AskQuestionRegistrar); ok {
    r.SetAskQuestionFunc(app.askQuestionFromTUI)
    defer r.SetAskQuestionFunc(nil)
}
```

#### 3c. `app_question.go`（新增）

`askQuestionFromTUI` 桥接：用 `program.Send(OpenQuestionPanelMsg{...})` 唤起面板，
然后阻塞在 reply channel 上，直到用户提交（返回 answers）或 ctx 取消（返回 ctx.Err）。

#### 3d. `app_update.go` 集成

新 msg 类型（在 `messages.go`）：
- `OpenQuestionPanelMsg{Questions, Reply}`
- `CloseQuestionPanelMsg{}`
- `QuestionPanelDoneMsg{Outcome}`

Update 分支：
- `OpenQuestionPanelMsg` → 创建 panel、保存状态、禁用 input、启动
  `waitForQuestionOutcome` cmd（在 goroutine 里 select panel 的 outcome channel）
- `QuestionPanelDoneMsg` → 把 outcome 推给 `questionDeliver`，清理 panel 状态
- `CloseQuestionPanelMsg` → 调 `panel.Cancel()`（自然走 QuestionPanelDoneMsg 路径）

`handleKey` 中 question panel 优先级最高（高于 sidePanel）。

#### 3e. `app_view.go`

`View()` 顶部加分支：question panel 活跃时只渲染 `panel + status`，覆盖 chat 主区域。

## 关键设计决策

1. **不用 bubbles/textinput**：自己实现简单的 rune 输入，保持 panel 自包含
2. **buffered (size 1) outcome channel**：避免 `Cancel()` 与正常 deliver 之间的竞态
3. **类型断言注入**：`AskQuestionRegistrar` 接口让 TUI 不强依赖 RuntimeAdapter 具体类型
4. **嵌套保护**：当 panel 已经活跃时再来 `OpenQuestionPanelMsg`，立即推 `Cancelled`
   outcome 让第二个调用方失败（v1 不支持嵌套问答）

## 验证

- `pkg/tool/builtin/askuserquestion_test.go`：新增 `TestAskUserQuestionNoAskFnReturnsFailure`
  和 `TestAskUserQuestionAskFnError`
- `pkg/clikit/runtime_adapter_test.go`：新增 `TestRuntimeAdapterInjectsAskQuestionFunc`
  覆盖 nil-handler/inject/clear/Forked 四种路径
- `pkg/clikit/tui/question_panel_test.go`：11 个用例，覆盖单选/多选/Other/取消/多题/View 渲染
- `make build` 通过；`make test` 全量通过（pkg/server 的 TestCronHandler_Run 是预存在
  flake，与本次改动无关）

## 风险

| 风险 | 缓解 |
|---|---|
| `program.Send` 在 `p.Run()` 之前 panic | `Run()` 里先 `app.program = p` 再 `p.Run()`；askFn 注入也在 Run 内，由 LLM 调用触发，时序天然安全 |
| 跨 goroutine：tool 推 channel、bubbletea 主线程读 | `program.Send()` 是官方推荐的跨线程入口，已经线程安全 |
| ctx 取消竞态 | reply channel buffer=1；`select` 里 ctx.Done 与 reply 平等优先，取哪个都安全 |
| 嵌套调用 AskUserQuestion（罕见）| v1 拒绝：`questionPanel != nil` 时立即推 `Cancelled` outcome 让第二个调用失败 |

## 不动的部分

- `pkg/server/handler_turn.go`（web 路径已工作）
- `pkg/clikit/repl.go`（legacy REPL 由 Step 1 兜底报错，不再幻觉）

---

# v2 UX 重构：多题导航 + 统一 review 屏

## 背景

v1 把面板跑通了，但当 LLM 一次抛出多个问题时（claude-code 工具规范本身允许 1–4 题），
原 UX 有四个硬伤：

1. **强制顺序推进**：选完 Q1 立即跳 Q2，没法回看已答、没法跳过先答简单的
2. **缺整体视图**：看不到"全部 N 题里答到第几"
3. **无确认机会**：最后一题 Enter 直接交付给 LLM，来不及核对
4. **状态丢失**：cursor/mode/otherText 都是单值，跨题切换会被冲掉

参考实现：`.other/claude-code/src/components/permissions/AskUserQuestionPermissionRequest/`
（React/Ink）。

## 参考的 UX 模式

| 模式 | claude-code 来源 | 关键点 |
|---|---|---|
| 顶部 tab 导航条 | `QuestionNavigationBar.tsx` | 每题一个 tab，`☐`/`✓` checkbox + header；当前 tab 反白；末尾 `✓ Submit` tab |
| 自由跳转 | `AskUserQuestionPermissionRequest.tsx` | Tab/Shift+Tab 仅改 `currentQuestionIndex`，不动答案 |
| 单题单选短路 | `hideSubmitTab = questions.length === 1 && !questions[0]?.multiSelect` | 单题单选选完直接交付；其它都走 review |
| 最终 review 屏 | `SubmitQuestionsView.tsx` | 列出所有 Q&A，警告未答全，Submit/Cancel 选项 |
| 状态保留 | `useMultipleChoiceState` | `questionStates: Record<questionText, QuestionState>` 跨切换持久化 |

## 实施

### 状态模型重塑（`pkg/clikit/tui/question_panel.go`）

把"单值 cursor/mode/otherText" 改成"按题号索引的字典"：

```go
type QuestionPanel struct {
    // currentIdx ∈ [0, len(questions)] —— 等于 len(questions) 时为 review 屏
    currentIdx int

    // 按题号保存的 per-question 状态
    cursors    map[int]int
    selected   map[int]map[int]bool
    otherTexts map[int]string
    modes      map[int]questionPanelMode

    reviewCursor  int  // 0=Submit, 1=Cancel
    hasReviewStep bool // 单题单选 → false
    // ...
}
```

`hasReviewStep` 在 `NewQuestionPanel` 计算：

```go
hasReview := !(len(qs) == 1 && !qs[0].MultiSelect)
```

### 键路由

`handleSelectKey` 新增：

```go
case "tab", "right":       p.gotoTab(p.currentIdx + 1)
case "shift+tab", "left":  p.gotoTab(p.currentIdx - 1)
```

`handleOtherInputKey` 也新增 Tab/Shift+Tab，Esc 改为"返回 select 模式且
**保留**输入文本"（v1 行为是清空 + 取消整个面板，太鲁莽）。

新增 `handleReviewKey` 处理 review 屏：↑/↓ 切 Submit/Cancel；Shift+Tab/← 回到
最后一题；Enter 按光标位置 deliverAnswers / Cancel。

### Smart advance

```go
func (p *QuestionPanel) advance() tea.Cmd {
    if !p.hasReviewStep { return p.deliverAnswers() }      // 单题单选短路
    if next := p.firstUnansweredAfter(p.currentIdx); next >= 0 {
        p.gotoTab(next); return nil
    }
    p.gotoReview(); return nil
}
```

`firstUnansweredAfter` 从 `i+1` 起循环到末尾再从 0 到 `i-1`，找第一道未答题；
都答完 → -1。

### View 重排

```
┌─ renderNavBar()    ☐ Color  ✓ OS  ☐ Size  ✓ Submit
├─ renderQuestion() / renderReview()
└─ renderHintBar() —— 按 mode 切提示
```

Nav bar 用 `Background(t.Primary).Foreground(t.Bg).Bold(true).Padding(0, 1)` 反白
当前 tab，已答用 Success 色，未答用 FgDim。

Review 屏：

```
[✓] Review your answers
⚠ Not all questions answered (2/3)         // 仅未答全时
• Pick a color?
  → Green
• Platforms?
  → Linux,macOS

Ready to submit your answers?

▶ Submit answers
  Cancel
```

## 测试扩充（`pkg/clikit/tui/question_panel_test.go`）

| 名 | 验证 |
|---|---|
| `TabNav_PreservesAnswers` | Q1 选 A → Tab 到 Q2 移光标 → Shift+Tab 回 Q1：answers["Q1?"]=="A" 仍在，Q1 cursor 恢复，Q2 cursor 也保留 |
| `SmartAdvance_SkipsAnswered` | 3 题：Q1/Q3 已答 → 在 Q2 答完 Enter → 跳到 review |
| `Review_SubmitDelivers` | review reviewCursor=0 → Enter → 完整 outcome.Answers |
| `Review_CancelDelivers` | reviewCursor=1 → Enter → outcome.Cancelled |
| `Review_ShiftTabReturnsToLastQuestion` | review Shift+Tab → currentIdx == 最后一题 |
| `SingleQuestionSingleSelect_NoReview` | `hasReviewStep == false`，lastTabIdx == 0 |
| `SingleQuestionMultiSelect_HasReview` | `hasReviewStep == true` |
| `OtherText_PreservedAcrossNav` | Q1 Other 输入 "abc" 未确认 → Tab 走 → 回来 → text 仍是 "abc"，mode 仍是 qmodeOtherInput |
| `NavBar_RendersCheckboxesAndSubmitTab` | View 含 ☐、Submit；答完后含 ✓ |
| `Review_ViewListsAnswers` | review View 含 "Review your answers"、Q&A 列表、Submit/Cancel |

现有 11 个 v1 用例全部保留，只调整了少量内部访问方式（`cursorOf(p)`/`modeOf(p)`/
`otherTextOf(p)` 三个测试 helper 替代直接访问字段；多选/多题路径加 review 步骤）。

## 关键设计决策（增量）

1. **Review 屏识别用 `currentIdx == len(questions)`**：避免给 mode enum 加新值，且与
   nav bar tab 索引天然对齐（review 就是末尾的 Submit tab）
2. **Other 文本不在 Esc 时清**：用户可能误按 Esc，保留文本让他能再 Enter 回去继续编辑
3. **多选单题仍走 review**：mirror claude-code，多选有"勾错框"风险，多一步确认值得
4. **lipgloss 反白用 `t.Bg`** 不用 `t.InverseText`：Theme 没有 InverseText 字段，
   `Bg` 在 Primary 背景下天然产生反色效果

## 验证

- `go test ./pkg/clikit/tui/... -count=1` 通过（含 21 个 question_panel 用例）
- `make build` 通过
- `make test` 全量通过

## 不动的部分

- `pkg/clikit/tui/app_update.go` —— `handleQuestionPanelKey` 已优先于 input forwarding，
  Tab/Shift+Tab 会被忠实转发到 panel
- `pkg/server/handler_turn.go` —— web 路径不受影响
