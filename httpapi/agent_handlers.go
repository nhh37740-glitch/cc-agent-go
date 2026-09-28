package httpapi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"cc-agent-go/agent"
	"cc-agent-go/config"
	"cc-agent-go/service"
	"cc-agent-go/tool"
)

const systemPromptBase = `你是一个 AI 助手。
当用户要求创建文件、读文件、改文件时，优先调用 file 工具。
当用户要求执行本机程序、搜索代码、运行 go/git 时，调用 command 工具（program + args 数组，不是 shell 字符串）。
需要旧会话信息时，按照本次 system message 提供的会话文件位置读取必要片段。
如果 activate_skill 工具列出的技能和用户需求匹配，先调用 activate_skill。`

const generalSubAgentToolName = service.GeneralSubAgentToolName

const generalSubAgentToolDescription = `同时运行一个或多个临时通用 SubAgent。
每个 subAgentTasks 元素必须包含唯一 taskId、完整任务和 maximumRounds。
根据任务需要选择 maximumRounds：简单任务使用较小数值，浏览器搜索等多步骤任务使用较大数值。
只提交彼此独立、可以同时执行的任务。`

// RunSubAgentToolInput 是 run_subagent 工具参数解包后的固定结构。
type RunSubAgentToolInput struct {
	SubAgentTasks []service.SubAgentTask `json:"subAgentTasks"`
}

// StartedSubAgentTask 是 run_subagent 已经启动的一项后台任务。
type StartedSubAgentTask struct {
	TaskID string `json:"taskId"`
	Status string `json:"status"`
}

// RunSubAgentToolOutput 是 run_subagent 立即返回给主 Agent 的固定 JSON 结构。
type RunSubAgentToolOutput struct {
	MaximumParallelSubAgents int                   `json:"maximumParallelSubAgents"`
	Tasks                    []StartedSubAgentTask `json:"tasks"`
}

type BackgroundAgentReplyStartedEvent struct {
	Type      string `json:"type"`
	MessageID string `json:"messageId"`
}

type BackgroundAgentReplyTokenEvent struct {
	Type      string `json:"type"`
	MessageID string `json:"messageId"`
	Token     string `json:"token"`
}

type BackgroundAgentReplyCompletedEvent struct {
	Type      string `json:"type"`
	MessageID string `json:"messageId"`
	Text      string `json:"text"`
}

type BackgroundAgentReplyFailedEvent struct {
	Type      string `json:"type"`
	MessageID string `json:"messageId"`
	Message   string `json:"message"`
}

func (server *Server) buildGeneralSubAgentToolInputSchema(
	maximumParallelSubAgents int,
	maximumSubAgentRounds int,
) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"subAgentTasks": map[string]any{
				"type":     "array",
				"minItems": 1,
				"maxItems": maximumParallelSubAgents,
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"taskId": map[string]any{
							"type":        "string",
							"description": "本次工具调用中唯一的任务编号",
						},
						"task": map[string]any{
							"type":        "string",
							"description": "SubAgent 完成任务需要的全部信息",
						},
						"maximumRounds": map[string]any{
							"type":        "integer",
							"minimum":     1,
							"maximum":     maximumSubAgentRounds,
							"description": "这个 SubAgent 最多执行多少轮 DeepSeek 和工具处理",
						},
					},
					"required": []string{
						"taskId",
						"task",
						"maximumRounds",
					},
				},
			},
		},
		"required": []string{"subAgentTasks"},
	}
}

