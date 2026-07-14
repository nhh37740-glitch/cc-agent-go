package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"cc-agent-go/democode/v5/config"
	"cc-agent-go/democode/v5/model"
)

// ChatStream 发送流式请求到 DeepSeek，通过 onToken 回调逐个推送 token
//
// 参数:
//   - messages: 对话历史（和 Chat 一样的 Message 切片）
//   - systemPrompt: 系统提示词
//   - cfg: API 配置
//   - onToken: 每收到一个文本 token 就调用一次，例如 onToken("你好")
//
// 返回值: 累积的完整回复文本 + error
//
// 和 Chat 的区别:
//   - 请求体多一个 "stream": true
//   - 不调用 io.ReadAll 一次性读响应体，而是用 bufio.Scanner 逐行读
//   - 每行是一个 SSE 事件，格式为 "data: <JSON>\n\n"
func ChatStream(messages []model.Message, systemPrompt string, cfg config.Config,
	onToken func(string)) (string, error) {

	// 构建请求体 —— 和 Chat() 一样，只多了 "stream": true
	body := map[string]any{
		"model":      cfg.Model,
		"max_tokens": 4096,
		"system":     systemPrompt,
		"messages":   messages,
		"stream":     true, // ← v4 新增：告诉 DeepSeek 用 SSE 流式返回
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("序列化请求体失败: %w", err)
	}

	req, err := http.NewRequest("POST", cfg.ApiEndpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", cfg.ApiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("API 请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 非 200 时读取错误响应体
	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API 返回 %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// bufio.Scanner 逐行读取响应流
	// Scanner 是 Go 的高效逐行读取器，内部复用缓冲区，等价 Java BufferedReader.readLine()
	scanner := bufio.NewScanner(resp.Body)
	// 增大缓冲区：默认 64KB，设为 1MB 以应对 v6 的 tool_calls 长 JSON 行
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	// strings.Builder 高效累积完整回复
	// 等价 Java StringBuilder，内部用 []byte 累积，最后 String() 一次性转换
	var fullText strings.Builder

	// scanner.Scan() 返回 true 表示成功读取到下一行
	// 返回 false 表示 EOF 或出错
	for scanner.Scan() {
		line := scanner.Text()

		// 跳过空行和非 data 行（SSE 协议中还有 event: 开头的行，忽略）
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}

		// 剥离 "data:" 前缀，处理 "data:" 和 "data: " 两种情况
		dataJSON := line[5:] // 跳过 "data:" 共 5 字节
		if len(dataJSON) > 0 && dataJSON[0] == ' ' {
			dataJSON = dataJSON[1:] // 跳过紧随的空格
		}

		// 解析 data 行的 JSON
		var event map[string]any
		if err := json.Unmarshal([]byte(dataJSON), &event); err != nil {
			// 解析失败跳过该行
			continue
		}

		eventType, _ := event["type"].(string)

		switch eventType {
		case "content_block_delta":
			// 这是最常见的 token 事件
			// 结构: {"type":"content_block_delta","delta":{"type":"text_delta","text":"你好"}}
			delta, ok := event["delta"].(map[string]any)
			if !ok {
				continue
			}
			deltaType, _ := delta["type"].(string)
			if deltaType == "text_delta" {
				text, _ := delta["text"].(string)
				if text != "" {
					fullText.WriteString(text) // 累积完整回复
					onToken(text)              // 回调通知调用方
				}
			}
			// 注：v6 会增加 input_json_delta 处理，用于流式 tool_calls

		case "message_stop":
			// 流正常结束，跳出扫描循环
			goto done
		}
	}

done:
	// scanner.Err() 返回非 EOF 的扫描错误（如网络断开）
	if err := scanner.Err(); err != nil {
		return fullText.String(), fmt.Errorf("读取流失败: %w", err)
	}

	return fullText.String(), nil
}
