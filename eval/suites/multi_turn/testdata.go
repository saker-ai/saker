// Package multi_turn defines evaluation cases for multi-turn conversation
// behavior: context retention, session isolation, and history management.
package multi_turn

// Turn represents a single conversation turn.
type Turn struct {
	Prompt         string // user input
	ExpectedOutput string // expected substring in response
}

// MultiTurnCase represents a multi-turn conversation scenario.
type MultiTurnCase struct {
	Name      string
	SessionID string
	Turns     []Turn
}

// ContextRetentionCases tests whether the model retains context across turns.
func ContextRetentionCases() []MultiTurnCase {
	return []MultiTurnCase{
		{
			Name:      "remember_file_name",
			SessionID: "ctx-file",
			Turns: []Turn{
				{Prompt: "我正在编辑 main.go 文件", ExpectedOutput: "main.go"},
				{Prompt: "那个文件里有什么函数？", ExpectedOutput: "main.go"},
			},
		},
		{
			Name:      "remember_task_context",
			SessionID: "ctx-task",
			Turns: []Turn{
				{Prompt: "我需要修复登录模块的一个 bug", ExpectedOutput: "登录"},
				{Prompt: "这个 bug 的根因是什么？", ExpectedOutput: "登录"},
			},
		},
		{
			Name:      "remember_language_choice",
			SessionID: "ctx-lang",
			Turns: []Turn{
				{Prompt: "我们用 Go 语言开发", ExpectedOutput: "Go"},
				{Prompt: "推荐一个测试框架", ExpectedOutput: "Go"},
			},
		},
		{
			Name:      "multi_hop_context",
			SessionID: "ctx-hop",
			Turns: []Turn{
				{Prompt: "项目名叫 saker", ExpectedOutput: "saker"},
				{Prompt: "它是用 Go 写的", ExpectedOutput: "Go"},
				{Prompt: "总结一下这个项目", ExpectedOutput: "saker"},
			},
		},
	}
}

// SessionIsolationCases tests that different sessions don't share context.
func SessionIsolationCases() []struct {
	Name     string
	Session1 struct{ ID, Prompt, Output string }
	Session2 struct{ ID, Prompt, Output string }
} {
	return []struct {
		Name     string
		Session1 struct{ ID, Prompt, Output string }
		Session2 struct{ ID, Prompt, Output string }
	}{
		{
			Name: "different_sessions_isolated",
			Session1: struct{ ID, Prompt, Output string }{
				ID: "session-alpha", Prompt: "我在做项目 Alpha", Output: "Alpha",
			},
			Session2: struct{ ID, Prompt, Output string }{
				ID: "session-beta", Prompt: "我在做项目 Beta", Output: "Beta",
			},
		},
	}
}

// MessageOrderCases tests that message history maintains correct order.
func MessageOrderCases() []MultiTurnCase {
	return []MultiTurnCase{
		{
			Name:      "sequential_instructions",
			SessionID: "order-seq",
			Turns: []Turn{
				{Prompt: "第一步：创建目录", ExpectedOutput: "第一步"},
				{Prompt: "第二步：初始化项目", ExpectedOutput: "第二步"},
				{Prompt: "第三步：编写代码", ExpectedOutput: "第三步"},
				{Prompt: "回顾所有步骤", ExpectedOutput: "第一步"},
			},
		},
	}
}
