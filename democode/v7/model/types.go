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
	Message        string `json:"message"`
	ConversationId string `json:"conversationId,omitempty"` // 可选：不传时服务端自动生成
}

// ChatResponse 返回给前端的 JSON 响应体
type ChatResponse struct {
	ConversationId string `json:"conversationId"`
	Reply          string `json:"reply"`
}

// ========== v7 新增 ==========

// ApiResponse 封装 DeepSeek API 调用的完整返回值。
// v6 中 Chat/ChatStream 返回 (string, []ToolCall, error)，
// v7 改为返回 (*ApiResponse, error)，把 token 用量也带出来。
type ApiResponse struct {
	Text         string     // AI 文本回复（工具调用时可能为空）
	ToolCalls    []ToolCall // 工具调用列表（无工具调用时为空切片）
	InputTokens  int        // API 返回的 input_tokens
	OutputTokens int        // API 返回的 output_tokens
}

// SessionJson 会话持久化格式。字段名和 JSON 结构与 Java cc-agent-java 的
// SessionJson record 保持一致，确保两个版本的会话文件可以互换。
type SessionJson struct {
	ConversationId           string    `json:"conversationId"`
	Title                    string    `json:"title"`
	RunningTotalTokens       int       `json:"runningTotalTokens"`
	ContextWindowLimitTokens int       `json:"contextWindowLimitTokens"`
	LastInputTokens          int       `json:"lastInputTokens"`
	LastOutputTokens         int       `json:"lastOutputTokens"`
	CreatedAt                float64   `json:"createdAt"` // epoch 秒 + 纳秒小数
	UpdatedAt                float64   `json:"updatedAt"` // epoch 秒 + 纳秒小数
	Messages                 []Message `json:"messages"`
}

// ConversationSummary 会话列表摘要。不包含 messages 数组，减少列表接口的传输量。
type ConversationSummary struct {
	ConversationId           string  `json:"conversationId"`
	Title                    string  `json:"title"`
	UpdatedAt                float64 `json:"updatedAt"`
	ContextWindowLimitTokens int     `json:"contextWindowLimitTokens"`
	CurrentInputTokens       int     `json:"currentInputTokens"`
	CurrentOutputTokens      int     `json:"currentOutputTokens"`
	CurrentTotalTokens       int     `json:"currentTotalTokens"`
	CurrentWindowTokens      int     `json:"currentWindowTokens"`
}

// Compressor 是压缩函数的签名：接收旧消息列表，返回 LLM 生成的摘要文本。
// Go 中函数是一等公民，type 定义函数签名后可以像普通类型一样作为参数传递 ——
// 等价于 Java 的 @FunctionalInterface。
type Compressor func(oldMessages []Message) (string, error)
