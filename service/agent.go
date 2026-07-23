package service

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"cc-agent-go/config"
	"cc-agent-go/model"
	"cc-agent-go/tool"
)

const maxRounds = 50          // 最大工具调用轮数，和 Java 版一致
const maxToolResult = 8000    // 工具结果最大字符数，超长截断
const defaultMaxTokens = 4096 // 默认 API max_tokens
const subAgentStartedReply = "SubAgent 已启动，完成后会自动返回结果。"

// Run 执行 Agent 循环（非流式）。v7 新增会话持久化：
//   - 从 store 加载历史消息，实现跨请求的上下文延续
//   - 循环结束后保存本轮所有消息到 store
//   - 累计 token 数超过阈值时触发 LLM 压缩
//
// 参数:
//   - userMessage: 用户输入
//   - conversationId: 会话 ID（空字符串时自动生成 UUID）
//   - systemPrompt: 系统提示词
//   - cfg: API 配置
//   - registry: 工具注册表
//   - store: 会话存储
//
// 返回值: (AI回复, 实际使用的conversationId, error)
func Run(userMessage string, conversationId string, systemPrompt string,
	cfg config.Config, registry *tool.Registry, store *Store) (string, string, error) {

	// 空 conversationId → 生成新 UUID
	if conversationId == "" {
		conversationId = GenerateConversationId()
	}

	// 加载历史消息（新会话返回空切片）
	oldMessages, err := store.LoadMessages(conversationId)
	if err != nil {
		return "", conversationId, NewAppError(ErrorStorageRead,
			"store.LoadMessages", 0, err)
	}

	// 构建本轮用户消息
	userMsg := model.Message{
		Role: "user",
		Content: []model.MessageContentBlock{
			model.TextContentBlock{Text: userMessage},
		},
	}

	// 历史 + 本轮用户消息 = 完整 history
	history := append(oldMessages, userMsg)

	tools := registry.GetDefinitions()
	slog.Info("Agent 非流式请求开始",
		"component", "agent",
		"operation", "service.Run",
		"conversation_id", conversationId,
		"history_messages", len(oldMessages),
		"tool_count", len(tools))

	// 记录本轮新增的消息（用于保存）
	var newMessages []model.Message
	newMessages = append(newMessages, userMsg)

	totalOutputTokens := 0

	// Agent 循环
	for round := 0; round < maxRounds; round++ {
		resp, err := Chat(history, systemPrompt, cfg, tools, defaultMaxTokens)
		if err != nil {
			// 出错时不保存（和 Java 一致：部分结果不持久化）
			return "", conversationId, fmt.Errorf("第 %d 轮 API 调用失败: %w", round+1, err)
		}

		totalOutputTokens += resp.OutputTokens

		slog.Info("Agent 模型调用完成",
			"component", "agent",
			"operation", "service.Run",
			"conversation_id", conversationId,
			"round", round+1,
			"text_length", len(resp.Text),
			"tool_call_count", len(resp.ToolCalls),
			"input_tokens", resp.InputTokens,
			"output_tokens", resp.OutputTokens)
		for _, tc := range resp.ToolCalls {
			slog.Info("Agent 请求执行工具",
				"component", "agent",
				"operation", "registry.Execute",
				"conversation_id", conversationId,
				"round", round+1,
				"tool_name", tc.Name)
		}

		// 没有工具调用 → 模型给了最终回复
		if len(resp.ToolCalls) == 0 {
			// 构建 assistant 文本消息
			assistantMsg := model.Message{Role: "assistant"}
			if resp.Text != "" {
				assistantMsg.Content = append(assistantMsg.Content,
					model.TextContentBlock{Text: resp.Text})
			}
			newMessages = append(newMessages, assistantMsg)

			// 保存本轮对话到文件
			slog.Info("保存 Agent 会话",
				"component", "storage",
				"operation", "store.AppendTurnWithCompression",
				"conversation_id", conversationId,
				"new_messages", len(newMessages),
				"output_tokens", totalOutputTokens)
			saveErr := store.AppendTurnWithCompression(
				conversationId, newMessages, totalOutputTokens,
				cfg.CompressionThreshold,
				makeCompressor(cfg, tools),
			)
			if saveErr != nil {
				slog.Error("保存 Agent 会话失败",
					"component", "storage",
					"operation", "store.AppendTurnWithCompression",
					"conversation_id", conversationId,
					"error_kind", ErrorStorageWrite,
					"error", saveErr)
			}

			return resp.Text, conversationId, nil
		}

		// 构建 assistant 消息：文本 + tool_use 块
		assistantMsg := model.Message{Role: "assistant"}
		if resp.Text != "" {
			assistantMsg.Content = append(assistantMsg.Content,
				model.TextContentBlock{Text: resp.Text})
		}
		for _, tc := range resp.ToolCalls {
			assistantMsg.Content = append(assistantMsg.Content,
				model.ToolUseContentBlock{
					ID:    tc.ID,
					Name:  tc.Name,
					Input: tc.Input,
				})
		}
		history = append(history, assistantMsg)
		// 工具调用消息只进 history（内部上下文），不进 newMessages

		// 执行每个工具，工具结果打包成 user 消息
		toolResultMsg := model.Message{Role: "user"}
		responseContainsRunSubAgent :=
			containsToolCall(resp.ToolCalls, GeneralSubAgentToolName)
		runSubAgentStartedSuccessfully := false
		for _, tc := range resp.ToolCalls {
			result := ""
			var execErr error
			if responseContainsRunSubAgent &&
				tc.Name != GeneralSubAgentToolName {
				result = "工具未执行：本轮已经调用 run_subagent，" +
					"主 Agent 只负责启动 SubAgent。"
			} else {
				result, execErr = registry.Execute(tc.Name, tc.Input)
				if tc.Name == GeneralSubAgentToolName && execErr == nil {
					runSubAgentStartedSuccessfully = true
				}
			}
			if execErr != nil {
				slog.Warn("工具执行失败，错误将交给下一轮模型调用",
					"component", "tool",
					"operation", "registry.Execute",
					"conversation_id", conversationId,
					"round", round+1,
					"tool_name", tc.Name,
					"error_kind", ErrorTool,
					"error", execErr)
				result = fmt.Sprintf("工具执行错误: %v", execErr)
			}
			// 超长结果截断，防止撑爆上下文
			if len(result) > maxToolResult {
				result = result[:maxToolResult] + fmt.Sprintf(
					"\n\n[结果过长，已截断。原始长度 %d 字符，显示前 %d 字符]",
					len(result), maxToolResult)
			}
			toolResultMsg.Content = append(toolResultMsg.Content,
				model.ToolResultContentBlock{
					ToolUseID: tc.ID,
					Content:   result,
				})
		}
		history = append(history, toolResultMsg)
		// 工具结果只进 history，不进 newMessages

		if runSubAgentStartedSuccessfully {
			mainAgentReply := resp.Text
			if mainAgentReply != "" {
				mainAgentReply += "\n\n"
			}
			mainAgentReply += subAgentStartedReply
			newMessages = append(
				newMessages,
				model.Message{
					Role: "assistant",
					Content: []model.MessageContentBlock{
						model.TextContentBlock{Text: mainAgentReply},
					},
				},
			)

			saveErr := store.AppendTurnWithCompression(
				conversationId, newMessages, totalOutputTokens,
				cfg.CompressionThreshold,
				makeCompressor(cfg, tools),
			)
			if saveErr != nil {
				slog.Error("保存 Agent 会话失败",
					"component", "storage",
					"operation", "store.AppendTurnWithCompression",
					"conversation_id", conversationId,
					"error_kind", ErrorStorageWrite,
					"error", saveErr)
			}

			return mainAgentReply, conversationId, nil
		}
	}

	return "", conversationId, NewAppError(ErrorAgentLimit,
		"service.Run", 0,
		fmt.Errorf("达到最大工具调用轮数 %d，模型仍未给出最终回复", maxRounds))
}

