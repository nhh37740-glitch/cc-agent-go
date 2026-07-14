package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"cc-agent-go/democode/v6/config"
	"cc-agent-go/democode/v6/model"
)

// Chat 发送非流式请求到 DeepSeek，返回 AI 的文本回复和可能的工具调用。
//
// 参数:
//   - messages: 对话历史
//   - systemPrompt: 系统提示词
//   - cfg: API 配置
//   - tools: 工具定义数组，传给 API 让模型知道有哪些工具可用
//
// 返回值:
//   - text: 模型回复中的纯文本（可能是最终回复，也可能是工具调用前的说明文字）
//   - toolCalls: 模型请求调用的工具列表
//   - error
func Chat(messages []model.Message, systemPrompt string, cfg config.Config,
	tools []map[string]any) (string, []model.ToolCall, error) {

	body := map[string]any{
		"model":      cfg.Model,
		"max_tokens": 4096,
		"system":     systemPrompt,
		"messages":   messages,
	}
	// 只有有工具时才加 tools 字段
	if len(tools) > 0 {
		body["tools"] = tools
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", nil, fmt.Errorf("序列化请求体失败: %w", err)
	}
	// v6 调试：打印请求体
	fmt.Printf("[Chat] 请求体: %s\n", string(jsonBody))

	req, err := http.NewRequest("POST", cfg.ApiEndpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return "", nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", cfg.ApiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("API 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("读取响应体失败: %w", err)
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", nil, fmt.Errorf("解析响应 JSON 失败: %w", err)
	}

	contentList, ok := result["content"].([]any)
	if !ok || len(contentList) == 0 {
		return "", nil, fmt.Errorf("响应中没有 content 字段")
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

	return text, toolCalls, nil
}

// stringOrEmpty 从 map 中安全取 string 值，不存在或类型不对返回空字符串。
func stringOrEmpty(v any) string {
	s, _ := v.(string)
	return s
}
