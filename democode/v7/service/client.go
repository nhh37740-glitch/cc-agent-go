package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"cc-agent-go/democode/v7/config"
	"cc-agent-go/democode/v7/model"
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

	var text string
	var toolCalls []model.ToolCall

	for _, item := range contentList {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		blockType, _ := block["type"].(string)
		switch blockType {
		case "text":
			text, _ = block["text"].(string)
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

	// v7 新增：从 API 响应中提取 token 用量。
	// json.Unmarshal 到 map[string]any 时，JSON 数字一律解析为 float64，
	// 需要显式转换为 int。
	inputTokens := 0
	outputTokens := 0
	if usage, ok := result["usage"].(map[string]any); ok {
		if it, ok := usage["input_tokens"].(float64); ok {
			inputTokens = int(it)
		}
		if ot, ok := usage["output_tokens"].(float64); ok {
			outputTokens = int(ot)
		}
	}

	return &model.ApiResponse{
		Text:         text,
		ToolCalls:    toolCalls,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}, nil
}

// stringOrEmpty 从 map 中安全取 string 值，不存在或类型不对返回空字符串。
func stringOrEmpty(v any) string {
	s, _ := v.(string)
	return s
}