// RunStream 和 Run 逻辑一致，区别是用 ChatStream 替代 Chat，
// token 通过 onToken 回调实时推送给调用方（SSE），同时支持工具循环。
func RunStream(userMessage string, conversationId string, systemPrompt string,
	cfg config.Config, registry *tool.Registry, store *Store,
	onToken func(string)) (string, string, error) {

	if conversationId == "" {
		conversationId = GenerateConversationId()
	}

	userMessageForAgent := model.Message{
		Role: "user",
		Content: []model.MessageContentBlock{
			model.TextContentBlock{Text: userMessage},
		},
	}

	return runStreamWithInputMessage(
		userMessageForAgent,
		true,
		conversationId,
		systemPrompt,
		cfg,
		registry,
		store,
		onToken,
		"service.RunStream",
	)
}

// ContinueConversationAfterSubAgentsStream 读取主 Agent 最新会话，
// 把本批 SubAgent 结果作为本次 DeepSeek 输入，然后流式返回主 Agent 回复。
// SubAgent 结果输入不作为普通用户消息保存；最终 assistant 回复正常保存。
func ContinueConversationAfterSubAgentsStream(
	subAgentResults []SubAgentResult,
	conversationId string,
	systemPrompt string,
	cfg config.Config,
	_ *tool.Registry,
	store *Store,
	onToken func(string),
) (string, string, error) {
	subAgentResultsJSON, encodeSubAgentResultsError :=
		json.Marshal(subAgentResults)
	if encodeSubAgentResultsError != nil {
		return "", conversationId, fmt.Errorf(
			"SubAgent 结果无法编码为 JSON: %w",
			encodeSubAgentResultsError,
		)
	}

	subAgentResultsForMainAgent := model.Message{
		Role: "user",
		Content: []model.MessageContentBlock{
			model.TextContentBlock{
				Text: "你之前启动的一批 SubAgent 已经执行完毕。" +
					"你现在只能分析和汇总下面的执行结果，不能调用工具，" +
					"不能继续执行 SubAgent 尚未完成的工作。" +
					"如果 status 是 limit_reached 或 failed，" +
					"必须明确说明已经获得的结果和仍未完成的内容。" +
					"不要要求用户再次询问结果。\n\n" +
					string(subAgentResultsJSON),
			},
		},
	}

	return runStreamWithInputMessage(
		subAgentResultsForMainAgent,
		false,
		conversationId,
		systemPrompt,
		cfg,
		tool.NewRegistry(),
		store,
		onToken,
		"service.ContinueConversationAfterSubAgentsStream",
	)
}

