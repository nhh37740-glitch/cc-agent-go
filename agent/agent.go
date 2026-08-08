package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"cc-agent-go/memory"
	"cc-agent-go/model"
	"cc-agent-go/tool"
)

type AgentConfiguration struct {
	ModelName                 string
	BaseSystemPrompt          string
	MaximumRounds             int
	MaximumOutputTokens       int
	MaximumToolResultTokens   int
	MaximumStoredMemoryTokens int
	ModelContextWindowTokens  int
}

type Agent struct {
	availableTools    *tool.Registry
	conversationStore *memory.ProjectConversationStore
	callModel         AgentModelCallFunction
	countTokens       AgentTokenCounter
	receiveAgentEvent AgentEventReceiver
	configuration     AgentConfiguration
}

func NewAgent(
	configuration AgentConfiguration,
	availableTools *tool.Registry,
	conversationStore *memory.ProjectConversationStore,
	callModel AgentModelCallFunction,
	countTokens AgentTokenCounter,
	receiveAgentEvent AgentEventReceiver,
) (*Agent, error) {
	if availableTools == nil {
		return nil, fmt.Errorf("availableTools 不能为空")
	}
	if conversationStore == nil {
		return nil, fmt.Errorf("conversationStore 不能为空")
	}
	if callModel == nil {
		return nil, fmt.Errorf("callModel 不能为空")
	}
	if countTokens == nil {
		return nil, fmt.Errorf("countTokens 不能为空")
	}
	if configuration.MaximumRounds < 1 {
		return nil, fmt.Errorf("MaximumRounds 必须大于 0")
	}
	if configuration.MaximumOutputTokens < 1 {
		return nil, fmt.Errorf("MaximumOutputTokens 必须大于 0")
	}
	if configuration.MaximumToolResultTokens < 1 {
		return nil, fmt.Errorf("MaximumToolResultTokens 必须大于 0")
	}
	if configuration.MaximumStoredMemoryTokens < 1 {
		return nil, fmt.Errorf("MaximumStoredMemoryTokens 必须大于 0")
	}
	if configuration.ModelContextWindowTokens <= configuration.MaximumOutputTokens {
		return nil, fmt.Errorf("ModelContextWindowTokens 必须大于 MaximumOutputTokens")
	}
	return &Agent{
		availableTools:    availableTools,
		conversationStore: conversationStore,
		callModel:         callModel,
		countTokens:       countTokens,
		receiveAgentEvent: receiveAgentEvent,
		configuration:     configuration,
	}, nil
}

