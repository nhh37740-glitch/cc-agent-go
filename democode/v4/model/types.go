package model

// ContentBlock 消息中的内容块。每条消息的 content 是一个数组，
// 每个元素可以是 text（文本）或 tool_use（工具调用，v6 用）
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// Message 对话历史中的一条消息
type Message struct {
	Role    string         `json:"role"`    // "user" 或 "assistant"
	Content []ContentBlock `json:"content"` // 内容块数组，DeepSeek Anthropic 格式
}

// ChatRequest 前端发来的 JSON 请求体
type ChatRequest struct {
	Message string `json:"message"`
}

// ChatResponse 返回给前端的 JSON 响应体
type ChatResponse struct {
	Reply string `json:"reply"`
}