func runStreamWithInputMessage(
	inputMessage model.Message,
	saveInputMessage bool,
	conversationId string,
	systemPrompt string,
	cfg config.Config,
	registry *tool.Registry,
	store *Store,
	onToken func(string),
	operationName string,
) (string, string, error) {
	oldMessages, err := store.LoadMessages(conversationId)
	if err != nil {
		return "", conversationId, NewAppError(ErrorStorageRead,
			"store.LoadMessages", 0, err)
	}
	slog.Info("Agent 流式请求开始",
		"component", "agent",
		"operation", operationName,
		"conversation_id", conversationId,
		"history_messages", len(oldMessages))

	history := append(oldMessages, inputMessage)

	var newMessages []model.Message
	if saveInputMessage {
		newMessages = append(newMessages, inputMessage)
	}

	tools := registry.GetDefinitions()
	totalOutputTokens := 0

	for round := 0; round < maxRounds; round++ {
		resp, err := ChatStream(history, systemPrompt, cfg, tools, defaultMaxTokens, onToken)
		if err != nil {
			return "", conversationId, fmt.Errorf("第 %d 轮 API 调用失败: %w", round+1, err)
		}

		totalOutputTokens += resp.OutputTokens

		slog.Info("Agent 流式模型调用完成",
			"component", "agent",
			"operation", operationName,
			"conversation_id", conversationId,
			"round", round+1,
			"text_length", len(resp.Text),
			"tool_call_count", len(resp.ToolCalls),
			"input_tokens", resp.InputTokens,
			"output_tokens", resp.OutputTokens)
		for _, tc := range resp.ToolCalls {
			slog.Info("Agent 请求执行工具",
				"component", "agent",
				"operation", "registry.Execute",
				"conversation_id", conversationId,
				"round", round+1,
				"tool_name", tc.Name)
		}

		if len(resp.ToolCalls) == 0 {
			assistantMsg := model.Message{Role: "assistant"}
			if resp.Text != "" {
				assistantMsg.Content = append(assistantMsg.Content,
					model.TextContentBlock{Text: resp.Text})
			}
			newMessages = append(newMessages, assistantMsg)

			saveErr := store.AppendTurnWithCompression(
				conversationId, newMessages, totalOutputTokens,
				cfg.CompressionThreshold,
				makeCompressor(cfg, tools),
			)
			if saveErr != nil {
				slog.Error("保存 Agent 会话失败",
					"component", "storage",
					"operation", "store.AppendTurnWithCompression",
					"conversation_id", conversationId,
					"error_kind", ErrorStorageWrite,
					"error", saveErr)
			}

			return resp.Text, conversationId, nil
		}

		assistantMsg := model.Message{Role: "assistant"}
		if resp.Text != "" {
			assistantMsg.Content = append(assistantMsg.Content,
				model.TextContentBlock{Text: resp.Text})
		}
		for _, tc := range resp.ToolCalls {
			assistantMsg.Content = append(assistantMsg.Content,
				model.ToolUseContentBlock{
					ID:    tc.ID,
					Name:  tc.Name,
					Input: tc.Input,
				})
		}
		history = append(history, assistantMsg)
		// 工具调用消息只进 history（内部上下文），不进 newMessages

		toolResultMsg := model.Message{Role: "user"}
		responseContainsRunSubAgent :=
			containsToolCall(resp.ToolCalls, GeneralSubAgentToolName)
		runSubAgentStartedSuccessfully := false
		for _, tc := range resp.ToolCalls {
			result := ""
			var execErr error
			if responseContainsRunSubAgent &&
				tc.Name != GeneralSubAgentToolName {
				result = "工具未执行：本轮已经调用 run_subagent，" +
					"主 Agent 只负责启动 SubAgent。"
			} else {
				result, execErr = registry.Execute(tc.Name, tc.Input)
				if tc.Name == GeneralSubAgentToolName && execErr == nil {
					runSubAgentStartedSuccessfully = true
				}
			}
			if execErr != nil {
				slog.Warn("工具执行失败，错误将交给下一轮模型调用",
					"component", "tool",
					"operation", "registry.Execute",
					"conversation_id", conversationId,
					"round", round+1,
					"tool_name", tc.Name,
					"error_kind", ErrorTool,
					"error", execErr)
				result = fmt.Sprintf("工具执行错误: %v", execErr)
			}
			if len(result) > maxToolResult {
				result = result[:maxToolResult] + fmt.Sprintf(
					"\n\n[结果过长，已截断。原始长度 %d 字符，显示前 %d 字符]",
					len(result), maxToolResult)
			}
			toolResultMsg.Content = append(toolResultMsg.Content,
				model.ToolResultContentBlock{
					ToolUseID: tc.ID,
					Content:   result,
				})
		}
		history = append(history, toolResultMsg)
		// 工具结果只进 history，不进 newMessages

		if runSubAgentStartedSuccessfully {
			streamedReplySuffix := subAgentStartedReply
			mainAgentReply := resp.Text
			if mainAgentReply != "" {
				mainAgentReply += "\n\n"
				streamedReplySuffix = "\n\n" + streamedReplySuffix
			}
			mainAgentReply += subAgentStartedReply
			onToken(streamedReplySuffix)
			newMessages = append(
				newMessages,
				model.Message{
					Role: "assistant",
					Content: []model.MessageContentBlock{
						model.TextContentBlock{Text: mainAgentReply},
					},
				},
			)

			saveErr := store.AppendTurnWithCompression(
				conversationId, newMessages, totalOutputTokens,
				cfg.CompressionThreshold,
				makeCompressor(cfg, tools),
			)
			if saveErr != nil {
				slog.Error("保存 Agent 会话失败",
					"component", "storage",
					"operation", "store.AppendTurnWithCompression",
					"conversation_id", conversationId,
					"error_kind", ErrorStorageWrite,
					"error", saveErr)
			}

			return mainAgentReply, conversationId, nil
		}
	}

	return "", conversationId, NewAppError(ErrorAgentLimit,
		operationName, 0,
		fmt.Errorf("达到最大工具调用轮数 %d", maxRounds))
}

