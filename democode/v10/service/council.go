package service

import (
	"fmt"
	"sort"

	"cc-agent-go/democode/v10/config"
	"cc-agent-go/democode/v10/model"
)

// Speech 单条发言记录。
type Speech struct {
	Round int    `json:"round"`
	Agent string `json:"agent"`
	Text  string `json:"text"`
}

// CouncilRequest 前端发来的元老院请求体。
type CouncilRequest struct {
	Topic        string `json:"topic"`
	MaxRounds    int    `json:"maxRounds"`
	Interruption string `json:"interruption,omitempty"`
}

// CouncilResponse 非流式元老院响应体。
type CouncilResponse struct {
	Topic       string   `json:"topic"`
	Rounds      int      `json:"rounds"`
	Transcript  []Speech `json:"transcript"`
	TotalTokens int      `json:"totalTokens"`
}

// RunCouncil 执行辩论循环。
//
// 参数:
//   - topic: 议题文本
//   - maxRounds: 最多辩论轮数（一轮 = 每个 agent 各说一次）
//   - interruption: 用户插话内容（空字符串 = 无插话）
//   - personalities: map[agent名]人格systemPrompt
//   - cfg: API 配置
//
// 返回值: 发言记录、累计 token 数、error
func RunCouncil(topic string, maxRounds int, interruption string,
	personalities map[string]string, cfg config.Config) ([]Speech, int, error) {

	if maxRounds < 1 {
		maxRounds = 3
	}

	// 按 agent 名排序，保证每轮发言顺序一致
	names := make([]string, 0, len(personalities))
	for name := range personalities {
		names = append(names, name)
	}
	sort.Strings(names)

	// 初始化 history：议题作为首条用户消息
	history := []model.Message{
		{Role: "user", Content: []model.ContentBlock{
			{Type: "text", Text: "【元老院议题】" + topic},
		}},
	}

	// 插话
	if interruption != "" {
		history = append(history, model.Message{
			Role: "user",
			Content: []model.ContentBlock{
				{Type: "text", Text: "【公民插话】" + interruption},
			},
		})
	}

	var transcript []Speech
	totalTokens := 0

	for round := 1; round <= maxRounds; round++ {
		for _, name := range names {
			systemPrompt := personalities[name]

			// 深拷贝 history（Chat 可能修改 messages）
			messages := make([]model.Message, len(history))
			copy(messages, history)

			resp, err := Chat(messages, systemPrompt, cfg, nil, 99120)
			if err != nil {
				return transcript, totalTokens, fmt.Errorf("第 %d 轮 %s 发言失败: %w", round, name, err)
			}

			totalTokens += resp.InputTokens + resp.OutputTokens

			speechText := resp.Text

			// 发言追加到 history（供后续 agent 看到）
			history = append(history, model.Message{
				Role: "assistant",
				Content: []model.ContentBlock{
					{Type: "text", Text: "【" + name + "】" + speechText},
				},
			})

			transcript = append(transcript, Speech{
				Round: round,
				Agent: name,
				Text:  speechText,
			})
		}
	}

	return transcript, totalTokens, nil
}
