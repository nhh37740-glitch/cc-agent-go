package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"cc-agent-go/democode/v5/config"
	"cc-agent-go/democode/v5/model"
)

// Chat 发送非流式请求到 DeepSeek，返回 AI 的文本回复
func Chat(messages []model.Message, systemPrompt string, cfg config.Config) (string, error) {
	// 第一步：用 map[string]any 构建请求体 JSON
	// map[string]any 是 Go 的动态类型，string 键，any 值（等价 Java Map<String, Object>）
	body := map[string]any{
		"model":      cfg.Model,
		"max_tokens": 4096,
		"system":     systemPrompt,
		"messages":   messages,
	}

	// json.Marshal 把 map/slice/struct 序列化为 []byte（JSON 字节数组）
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("序列化请求体失败: %w", err)
	}

	// http.NewRequest 创建 HTTP 请求对象，三个参数：方法、URL、请求体
	// bytes.NewReader(jsonBody) 把 []byte 包装成 io.Reader —— NewRequest 的第三个参数要求 io.Reader
	req, err := http.NewRequest("POST", cfg.ApiEndpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %w", err)
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", cfg.ApiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	// http.DefaultClient.Do 发送请求，返回 *http.Response
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("API 请求失败: %w", err)
	}
	defer resp.Body.Close() // 函数返回前关闭响应体，释放连接

	// io.ReadAll 读取整个响应体到 []byte
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应体失败: %w", err)
	}

	// 解析响应 JSON：顶层是一个 map
	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("解析响应 JSON 失败: %w", err)
	}

	// DeepSeek 响应的 content 字段是一个数组，每个元素有 type 和 text
	// 结构：{"content": [{"type": "text", "text": "回复内容"}, ...]}
	contentList, ok := result["content"].([]any)
	if !ok || len(contentList) == 0 {
		return "", fmt.Errorf("响应中没有 content 字段")
	}

	// 取第一个 type=text 的内容块
	for _, item := range contentList {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if block["type"] == "text" {
			text, _ := block["text"].(string)
			if text != "" {
				return text, nil
			}
		}
	}

	return "", fmt.Errorf("响应中没有找到 text 内容")
}
