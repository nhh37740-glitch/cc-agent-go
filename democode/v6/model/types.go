package model

// ContentBlock 消息中的内容块。DeepSeek Anthropic 格式要求 content 是数组，
// 每个元素可以是 text（文本）、tool_use（工具调用请求）、tool_result（工具执行结果）
type ContentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`          // tool_use 使用
	Name      string         `json:"name,omitempty"`        // tool_use 使用
	Input     map[string]any `json:"input,omitempty"`       // tool_use 使用
	ToolUseID string         `json:"tool_use_id,omitempty"` // tool_result 使用
	Content   string         `json:"content,omitempty"`     // tool_result 使用（纯文本结果）
}

// Message 对话历史中的一条消息
type Message struct {
	Role    string         `json:"role"`    // "user" 或 "assistant"
	Content []ContentBlock `json:"content"` // 内容块数组
}

// ToolCall 从 API 响应中解析出的工具调用
type ToolCall struct {
	ID    string         // 工具调用 ID，回传结果时匹配
	Name  string         // 工具名，如 "bash"
	Input map[string]any // 工具参数，如 {"command": "echo hello"}
}

// ChatRequest 前端发来的 JSON 请求体
type ChatRequest struct {
	Message string `json:"message"`
}

// ChatResponse 返回给前端的 JSON 响应体
type ChatResponse struct {
	Reply string `json:"reply"`
}
