package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"cc-agent-go/democode/v6.5/config"
	"cc-agent-go/democode/v6.5/model"
)

func ChatStream(messages []model.Message, systemPrompt string, cfg config.Config,
	tools []map[string]any, onToken func(string)) (string, []model.ToolCall, error) {

	body := map[string]any{
		"model":      cfg.Model,
		"max_tokens": 4096,
		"system":     systemPrompt,
		"messages":   messages,
		"stream":     true,
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

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

	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", nil, fmt.Errorf("API 返回 %d: %s", resp.StatusCode, string(bodyBytes))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var fullText strings.Builder
	var toolCalls []model.ToolCall
	toolBlocks := make(map[int]*toolAccumulator)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		dataJSON := line[5:]
		if len(dataJSON) > 0 && dataJSON[0] == ' ' {
			dataJSON = dataJSON[1:]
		}

		var event map[string]any
		if err := json.Unmarshal([]byte(dataJSON), &event); err != nil {
			continue
		}

		eventType, _ := event["type"].(string)

		switch eventType {
		case "content_block_start":
			block, ok := event["content_block"].(map[string]any)
			if !ok {
				continue
			}
			if block["type"] == "tool_use" {
				idx := intOrZero(event["index"])
				toolBlocks[idx] = &toolAccumulator{
					id:   stringOrEmpty(block["id"]),
					name: stringOrEmpty(block["name"]),
				}
			}

		case "content_block_delta":
			delta, ok := event["delta"].(map[string]any)
			if !ok {
				continue
			}
			deltaType, _ := delta["type"].(string)
			switch deltaType {
			case "text_delta":
				text, _ := delta["text"].(string)
				if text != "" {
					fullText.WriteString(text)
					onToken(text)
				}
			case "input_json_delta":
				idx := intOrZero(event["index"])
				if acc, ok := toolBlocks[idx]; ok {
					acc.jsonBuf.WriteString(stringOrEmpty(delta["partial_json"]))
				}
			}

		case "content_block_stop":
			idx := intOrZero(event["index"])
			if acc, ok := toolBlocks[idx]; ok {
				var input map[string]any
				if err := json.Unmarshal([]byte(acc.jsonBuf.String()), &input); err == nil {
					toolCalls = append(toolCalls, model.ToolCall{
						ID:    acc.id,
						Name:  acc.name,
						Input: input,
					})
				}
				delete(toolBlocks, idx)
			}

		case "message_stop":
			goto done
		}
	}

done:
	if err := scanner.Err(); err != nil {
		return fullText.String(), toolCalls, fmt.Errorf("读取流失败: %w", err)
	}
	return fullText.String(), toolCalls, nil
}

type toolAccumulator struct {
	id     string
	name   string
	jsonBuf strings.Builder
}

func intOrZero(v any) int {
	f, _ := v.(float64)
	return int(f)
}
