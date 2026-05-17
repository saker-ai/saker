package api

import (
	"testing"

	"github.com/saker-ai/saker/pkg/model"
)

func TestHasAskTool(t *testing.T) {
	tests := []struct {
		name  string
		tools []model.ToolDefinition
		want  bool
	}{
		{"present", []model.ToolDefinition{{Name: "bash"}, {Name: "ask_user_question"}}, true},
		{"absent", []model.ToolDefinition{{Name: "bash"}, {Name: "read_file"}}, false},
		{"empty", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasAskTool(tt.tools); got != tt.want {
				t.Errorf("hasAskTool() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLooksLikeTextQuestion(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{
			"chinese question with bullets",
			"好的！请问您想生成什么样的图片呢？比如：\n• 主题：风景、人物、动物等\n• 风格：写实、卡通、水彩等\n• 色调：明亮、暗调、复古等",
			true,
		},
		{
			"english question with dashes",
			"What kind of image would you like?\n- Landscape\n- Portrait\n- Abstract",
			true,
		},
		{
			"numbered list chinese",
			"请选择一个选项？\n1. 选项A\n2. 选项B",
			true,
		},
		{
			"numbered list with chinese numbering",
			"您想要哪种风格？\n1、写实风格\n2、卡通风格",
			true,
		},
		{
			"asterisk list",
			"Which approach do you prefer?\n* Option A\n* Option B",
			true,
		},
		{
			"circled numbers",
			"请选择？\n① 第一个\n② 第二个",
			true,
		},
		{
			"parenthesis numbers",
			"你想要什么？\n1) 方案一\n2) 方案二",
			true,
		},
		{
			"no question mark",
			"Here are some options:\n- Option A\n- Option B",
			false,
		},
		{
			"question but no list",
			"What do you want me to do?",
			false,
		},
		{
			"too short",
			"OK?",
			false,
		},
		{
			"code block with question mark",
			"Here is the fix:\n```go\nif err != nil { return fmt.Errorf(\"failed? %w\", err) }\n```\n- Line 1 fixed\n- Line 2 fixed",
			true,
		},
		{
			"plain text no structure",
			"I'll help you with that. Let me look into the code and find the issue.",
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksLikeTextQuestion(tt.text); got != tt.want {
				t.Errorf("looksLikeTextQuestion() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMatchesNumberedList(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"1. First item", true},
		{"1、第一项", true},
		{"1) First", true},
		{"① 第一", true},
		{"No numbers here", false},
		{"Line 2. something", false},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			if got := matchesNumberedList(tt.text); got != tt.want {
				t.Errorf("matchesNumberedList(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}