func (configuredAgent *Agent) Run(
	agentTaskInput AgentTaskInput,
	executionEnvironment AgentExecutionEnvironment,
) (AgentRunResult, error) {
	if validateExecutionEnvironmentError := executionEnvironment.Validate(); validateExecutionEnvironmentError != nil {
		return nil, validateExecutionEnvironmentError
	}
	if agentTaskInput == nil || !validateAgentTaskInput(agentTaskInput) {
		return nil, fmt.Errorf("AgentTaskInput 缺少具体任务文字")
	}

	memoryReference, projectRules, buildMemoryReferenceError :=
		buildAgentMemoryReference(executionEnvironment, configuredAgent.conversationStore)
	if buildMemoryReferenceError != nil {
		return nil, buildMemoryReferenceError
	}
	configuredAgent.emit(AgentMemoryReferenceReadyEvent{
		ConversationID:          executionEnvironment.ConversationID,
		RelativeSessionFilePath: memoryReference.RelativeSessionFilePath,
	})
	systemPrompt := strings.TrimSpace(configuredAgent.configuration.BaseSystemPrompt)
	if projectRules != "" {
		systemPrompt += "\n\n项目 AGENTS.md：\n" + projectRules
	}
	systemPrompt += "\n\n" + memoryReference.SystemInstruction()
	currentRunMessages := []model.Message{{
		Role: "user",
		Content: []model.MessageContentBlock{
			model.TextContentBlock{Text: agentTaskInput.TaskText()},
		},
	}}
	toolExecutionEnvironment := tool.ToolExecutionEnvironment{
		WorkingDirectory: executionEnvironment.WorkingDirectory,
		ConversationID:   executionEnvironment.ConversationID,
	}
	var partialTextParts []string
	lastInputTokens := 0
	totalOutputTokens := 0
	hasExecutedToolCall := false

	for round := 1; round <= configuredAgent.configuration.MaximumRounds; round++ {
		toolDefinitions := configuredAgent.availableTools.GetDefinitions()
		isFinalResultRound := round == configuredAgent.configuration.MaximumRounds &&
			hasExecutedToolCall
		if isFinalResultRound {
			toolDefinitions = nil
			currentRunMessages = append(currentRunMessages, model.Message{
				Role: "user",
				Content: []model.MessageContentBlock{
					model.TextContentBlock{
						Text: "这是允许的最后一轮。不得再调用工具。请根据本次执行已经获得的内容，写出已完成结果和未完成事项。",
					},
				},
			})
		}
		preparedRequestTokens, countPreparedRequestError :=
			configuredAgent.countTokens.CountPreparedModelRequest(PreparedModelRequest{
				ModelName:           configuredAgent.configuration.ModelName,
				SystemPrompt:        systemPrompt,
				Messages:            currentRunMessages,
				ToolDefinitions:     toolDefinitions,
				MaximumOutputTokens: configuredAgent.configuration.MaximumOutputTokens,
			})
		if countPreparedRequestError != nil {
			return nil, countPreparedRequestError
		}
		if preparedRequestTokens+configuredAgent.configuration.MaximumOutputTokens >
			configuredAgent.configuration.ModelContextWindowTokens {
			messagesBeforeCompaction := len(currentRunMessages)
			compactedMessages, compactCurrentRunError :=
				configuredAgent.compactCurrentRun(currentRunMessages)
			if compactCurrentRunError != nil {
				return nil, compactCurrentRunError
			}
			currentRunMessages = compactedMessages
			configuredAgent.emit(AgentCurrentRunCompactedEvent{
				MessagesBefore: messagesBeforeCompaction,
				MessagesAfter:  len(currentRunMessages),
			})
			preparedRequestTokens, countPreparedRequestError =
				configuredAgent.countTokens.CountPreparedModelRequest(PreparedModelRequest{
					ModelName:           configuredAgent.configuration.ModelName,
					SystemPrompt:        systemPrompt,
					Messages:            currentRunMessages,
					ToolDefinitions:     toolDefinitions,
					MaximumOutputTokens: configuredAgent.configuration.MaximumOutputTokens,
				})
			if countPreparedRequestError != nil {
				return nil, countPreparedRequestError
			}
			if preparedRequestTokens+configuredAgent.configuration.MaximumOutputTokens >
				configuredAgent.configuration.ModelContextWindowTokens {
				return nil, fmt.Errorf(
					"agent_context_limit_reached: 压缩后请求仍需要 %d token，模型窗口为 %d",
					preparedRequestTokens+configuredAgent.configuration.MaximumOutputTokens,
					configuredAgent.configuration.ModelContextWindowTokens,
				)
			}
		}
		configuredAgent.emit(AgentRoundStartedEvent{
			Round:                 round,
			PreparedRequestTokens: preparedRequestTokens,
		})
		modelResponse, callModelError := configuredAgent.callModel(AgentModelCallRequest{
			SystemPrompt:        systemPrompt,
			Messages:            currentRunMessages,
			ToolDefinitions:     toolDefinitions,
			MaximumOutputTokens: configuredAgent.configuration.MaximumOutputTokens,
			ReceiveTextDelta: func(textDelta string) {
				configuredAgent.emit(AgentTextDeltaEvent{Text: textDelta})
			},
		})
		if callModelError != nil {
			return nil, fmt.Errorf("Agent 第 %d 轮模型调用失败: %w", round, callModelError)
		}
		lastInputTokens = modelResponse.InputTokens
		totalOutputTokens += modelResponse.OutputTokens
		if modelResponse.Text != "" {
			partialTextParts = append(partialTextParts, modelResponse.Text)
		}

		if len(modelResponse.ToolCalls) == 0 {
			memorySaveResult := configuredAgent.saveCompletedRun(
				agentTaskInput,
				executionEnvironment,
				modelResponse.Text,
				lastInputTokens,
				totalOutputTokens,
			)
			if isFinalResultRound {
				maximumRoundsResult := AgentMaximumRoundsReachedResult{
					PartialText:   modelResponse.Text,
					MaximumRounds: configuredAgent.configuration.MaximumRounds,
					MemorySave:    memorySaveResult,
				}
				configuredAgent.emit(AgentCompletedEvent{Result: maximumRoundsResult})
				return maximumRoundsResult, nil
			}
			completedResult := AgentCompletedResult{Text: modelResponse.Text, MemorySave: memorySaveResult}
			configuredAgent.emit(AgentCompletedEvent{Result: completedResult})
			return completedResult, nil
		}

		assistantToolCallMessage := model.Message{Role: "assistant"}
		if modelResponse.Text != "" {
			assistantToolCallMessage.Content = append(
				assistantToolCallMessage.Content,
				model.TextContentBlock{Text: modelResponse.Text},
			)
		}
		for _, returnedToolCall := range modelResponse.ToolCalls {
			hasExecutedToolCall = true
			assistantToolCallMessage.Content = append(
				assistantToolCallMessage.Content,
				model.ToolUseContentBlock{
					ID: returnedToolCall.ID, Name: returnedToolCall.Name, Input: returnedToolCall.Input,
				},
			)
		}
		currentRunMessages = append(currentRunMessages, assistantToolCallMessage)

		toolResultMessage := model.Message{Role: "user"}
		for _, returnedToolCall := range modelResponse.ToolCalls {
			configuredAgent.emit(AgentToolStartedEvent{
				Round: round, ToolUseID: returnedToolCall.ID, ToolName: returnedToolCall.Name,
			})
			toolResult, executeToolError := configuredAgent.availableTools.Execute(
				returnedToolCall.Name,
				returnedToolCall.Input,
				toolExecutionEnvironment,
			)
			if executeToolError != nil {
				configuredAgent.emit(AgentToolFailedEvent{
					Round: round, ToolUseID: returnedToolCall.ID,
					ToolName: returnedToolCall.Name, Cause: executeToolError,
				})
				toolResult = fmt.Sprintf("工具执行错误: %v", executeToolError)
			} else {
				configuredAgent.emit(AgentToolSucceededEvent{
					Round: round, ToolUseID: returnedToolCall.ID, ToolName: returnedToolCall.Name,
				})
			}
			toolResult, truncateToolResultError :=
				configuredAgent.truncateToolResult(toolResult)
			if truncateToolResultError != nil {
				return nil, truncateToolResultError
			}
			toolResultMessage.Content = append(
				toolResultMessage.Content,
				model.ToolResultContentBlock{
					ToolUseID: returnedToolCall.ID,
					Content:   toolResult,
				},
			)
			if executeToolError == nil &&
				configuredAgent.availableTools.IsTerminalTool(returnedToolCall.Name) {
				finalText := strings.TrimSpace(modelResponse.Text)
				if finalText != "" {
					finalText += "\n\n"
				}
				finalText += "SubAgent 已启动，完成后会自动返回结果。"
				memorySaveResult := configuredAgent.saveCompletedRun(
					agentTaskInput,
					executionEnvironment,
					finalText,
					lastInputTokens,
					totalOutputTokens,
				)
				terminalResult := AgentTerminalToolCompletedResult{
					Text: finalText, ToolName: returnedToolCall.Name,
					ToolResult: toolResult, MemorySave: memorySaveResult,
				}
				configuredAgent.emit(AgentCompletedEvent{Result: terminalResult})
				return terminalResult, nil
			}
		}
		currentRunMessages = append(currentRunMessages, toolResultMessage)
	}

	partialText := strings.Join(partialTextParts, "\n\n")
	memorySaveResult := configuredAgent.saveCompletedRun(
		agentTaskInput,
		executionEnvironment,
		partialText,
		lastInputTokens,
		totalOutputTokens,
	)
	maximumRoundsResult := AgentMaximumRoundsReachedResult{
		PartialText:   partialText,
		MaximumRounds: configuredAgent.configuration.MaximumRounds,
		MemorySave:    memorySaveResult,
	}
	configuredAgent.emit(AgentCompletedEvent{Result: maximumRoundsResult})
	return maximumRoundsResult, nil
}