func (server *Server) decodeAndValidateRunSubAgentToolInput(
	toolArguments map[string]any,
	maximumParallelSubAgents int,
	maximumSubAgentRounds int,
) (RunSubAgentToolInput, error) {
	var runSubAgentToolInput RunSubAgentToolInput

	runSubAgentToolInputJSONBytes, encodeToolArgumentsError :=
		json.Marshal(toolArguments)
	if encodeToolArgumentsError != nil {
		return runSubAgentToolInput, fmt.Errorf(
			"run_subagent 参数无法编码为 JSON: %w",
			encodeToolArgumentsError,
		)
	}

	decodeToolArgumentsError := json.Unmarshal(
		runSubAgentToolInputJSONBytes,
		&runSubAgentToolInput,
	)
	if decodeToolArgumentsError != nil {
		return runSubAgentToolInput, fmt.Errorf(
			"run_subagent 参数 JSON 无法解包: %w",
			decodeToolArgumentsError,
		)
	}

	if len(runSubAgentToolInput.SubAgentTasks) == 0 {
		return runSubAgentToolInput, fmt.Errorf(
			"run_subagent 的 subAgentTasks 至少需要 1 个任务",
		)
	}
	if len(runSubAgentToolInput.SubAgentTasks) > maximumParallelSubAgents {
		return runSubAgentToolInput, fmt.Errorf(
			"run_subagent 收到 %d 个任务，当前配置最多允许 %d 个",
			len(runSubAgentToolInput.SubAgentTasks),
			maximumParallelSubAgents,
		)
	}

	seenSubAgentTaskIDs := make(map[string]bool)
	for taskIndex, subAgentTask := range runSubAgentToolInput.SubAgentTasks {
		trimmedTaskID := strings.TrimSpace(subAgentTask.TaskID)
		if trimmedTaskID == "" {
			return runSubAgentToolInput, fmt.Errorf(
				"run_subagent 的第 %d 个任务缺少非空 taskId",
				taskIndex+1,
			)
		}
		if strings.TrimSpace(subAgentTask.Task) == "" {
			return runSubAgentToolInput, fmt.Errorf(
				"run_subagent 的第 %d 个任务缺少非空 task",
				taskIndex+1,
			)
		}
		if subAgentTask.MaximumRounds < 1 {
			return runSubAgentToolInput, fmt.Errorf(
				"run_subagent 的第 %d 个任务 maximumRounds 必须大于 0",
				taskIndex+1,
			)
		}
		if subAgentTask.MaximumRounds > maximumSubAgentRounds {
			return runSubAgentToolInput, fmt.Errorf(
				"run_subagent 的第 %d 个任务 maximumRounds=%d，当前配置最多允许 %d 轮",
				taskIndex+1,
				subAgentTask.MaximumRounds,
				maximumSubAgentRounds,
			)
		}
		if seenSubAgentTaskIDs[trimmedTaskID] {
			return runSubAgentToolInput, fmt.Errorf(
				"run_subagent 的 taskId %q 重复",
				trimmedTaskID,
			)
		}
		seenSubAgentTaskIDs[trimmedTaskID] = true
	}

	return runSubAgentToolInput, nil
}

func (server *Server) registerGeneralSubAgentTool(
	mainAgentToolRegistry *tool.Registry,
	parentConversationID string,
	completedResultsCallback service.CompletedSubAgentResultsCallback,
) error {
	return server.registerGeneralSubAgentToolForConfig(mainAgentToolRegistry,
		parentConversationID, completedResultsCallback, server.loadConfig())
}

func (server *Server) registerGeneralSubAgentToolForConfig(
	mainAgentToolRegistry *tool.Registry,
	parentConversationID string,
	completedResultsCallback service.CompletedSubAgentResultsCallback,
	applicationConfig config.Config,
) error {

	executeRunSubAgentTool := func(
		toolArguments map[string]any,
		toolExecutionEnvironment tool.ToolExecutionEnvironment,
	) (string, error) {
		runSubAgentToolInput, decodeToolArgumentsError :=
			server.decodeAndValidateRunSubAgentToolInput(
				toolArguments,
				applicationConfig.MaximumParallelSubAgents,
				applicationConfig.MaximumSubAgentRounds,
			)
		if decodeToolArgumentsError != nil {
			return "", decodeToolArgumentsError
		}

		availableSubAgentTools :=
			mainAgentToolRegistry.CopyExcludingTools(generalSubAgentToolName)
		service.RunSubAgentsInBackground(
			parentConversationID,
			runSubAgentToolInput.SubAgentTasks,
			applicationConfig,
			availableSubAgentTools,
			completedResultsCallback,
			service.SubAgentHostEnvironment{
				WorkingDirectory:         toolExecutionEnvironment.WorkingDirectory,
				ConversationStore:        server.projectConversationStore,
				TokenCounter:             server.tokenCounter,
				ModelContextWindowTokens: server.modelContextWindowTokens,
			},
		)

		startedSubAgentTasks := make(
			[]StartedSubAgentTask,
			0,
			len(runSubAgentToolInput.SubAgentTasks),
		)
		for _, startedSubAgentTask := range runSubAgentToolInput.SubAgentTasks {
			startedSubAgentTasks = append(
				startedSubAgentTasks,
				StartedSubAgentTask{
					TaskID: startedSubAgentTask.TaskID,
					Status: "running",
				},
			)
		}
		runSubAgentToolOutput := RunSubAgentToolOutput{
			MaximumParallelSubAgents: applicationConfig.MaximumParallelSubAgents,
			Tasks:                    startedSubAgentTasks,
		}
		runSubAgentToolOutputJSONBytes, encodeToolOutputError :=
			json.Marshal(runSubAgentToolOutput)
		if encodeToolOutputError != nil {
			return "", fmt.Errorf(
				"run_subagent 返回值无法编码为 JSON: %w",
				encodeToolOutputError,
			)
		}

		return string(runSubAgentToolOutputJSONBytes), nil
	}

	return mainAgentToolRegistry.RegisterTerminalFunctionTool(
		generalSubAgentToolName,
		generalSubAgentToolDescription,
		server.buildGeneralSubAgentToolInputSchema(
			applicationConfig.MaximumParallelSubAgents,
			applicationConfig.MaximumSubAgentRounds,
		),
		executeRunSubAgentTool,
	)
}

