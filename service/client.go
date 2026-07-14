package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"cc-agent-go/config"
	"cc-agent-go/model"
)

// Chat 发送非流式请求到 DeepSeek，返回 *ApiResponse（文本 + 工具调用 + token 用量）。
//
// 参数:
//   - messages: 对话历史
//   - systemPrompt: 系统提示词
//   - cfg: API 配置
//   - tools: 工具定义数组，传给 API 让模型知道有哪些工具可用
//
// 返回值从 v6 的 (string, []ToolCall, error) 改为 (*ApiResponse, error)，
// 好处：返回 struct 指针避免多返回值过长；出错时返回 nil。
func Chat(messages []model.Message, systemPrompt string, cfg config.Config,
	tools []map[string]any, maxTokens int) (*model.ApiResponse, error) {

	body := map[string]any{
		"model":      cfg.Model,
		"max_tokens": maxTokens,
		"system":     systemPrompt,
		"messages":   messages,
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

	req, err := http.NewRequest("POST", cfg.ApiEndpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", cfg.ApiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应体失败: %w", err)
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("解析响应 JSON 失败: %w", err)
	}

	contentList, ok := result["content"].([]any)
	if !ok || len(contentList) == 0 {
		return nil, fmt.Errorf("响应中没有 content 字段")
	}

	var textBuilder strings.Builder
	var toolCalls []model.ToolCall

	for _, item := range contentList {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		blockType, _ := block["type"].(string)
		switch blockType {
		case "text":
			textBuilder.WriteString(stringOrEmpty(block["text"]))
		case "tool_use":
			tc := model.ToolCall{
				ID:   stringOrEmpty(block["id"]),
				Name: stringOrEmpty(block["name"]),
			}
			if input, ok := block["input"].(map[string]any); ok {
				tc.Input = input
			}
			toolCalls = append(toolCalls, tc)
		}
	}
	text := textBuilder.String()

	// 读取 stop_reason，诊断发言被截断的原因
	if stopReason, ok := result["stop_reason"].(string); ok {
		fmt.Printf("[Chat] stop_reason=%s, output_tokens≈%d, text_len=%d\n",
			stopReason, intFromMap(result, "usage", "output_tokens"), len(text))
	}

	// 提取 token 用量
	inputTokens := intFromMap(result, "usage", "input_tokens")
	outputTokens := intFromMap(result, "usage", "output_tokens")

	return &model.ApiResponse{
		Text:         text,
		ToolCalls:    toolCalls,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}, nil
}

// intFromMap 从嵌套 map 中安全读取 int 值（JSON 数字 → float64 → int）。
func intFromMap(m map[string]any, keys ...string) int {
	for i, key := range keys {
		if i == len(keys)-1 {
			if v, ok := m[key].(float64); ok {
				return int(v)
			}
			return 0
		}
		if sub, ok := m[key].(map[string]any); ok {
			m = sub
		} else {
			return 0
		}
	}
	return 0
}

// stringOrEmpty 从 map 中安全取 string 值，不存在或类型不对返回空字符串。
func stringOrEmpty(v any) string {
	s, _ := v.(string)
	return s
}
