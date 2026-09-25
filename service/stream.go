package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"cc-agent-go/config"
	"cc-agent-go/model"
)

// ChatStream 发送流式请求到 DeepSeek，通过 onToken 回调实时推送 token，
// 返回 *ApiResponse（完整文本 + 工具调用 + token 用量）。
// requestContext 取消时中断 HTTP 读取，并尽量返回已收到的部分正文。
func ChatStream(
	requestContext context.Context,
	messages []model.Message,
	systemPrompt string,
	cfg config.Config,
	tools []map[string]any,
	maxTokens int,
	onToken func(string),
) (*model.ApiResponse, error) {
	if requestContext == nil {
		requestContext = context.Background()
	}
	if strings.TrimSpace(cfg.ApiKey) == "" {
		return nil, NewAppError(ErrorConfig, "service.ChatStream", 0,
			fmt.Errorf("DEEPSEEK_API_KEY 未设置"))
	}

	body := map[string]any{
		"model":      cfg.Model,
		"max_tokens": maxTokens,
		"system":     systemPrompt,
		"messages":   messages,
		"stream":     true,
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, NewAppError(ErrorInternal, "service.ChatStream.marshal", 0, err)
	}

	req, err := http.NewRequestWithContext(
		requestContext,
		"POST",
		cfg.ApiEndpoint,
		bytes.NewReader(jsonBody),
	)
	if err != nil {
		return nil, NewAppError(ErrorConfig, "service.ChatStream.newRequest", 0, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", cfg.ApiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, newNetworkError("service.ChatStream.do", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, NewAppError(ErrorProviderResponseInvalid,
				"service.ChatStream.readErrorResponse", resp.StatusCode, readErr)
		}
		return nil, newProviderError("service.ChatStream.provider", resp.StatusCode, bodyBytes, cfg.ApiKey)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var fullText strings.Builder
	var toolCalls []model.ToolCall
	toolBlocks := make(map[int]*toolAccumulator)

	// v7 新增：从 SSE 事件中提取 token 用量
	inputTokens := 0
	outputTokens := 0

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
		// v7 新增：message_start 事件携带 input_tokens
		case "message_start":
			if msg, ok := event["message"].(map[string]any); ok {
				if usage, ok := msg["usage"].(map[string]any); ok {
					if it, ok := usage["input_tokens"].(float64); ok {
						inputTokens = int(it)
					}
				}
			}

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

		// v7 新增：message_delta 事件携带 output_tokens
		case "message_delta":
			if usage, ok := event["usage"].(map[string]any); ok {
				if ot, ok := usage["output_tokens"].(float64); ok {
					outputTokens = int(ot)
				}
			}

		case "message_stop":
			goto done
		}
	}

done:
	partialResponse := &model.ApiResponse{
		Text:         fullText.String(),
		ToolCalls:    toolCalls,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, context.Canceled) ||
			errors.Is(requestContext.Err(), context.Canceled) {
			return partialResponse, context.Canceled
		}
		return partialResponse, newNetworkError("service.ChatStream.readStream", err)
	}
	if errors.Is(requestContext.Err(), context.Canceled) {
		return partialResponse, context.Canceled
	}
	return partialResponse, nil
}

type toolAccumulator struct {
	id      string
	name    string
	jsonBuf strings.Builder
}

func intOrZero(v any) int {
	f, _ := v.(float64)
	return int(f)
}