func (server *Server) sendEncodedConversationEvent(
	conversationID string,
	conversationEventJSON []byte,
) {
	server.conversationEventReceivers.SendEventJSON(
		conversationID,
		conversationEventJSON,
	)
}

func (server *Server) sendBackgroundAgentReplyStarted(
	conversationID string,
	backgroundReplyStartedEvent BackgroundAgentReplyStartedEvent,
) {
	conversationEventJSON, encodeConversationEventError :=
		json.Marshal(backgroundReplyStartedEvent)
	if encodeConversationEventError != nil {
		slog.Error(
			"后台回复开始事件无法编码为 JSON",
			"component", "conversation_events",
			"operation", "sendBackgroundAgentReplyStarted",
			"conversation_id", conversationID,
			"error_kind", service.ErrorInternal,
			"error", encodeConversationEventError,
		)
		return
	}
	server.sendEncodedConversationEvent(conversationID, conversationEventJSON)
}

func (server *Server) sendBackgroundAgentReplyToken(
	conversationID string,
	backgroundReplyTokenEvent BackgroundAgentReplyTokenEvent,
) {
	conversationEventJSON, encodeConversationEventError :=
		json.Marshal(backgroundReplyTokenEvent)
	if encodeConversationEventError != nil {
		slog.Error(
			"后台回复 token 事件无法编码为 JSON",
			"component", "conversation_events",
			"operation", "sendBackgroundAgentReplyToken",
			"conversation_id", conversationID,
			"error_kind", service.ErrorInternal,
			"error", encodeConversationEventError,
		)
		return
	}
	server.sendEncodedConversationEvent(conversationID, conversationEventJSON)
}

func (server *Server) sendBackgroundAgentReplyCompleted(
	conversationID string,
	backgroundReplyCompletedEvent BackgroundAgentReplyCompletedEvent,
) {
	conversationEventJSON, encodeConversationEventError :=
		json.Marshal(backgroundReplyCompletedEvent)
	if encodeConversationEventError != nil {
		slog.Error(
			"后台回复完成事件无法编码为 JSON",
			"component", "conversation_events",
			"operation", "sendBackgroundAgentReplyCompleted",
			"conversation_id", conversationID,
			"error_kind", service.ErrorInternal,
			"error", encodeConversationEventError,
		)
		return
	}
	server.sendEncodedConversationEvent(conversationID, conversationEventJSON)
}

func (server *Server) sendBackgroundAgentReplyFailed(
	conversationID string,
	backgroundReplyFailedEvent BackgroundAgentReplyFailedEvent,
) {
	conversationEventJSON, encodeConversationEventError :=
		json.Marshal(backgroundReplyFailedEvent)
	if encodeConversationEventError != nil {
		slog.Error(
			"后台回复失败事件无法编码为 JSON",
			"component", "conversation_events",
			"operation", "sendBackgroundAgentReplyFailed",
			"conversation_id", conversationID,
			"error_kind", service.ErrorInternal,
			"error", encodeConversationEventError,
		)
		return
	}
	server.sendEncodedConversationEvent(conversationID, conversationEventJSON)
}

