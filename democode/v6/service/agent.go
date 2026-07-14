package service

import (
	"fmt"

	"cc-agent-go/democode/v6/config"
	"cc-agent-go/democode/v6/model"
	"cc-agent-go/democode/v6/tool"
)

const maxRounds = 50       // 最大工具调用轮数，和 Java 版一致
const maxToolResult = 8000 // 工具结果最大字符数，超长截断

// Run 执行 Agent 循环：用户消息 → API（带 tools）→ 模型回复 → 有 tool_calls?
//   - 否：返回文本
//   - 是：执行工具 → 结果追加到 history → 回到 API 调用
//
// 最多执行 maxRounds 轮，防止无限循环。
func Run(userMessage string, systemPrompt string, cfg config.Config,
	registry *tool.Registry) (string, error) {

	// 构建初始 history：只有用户消息
	history := []model.Message{
		{
			Role: "user",
			Content: []model.ContentBlock{
				{Type: "text", Text: userMessage},
			},
		},
	}

	tools := registry.GetDefinitions()
	fmt.Printf("[Agent] 收到消息: %s, 工具数: %d\n", userMessage, len(tools))

	// Agent 循环
	for round := 0; round < maxRounds; round++ {
		text, toolCalls, err := Chat(history, systemPrompt, cfg, tools)
		if err != nil {
			return "", fmt.Errorf("第 %d 轮 API 调用失败: %w", round+1, err)
		}

		// 没有工具调用 → 模型给了最终回复
		if len(toolCalls) == 0 {
			return text, nil
		}

		// 构建 assistant 消息：包含模型说的文本 + tool_use 块
		assistantMsg := model.Message{Role: "assistant"}
		if text != "" {
			assistantMsg.Content = append(assistantMsg.Content,
				model.ContentBlock{Type: "text", Text: text})
		}
		for _, tc := range toolCalls {
			assistantMsg.Content = append(assistantMsg.Content,
				model.ContentBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Name,
					Input: tc.Input,
				})
		}
		history = append(history, assistantMsg)

		// 执行每个工具，工具结果打包成 user 消息
		userMsg := model.Message{Role: "user"}
		for _, tc := range toolCalls {
			result, execErr := registry.Execute(tc.Name, tc.Input)
			if execErr != nil {
				result = fmt.Sprintf("工具执行错误: %v", execErr)
			}
			// 超长结果截断，防止撑爆上下文
			if len(result) > maxToolResult {
				result = result[:maxToolResult] + fmt.Sprintf(
					"\n\n[结果过长，已截断。原始长度 %d 字符，显示前 %d 字符]",
					len(result), maxToolResult)
			}
			userMsg.Content = append(userMsg.Content,
				model.ContentBlock{
					Type:      "tool_result",
					ToolUseID: tc.ID,
					Content:   result,
				})
		}
		history = append(history, userMsg)
	}

	return "", fmt.Errorf("达到最大工具调用轮数 %d，模型仍未给出最终回复", maxRounds)
}