func (configuredAgent *Agent) truncateToolResult(toolResult string) (string, error) {
	toolResultTokens, countToolResultError :=
		configuredAgent.countTokens.CountText(toolResult)
	if countToolResultError != nil {
		return "", countToolResultError
	}
	if toolResultTokens <= configuredAgent.configuration.MaximumToolResultTokens {
		return toolResult, nil
	}
	truncatedToolResult, truncateToolResultError :=
		configuredAgent.countTokens.TruncateText(
			toolResult,
			configuredAgent.configuration.MaximumToolResultTokens,
		)
	if truncateToolResultError != nil {
		return "", truncateToolResultError
	}
	return truncatedToolResult.Text +
		fmt.Sprintf(
			"\n\n[结果已按 token 截断：原始 %d token。请使用更具体的 rg、head 或 tail 命令读取必要片段。]",
			truncatedToolResult.OriginalTokens,
		), nil
}

func (configuredAgent *Agent) compactCurrentRun(
	currentRunMessages []model.Message,
) ([]model.Message, error) {
	if len(currentRunMessages) < 4 {
		return currentRunMessages, fmt.Errorf("当前任务本身超过模型上下文窗口，无法再压缩")
	}
	completedRoundMessages := currentRunMessages[1 : len(currentRunMessages)-2]
	completedRoundMessagesJSON, encodeCompletedRoundMessagesError :=
		json.Marshal(completedRoundMessages)
	if encodeCompletedRoundMessagesError != nil {
		return nil, fmt.Errorf("编码待压缩消息失败: %w", encodeCompletedRoundMessagesError)
	}
	maximumSummarySourceTokens :=
		configuredAgent.configuration.ModelContextWindowTokens -
			configuredAgent.configuration.MaximumOutputTokens -
			1000
	if maximumSummarySourceTokens <= 0 {
		return nil, fmt.Errorf("模型上下文窗口不足以执行当前消息压缩")
	}
	boundedSummarySource, truncateSummarySourceError :=
		configuredAgent.countTokens.TruncateText(
			string(completedRoundMessagesJSON),
			maximumSummarySourceTokens,
		)
	if truncateSummarySourceError != nil {
		return nil, fmt.Errorf("截断待压缩消息失败: %w", truncateSummarySourceError)
	}
	summaryResponse, summarizeCurrentRunError := configuredAgent.callModel(
		AgentModelCallRequest{
			SystemPrompt: "把已经完成的工具调用和结果压缩为事实摘要。保留文件名、数值、错误和未完成事项。",
			Messages: []model.Message{
				{
					Role: "user",
					Content: []model.MessageContentBlock{
						model.TextContentBlock{Text: boundedSummarySource.Text},
					},
				},
			},
			ToolDefinitions:     nil,
			MaximumOutputTokens: configuredAgent.configuration.MaximumOutputTokens,
		},
	)
	if summarizeCurrentRunError != nil {
		return nil, fmt.Errorf("压缩当前 Agent.Run 消息失败: %w", summarizeCurrentRunError)
	}
	compactedMessages := []model.Message{
		currentRunMessages[0],
		{
			Role: "assistant",
			Content: []model.MessageContentBlock{
				model.TextContentBlock{Text: "[本次执行较早步骤摘要] " + summaryResponse.Text},
			},
		},
	}
	compactedMessages = append(compactedMessages, currentRunMessages[len(currentRunMessages)-2:]...)
	return compactedMessages, nil
}