func (server *Server) continueMainAgentAfterSubAgents(
	workingDirectory string,
	parentConversationID string,
	subAgentResults []service.SubAgentResult,
	runtimeScope string,
) {
	backgroundReplyMessageID := service.GenerateConversationId()
	server.sendBackgroundAgentReplyStarted(
		parentConversationID,
		BackgroundAgentReplyStartedEvent{
			Type:      "background_reply_started",
			MessageID: backgroundReplyMessageID,
		},
	)

	unlockConversationExecution :=
		server.conversationExecutionLocks.LockConversation(parentConversationID)
	defer unlockConversationExecution()

	applicationConfig := server.configForRuntimeScope(runtimeScope)
	backgroundReplyToolRegistry := tool.NewRegistry()
	internalContinuationTask, encodeSubAgentResultsError :=
		agent.EncodeInternalContinuationTask(subAgentResults)
	if encodeSubAgentResultsError != nil {
		server.sendBackgroundAgentReplyFailed(
			parentConversationID,
			BackgroundAgentReplyFailedEvent{
				Type:      "background_reply_failed",
				MessageID: backgroundReplyMessageID,
				Message:   "SubAgent 结果无法编码。",
			},
		)
		return
	}
	backgroundReplyResult, continueMainAgentError := service.RunAgentTask(
		internalContinuationTask,
		agent.AgentExecutionEnvironment{
			WorkingDirectory: workingDirectory,
			ConversationID:   parentConversationID,
		},
		systemPromptBase,
		applicationConfig,
		backgroundReplyToolRegistry,
		server.projectConversationStore,
		server.tokenCounter,
		server.modelContextWindowTokens,
		service.AgentRunOptions{
			KeepRecentMemoryTokens: applicationConfig.KeepRecentMemoryTokens,
			StreamText:             true,
			ReceiveEvent: func(agentEvent agent.AgentEvent) {
				textDeltaEvent, isTextDelta :=
					agentEvent.(agent.AgentTextDeltaEvent)
				if !isTextDelta {
					return
				}
				server.sendBackgroundAgentReplyToken(
					parentConversationID,
					BackgroundAgentReplyTokenEvent{
						Type:      "background_reply_token",
						MessageID: backgroundReplyMessageID,
						Token:     textDeltaEvent.Text,
					},
				)
			},
		},
	)
	if continueMainAgentError != nil {
		slog.Error(
			"SubAgent 完成后调用主 Agent失败",
			"component", "subagent_callback",
			"operation", "continueMainAgentAfterSubAgents",
			"conversation_id", parentConversationID,
			"error_kind", service.ErrorInternal,
			"error", continueMainAgentError,
		)
		server.sendBackgroundAgentReplyFailed(
			parentConversationID,
			BackgroundAgentReplyFailedEvent{
				Type:      "background_reply_failed",
				MessageID: backgroundReplyMessageID,
				Message:   "SubAgent 完成后调用主 Agent失败。",
			},
		)
		return
	}

	server.sendBackgroundAgentReplyCompleted(
		parentConversationID,
		BackgroundAgentReplyCompletedEvent{
			Type:      "background_reply_completed",
			MessageID: backgroundReplyMessageID,
			Text:      backgroundReplyResult.FinalText(),
		},
	)
}

func (server *Server) createConversationToolRegistry(
	parentConversationID string,
	workingDirectory string,
	applicationConfig config.Config,
	runtimeScope string,
) (*tool.Registry, error) {
	conversationToolRegistry :=
		server.registry.CopyExcludingTools(generalSubAgentToolName)
	registerGeneralSubAgentError := server.registerGeneralSubAgentToolForConfig(
		conversationToolRegistry,
		parentConversationID,
		func(
			completedParentConversationID string,
			subAgentResults []service.SubAgentResult,
		) {
			server.continueMainAgentAfterSubAgents(
				workingDirectory,
				completedParentConversationID,
				subAgentResults,
				runtimeScope,
			)
		},
		applicationConfig,
	)
	if registerGeneralSubAgentError != nil {
		return nil, registerGeneralSubAgentError
	}
	return conversationToolRegistry, nil
}
