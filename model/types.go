package model

import (
	"encoding/json"
	"fmt"
)

// MessageContentBlock 是 Message.Content 允许保存的内容类型。
// 三个具体类型分别定义自己的必填字段，不共用一个包含所有可选字段的结构体。
type MessageContentBlock interface {
	messageContentBlock()
}

type TextContentBlock struct {
	Text string `json:"text"`
}

func (TextContentBlock) messageContentBlock() {}

func (textContentBlock TextContentBlock) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{
		Type: "text",
		Text: textContentBlock.Text,
	})
}

type ToolUseContentBlock struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

func (ToolUseContentBlock) messageContentBlock() {}

func (toolUseContentBlock ToolUseContentBlock) MarshalJSON() ([]byte, error) {
	toolInput := toolUseContentBlock.Input
	if toolInput == nil {
		toolInput = map[string]any{}
	}

	return json.Marshal(struct {
		Type  string         `json:"type"`
		ID    string         `json:"id"`
		Name  string         `json:"name"`
		Input map[string]any `json:"input"`
	}{
		Type:  "tool_use",
		ID:    toolUseContentBlock.ID,
		Name:  toolUseContentBlock.Name,
		Input: toolInput,
	})
}

type ToolResultContentBlock struct {
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
}

func (ToolResultContentBlock) messageContentBlock() {}

func (toolResultContentBlock ToolResultContentBlock) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type      string `json:"type"`
		ToolUseID string `json:"tool_use_id"`
		Content   string `json:"content"`
	}{
		Type:      "tool_result",
		ToolUseID: toolResultContentBlock.ToolUseID,
		Content:   toolResultContentBlock.Content,
	})
}

// Message 对话历史中的一条消息
type Message struct {
	Role    string                `json:"role"`    // "user" 或 "assistant"
	Content []MessageContentBlock `json:"content"` // 内容块数组
}

func (message *Message) UnmarshalJSON(messageJSON []byte) error {
	var messageFields struct {
		Role    string            `json:"role"`
		Content []json.RawMessage `json:"content"`
	}
	if decodeMessageError := json.Unmarshal(messageJSON, &messageFields); decodeMessageError != nil {
		return decodeMessageError
	}

	decodedContentBlocks := make(
		[]MessageContentBlock,
		0,
		len(messageFields.Content),
	)
	for contentBlockIndex, contentBlockJSON := range messageFields.Content {
		var contentBlockTypeField struct {
			Type string `json:"type"`
		}
		if decodeTypeError := json.Unmarshal(
			contentBlockJSON,
			&contentBlockTypeField,
		); decodeTypeError != nil {
			return fmt.Errorf(
				"解包第 %d 个消息内容类型失败: %w",
				contentBlockIndex,
				decodeTypeError,
			)
		}

		switch contentBlockTypeField.Type {
		case "text":
			var textContentBlock TextContentBlock
			if decodeTextError := json.Unmarshal(
				contentBlockJSON,
				&textContentBlock,
			); decodeTextError != nil {
				return fmt.Errorf(
					"解包第 %d 个文本内容失败: %w",
					contentBlockIndex,
					decodeTextError,
				)
			}
			decodedContentBlocks = append(
				decodedContentBlocks,
				textContentBlock,
			)
		case "tool_use":
			var toolUseContentBlock ToolUseContentBlock
			if decodeToolUseError := json.Unmarshal(
				contentBlockJSON,
				&toolUseContentBlock,
			); decodeToolUseError != nil {
				return fmt.Errorf(
					"解包第 %d 个工具调用内容失败: %w",
					contentBlockIndex,
					decodeToolUseError,
				)
			}
			if toolUseContentBlock.Input == nil {
				toolUseContentBlock.Input = map[string]any{}
			}
			decodedContentBlocks = append(
				decodedContentBlocks,
				toolUseContentBlock,
			)
		case "tool_result":
			var toolResultContentBlock ToolResultContentBlock
			if decodeToolResultError := json.Unmarshal(
				contentBlockJSON,
				&toolResultContentBlock,
			); decodeToolResultError != nil {
				return fmt.Errorf(
					"解包第 %d 个工具结果内容失败: %w",
					contentBlockIndex,
					decodeToolResultError,
				)
			}
			decodedContentBlocks = append(
				decodedContentBlocks,
				toolResultContentBlock,
			)
		default:
			return fmt.Errorf(
				"第 %d 个消息内容使用了不支持的 type %q",
				contentBlockIndex,
				contentBlockTypeField.Type,
			)
		}
	}

	message.Role = messageFields.Role
	message.Content = decodedContentBlocks
	return nil
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

// ErrorResponse 是非流式 HTTP 错误响应。ProviderStatus 只在 DeepSeek
// 返回非成功状态码时出现。
type ErrorResponse struct {
	Code           string `json:"code"`
	Message        string `json:"message"`
	ProviderStatus int    `json:"providerStatus,omitempty"`
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
