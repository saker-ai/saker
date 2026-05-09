package server

import "time"

// JSON-RPC 2.0 envelope types.

// Request represents a JSON-RPC 2.0 request from the client.
type Request struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id,omitempty"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}

// Response represents a JSON-RPC 2.0 response to the client.
type Response struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Result  any    `json:"result,omitempty"`
	Error   *Error `json:"error,omitempty"`
}

// Notification represents a JSON-RPC 2.0 server-initiated notification (no ID).
type Notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// Error represents a JSON-RPC 2.0 error object.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Standard JSON-RPC error codes.
const (
	ErrCodeParse          = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternal       = -32603
)

// Business data types.

// Thread represents a conversation thread.
type Thread struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ThreadItem represents a single message or event within a thread.
type ThreadItem struct {
	ID        string     `json:"id"`
	ThreadID  string     `json:"thread_id"`
	TurnID    string     `json:"turn_id,omitempty"`
	Role      string     `json:"role"` // "user", "assistant", "system", "tool"
	ToolName  string     `json:"tool_name,omitempty"`
	Content   string     `json:"content"`
	Artifacts []Artifact `json:"artifacts,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// Artifact represents a media output produced by a tool.
type Artifact struct {
	Type string `json:"type"`           // "image", "video", "audio"
	URL  string `json:"url"`            // /api/files/... path
	Name string `json:"name,omitempty"` // tool name that produced it
}

// ApprovalRequest is pushed to the client when the runtime needs permission.
type ApprovalRequest struct {
	ID         string         `json:"id"`
	ThreadID   string         `json:"thread_id"`
	TurnID     string         `json:"turn_id"`
	ToolName   string         `json:"tool_name"`
	ToolParams map[string]any `json:"tool_params,omitempty"`
	Reason     string         `json:"reason,omitempty"`
}

// QuestionOption is a selectable option for a question (mirrors toolbuiltin.QuestionOption).
type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// QuestionItem represents a single question with options.
type QuestionItem struct {
	Question    string           `json:"question"`
	Header      string           `json:"header"`
	Options     []QuestionOption `json:"options"`
	MultiSelect bool             `json:"multiSelect"`
}

// QuestionRequest is pushed to the client when the agent calls AskUserQuestion.
type QuestionRequest struct {
	ID        string         `json:"id"`
	ThreadID  string         `json:"thread_id"`
	TurnID    string         `json:"turn_id"`
	Questions []QuestionItem `json:"questions"`
}