func containsToolCall(
	toolCalls []model.ToolCall,
	expectedToolName string,
) bool {
	for _, toolCall := range toolCalls {
		if toolCall.Name == expectedToolName {
			return true
		}
	}
	return false
}

// ========================================================================
// UUID 生成
// ========================================================================

// GenerateConversationId 生成 v4 UUID 格式的会话 ID。
// 使用 crypto/rand（密码学安全随机源），而非 math/rand（伪随机）。
// 输出格式: xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx
// 和 Java UUID.randomUUID().toString() 格式一致。
//
// 位运算说明:
//
//	b[6] = (b[6] & 0x0f) | 0x40  → 高 4 位清零后设为 0100（版本 4）
//	b[8] = (b[8] & 0x3f) | 0x80  → 高 2 位清零后设为 10（变体 1）
func GenerateConversationId() string {
	b := make([]byte, 16)
	rand.Read(b) // crypto/rand.Read 填充随机字节

	// UUID v4: 第 7 字节高 4 位 = 4
	b[6] = (b[6] & 0x0f) | 0x40
	// UUID variant: 第 9 字节高 2 位 = 10
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ========================================================================
// 压缩器
// ========================================================================

// makeCompressor 创建压缩函数：把旧消息序列化为文本，调 LLM 生成摘要。
// 摘要指令和 Java cc-agent-java BuildCompressor 一致。
// 返回的 Compressor 闭包捕获了 cfg 和 tools。
func makeCompressor(cfg config.Config, tools []map[string]any) model.Compressor {
	return func(oldMessages []model.Message) (string, error) {
		// 序列化旧消息为文本
		var sb strings.Builder
		for _, msg := range oldMessages {
			sb.WriteString("[" + msg.Role + "]: ")
			for _, messageContentBlock := range msg.Content {
				switch contentBlock := messageContentBlock.(type) {
				case model.TextContentBlock:
					sb.WriteString(contentBlock.Text)
				case model.ToolUseContentBlock:
					sb.WriteString(fmt.Sprintf("[调用工具 %s]", contentBlock.Name))
				case model.ToolResultContentBlock:
					// 工具结果可能很长，截断
					result := contentBlock.Content
					if len(result) > 500 {
						result = result[:500] + "..."
					}
					sb.WriteString(fmt.Sprintf("[工具结果: %s]", result))
				}
			}
			sb.WriteString("\n")
		}

		summaryRequest := []model.Message{{
			Role: "user",
			Content: []model.MessageContentBlock{
				model.TextContentBlock{
					Text: "请用一段话总结以下对话内容，保留所有关键事实、工具调用结果、用户偏好。最多500字。\n\n" + sb.String(),
				},
			},
		}}

		// 调 Chat，max_tokens=512，不带工具
		resp, err := Chat(summaryRequest, "", cfg, nil, 512)
		if err != nil {
			return "", fmt.Errorf("压缩 LLM 调用失败: %w", err)
		}
		return resp.Text, nil
	}
}