func (configuredAgent *Agent) saveCompletedRun(
	agentTaskInput AgentTaskInput,
	executionEnvironment AgentExecutionEnvironment,
	finalText string,
	lastInputTokens int,
	totalOutputTokens int,
) AgentMemorySaveResult {
	messagesToSave := []model.Message{}
	if agentTaskInput.ShouldSaveVisibleTask() {
		messagesToSave = append(messagesToSave, model.Message{
			Role: "user",
			Content: []model.MessageContentBlock{
				model.TextContentBlock{Text: agentTaskInput.TaskText()},
			},
		})
	}
	messagesToSave = append(messagesToSave, model.Message{
		Role: "assistant",
		Content: []model.MessageContentBlock{
			model.TextContentBlock{Text: finalText},
		},
	})
	_, appendConversationError := configuredAgent.conversationStore.AppendConversationTurn(
		executionEnvironment.WorkingDirectory,
		executionEnvironment.ConversationID,
		messagesToSave,
		lastInputTokens,
		totalOutputTokens,
		0,
		configuredAgent.configuration.ModelContextWindowTokens,
	)
	if appendConversationError != nil {
		configuredAgent.emit(AgentMemorySaveFailedEvent{Cause: appendConversationError})
		slog.Error("保存 Agent 项目会话失败",
			"component", "agent_memory",
			"operation", "ProjectConversationStore.AppendConversationTurn",
			"conversation_id", executionEnvironment.ConversationID,
			"error", appendConversationError,
		)
		return AgentMemorySaveFailed{Cause: appendConversationError}
	}
	sessionJSON, readSessionJSONError := configuredAgent.conversationStore.ReadSessionJSON(
		executionEnvironment.WorkingDirectory,
		executionEnvironment.ConversationID,
	)
	if readSessionJSONError != nil {
		configuredAgent.emit(AgentMemorySaveFailedEvent{Cause: readSessionJSONError})
		return AgentMemorySaveFailed{Cause: readSessionJSONError}
	}
	storedMemoryTokens, countStoredMemoryError :=
		configuredAgent.countTokens.CountText(string(sessionJSON))
	if countStoredMemoryError != nil {
		configuredAgent.emit(AgentMemorySaveFailedEvent{Cause: countStoredMemoryError})
		return AgentMemorySaveFailed{Cause: countStoredMemoryError}
	}
	if storedMemoryTokens > configuredAgent.configuration.MaximumStoredMemoryTokens {
		compressedTokens, compressStoredMemoryError :=
			configuredAgent.compressStoredMemory(executionEnvironment)
		if compressStoredMemoryError != nil {
			configuredAgent.emit(AgentStoredMemoryCompressionFailedEvent{
				Cause: compressStoredMemoryError,
			})
		} else {
			configuredAgent.emit(AgentStoredMemoryCompressedEvent{
				TokensBefore: storedMemoryTokens,
				TokensAfter:  compressedTokens,
			})
			storedMemoryTokens = compressedTokens
		}
	}
	if saveStoredMemoryTokensError :=
		configuredAgent.conversationStore.SaveStoredMemoryTokens(
			executionEnvironment.WorkingDirectory,
			executionEnvironment.ConversationID,
			storedMemoryTokens,
		); saveStoredMemoryTokensError != nil {
		configuredAgent.emit(AgentMemorySaveFailedEvent{Cause: saveStoredMemoryTokensError})
		return AgentMemorySaveFailed{Cause: saveStoredMemoryTokensError}
	}
	sessionJSONWithStoredTokenField, readFinalSessionJSONError :=
		configuredAgent.conversationStore.ReadSessionJSON(
			executionEnvironment.WorkingDirectory,
			executionEnvironment.ConversationID,
		)
	if readFinalSessionJSONError != nil {
		return AgentMemorySaveFailed{Cause: readFinalSessionJSONError}
	}
	recountedStoredMemoryTokens, recountStoredMemoryError :=
		configuredAgent.countTokens.CountText(string(sessionJSONWithStoredTokenField))
	if recountStoredMemoryError != nil {
		return AgentMemorySaveFailed{Cause: recountStoredMemoryError}
	}
	if recountedStoredMemoryTokens != storedMemoryTokens {
		if saveRecountedTokensError :=
			configuredAgent.conversationStore.SaveStoredMemoryTokens(
				executionEnvironment.WorkingDirectory,
				executionEnvironment.ConversationID,
				recountedStoredMemoryTokens,
			); saveRecountedTokensError != nil {
			return AgentMemorySaveFailed{Cause: saveRecountedTokensError}
		}
		storedMemoryTokens = recountedStoredMemoryTokens
	}
	return AgentMemorySaved{StoredMemoryTokens: storedMemoryTokens}
}

func (configuredAgent *Agent) compressStoredMemory(
	executionEnvironment AgentExecutionEnvironment,
) (int, error) {
	conversation, loadConversationError := configuredAgent.conversationStore.LoadConversation(
		executionEnvironment.WorkingDirectory,
		executionEnvironment.ConversationID,
	)
	if loadConversationError != nil {
		return 0, loadConversationError
	}
	if conversation == nil || len(conversation.Messages) < 5 {
		return 0, fmt.Errorf("会话超过记忆 token 上限，但没有可归档的旧消息")
	}
	recentMessageStart := len(conversation.Messages) - 4
	oldMessages := conversation.Messages[:recentMessageStart]
	summaryResponse, summarizeMemoryError := configuredAgent.callModel(AgentModelCallRequest{
		SystemPrompt:        "压缩历史会话。保留用户要求、已完成结果、具体文件、数值、错误和未完成事项。",
		Messages:            oldMessages,
		ToolDefinitions:     nil,
		MaximumOutputTokens: configuredAgent.configuration.MaximumOutputTokens,
	})
	if summarizeMemoryError != nil {
		return 0, summarizeMemoryError
	}
	if archiveMessagesError := configuredAgent.conversationStore.ArchiveMessages(
		executionEnvironment.WorkingDirectory,
		executionEnvironment.ConversationID,
		oldMessages,
	); archiveMessagesError != nil {
		return 0, archiveMessagesError
	}
	conversation.Messages = append(
		[]model.Message{{
			Role: "assistant",
			Content: []model.MessageContentBlock{
				model.TextContentBlock{Text: "[历史会话摘要] " + summaryResponse.Text},
			},
		}},
		conversation.Messages[recentMessageStart:]...,
	)
	if saveCompressedConversationError := configuredAgent.conversationStore.SaveConversation(
		executionEnvironment.WorkingDirectory,
		conversation,
	); saveCompressedConversationError != nil {
		return 0, saveCompressedConversationError
	}
	compressedSessionJSON, readCompressedSessionError :=
		configuredAgent.conversationStore.ReadSessionJSON(
			executionEnvironment.WorkingDirectory,
			executionEnvironment.ConversationID,
		)
	if readCompressedSessionError != nil {
		return 0, readCompressedSessionError
	}
	compressedTokens, countCompressedMemoryError :=
		configuredAgent.countTokens.CountText(string(compressedSessionJSON))
	if countCompressedMemoryError != nil {
		return 0, countCompressedMemoryError
	}
	return compressedTokens, nil
}

func (configuredAgent *Agent) emit(agentEvent AgentEvent) {
	if configuredAgent.receiveAgentEvent != nil {
		configuredAgent.receiveAgentEvent(agentEvent)
	}
}

func EncodeInternalContinuationTask(subAgentResults any) (InternalContinuationTaskInput, error) {
	subAgentResultsJSON, encodeSubAgentResultsError := json.Marshal(subAgentResults)
	if encodeSubAgentResultsError != nil {
		return InternalContinuationTaskInput{}, encodeSubAgentResultsError
	}
	return InternalContinuationTaskInput{
		ContinuationInstruction: "此前启动的 SubAgent 已经完成。只分析和汇总下面的结果；" +
			"如果任务未完成，明确写出已有结果和未完成内容。\n\n" +
			string(subAgentResultsJSON),
	}, nil
}
