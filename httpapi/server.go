package httpapi

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"cc-agent-go/agent"
	"cc-agent-go/config"
	"cc-agent-go/harness"
	"cc-agent-go/mcp"
	"cc-agent-go/memory"
	"cc-agent-go/model"
	"cc-agent-go/service"
	"cc-agent-go/tool"
)

type Dependencies struct {
	LoadConfig                 func() config.Config
	Personalities              map[string]string
	Registry                   *tool.Registry
	MCPServerManager           *mcp.MCPServerManager
	ConversationEventReceivers *service.ConversationEventReceivers
	ConversationExecutionLocks *service.ConversationExecutionLocks
	ConversationRunRegistry    *service.ConversationRunRegistry
	ProjectConversationStore   *memory.ProjectConversationStore
	TokenCounter               agent.AgentTokenCounter
	ModelContextWindowTokens   int
	HarnessPrompts             harness.Prompts
	HarnessPromptsLoadError    error
	ApplicationLogFilePath     string
}

type Server struct {
	loadConfig                 func() config.Config
	personalities              map[string]string
	registry                   *tool.Registry
	mcpServerManager           *mcp.MCPServerManager
	conversationEventReceivers *service.ConversationEventReceivers
	conversationExecutionLocks *service.ConversationExecutionLocks
	conversationRunRegistry    *service.ConversationRunRegistry
	projectConversationStore   *memory.ProjectConversationStore
	tokenCounter               agent.AgentTokenCounter
	modelContextWindowTokens   int
	harnessPrompts             harness.Prompts
	harnessPromptsLoadError    error
	applicationLogFilePath     string
}

func NewServer(dependencies Dependencies) *Server {
	if dependencies.LoadConfig == nil {
		dependencies.LoadConfig = config.Load
	}
	if dependencies.Registry == nil {
		dependencies.Registry = tool.NewRegistry()
	}
	if dependencies.ConversationEventReceivers == nil {
		dependencies.ConversationEventReceivers = service.NewConversationEventReceivers()
	}
	if dependencies.ConversationExecutionLocks == nil {
		dependencies.ConversationExecutionLocks = service.NewConversationExecutionLocks()
	}
	if dependencies.ConversationRunRegistry == nil {
		dependencies.ConversationRunRegistry = service.NewConversationRunRegistry()
	}
	if dependencies.ProjectConversationStore == nil {
		dependencies.ProjectConversationStore = memory.NewProjectConversationStore()
	}
	if dependencies.ApplicationLogFilePath == "" {
		dependencies.ApplicationLogFilePath = "logs/server.jsonl"
	}
	return &Server{
		loadConfig:                 dependencies.LoadConfig,
		personalities:              dependencies.Personalities,
		registry:                   dependencies.Registry,
		mcpServerManager:           dependencies.MCPServerManager,
		conversationEventReceivers: dependencies.ConversationEventReceivers,
		conversationExecutionLocks: dependencies.ConversationExecutionLocks,
		conversationRunRegistry:    dependencies.ConversationRunRegistry,
		projectConversationStore:   dependencies.ProjectConversationStore,
		tokenCounter:               dependencies.TokenCounter,
		modelContextWindowTokens:   dependencies.ModelContextWindowTokens,
		harnessPrompts:             dependencies.HarnessPrompts,
		harnessPromptsLoadError:    dependencies.HarnessPromptsLoadError,
		applicationLogFilePath:     dependencies.ApplicationLogFilePath,
	}
}

func (server *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/chat", server.handleChat)
	mux.HandleFunc("POST /api/chat/stream", server.handleChatStream)
	mux.HandleFunc("GET /api/mcp/servers", server.handleListMCPServers)
	mux.HandleFunc("PUT /api/mcp/servers", server.handleSelectMCPServers)
	mux.HandleFunc("GET /api/conversations", server.handleListConversations)
	mux.HandleFunc("GET /api/conversations/{id}/events", server.handleConversationEvents)
	mux.HandleFunc("GET /api/conversations/{id}", server.handleGetConversation)
	mux.HandleFunc("POST /api/conversations/{id}/stop", server.handleStopConversation)
	mux.HandleFunc("DELETE /api/conversations/{id}", server.handleDeleteConversation)
	mux.HandleFunc("GET /api/logs", server.handleListRecentApplicationLogs)
	mux.HandleFunc("POST /api/council", server.handleCouncil)
	mux.HandleFunc("POST /api/council/stream", server.handleCouncilStream)
	mux.HandleFunc("POST /api/harness/chat/stream", server.handleHarnessChatStream)
	mux.HandleFunc("GET /api/harness/agents", server.handleHarnessAgents)
	mux.HandleFunc("GET /api/harness/agents/{name}/memory", server.handleHarnessAgentMemory)
	mux.HandleFunc("POST /api/harness/agents/{name}/heartbeat", server.handleHarnessAgentHeartbeat)
	mux.HandleFunc("GET /harness", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "harness.html")
	})
	mux.HandleFunc("GET /council", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "council.html")
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "index.html")
	})
	return mux
}

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
	applicationConfig := server.loadConfig()

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

	applicationConfig := server.loadConfig()
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
) (*tool.Registry, error) {
	conversationToolRegistry :=
		server.registry.CopyExcludingTools(generalSubAgentToolName)
	registerGeneralSubAgentError := server.registerGeneralSubAgentTool(
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
			)
		},
	)
	if registerGeneralSubAgentError != nil {
		return nil, registerGeneralSubAgentError
	}
	return conversationToolRegistry, nil
}

func (server *Server) publicError(err error) (int, model.ErrorResponse) {
	status := http.StatusInternalServerError
	resp := model.ErrorResponse{
		Code:    string(service.ErrorInternal),
		Message: "服务内部错误。",
	}

	var appErr *service.AppError
	if errors.As(err, &appErr) {
		resp.Code = string(appErr.Kind)
		resp.ProviderStatus = appErr.ProviderStatus
		switch appErr.Kind {
		case service.ErrorInvalidRequest:
			status = http.StatusBadRequest
			resp.Message = "请求参数无效。"
		case service.ErrorConfig:
			status = http.StatusServiceUnavailable
			resp.Message = "服务端未配置 DEEPSEEK_API_KEY。"
		case service.ErrorProviderAuth:
			status = http.StatusBadGateway
			resp.Message = "DeepSeek API 鉴权失败，请检查服务端 DEEPSEEK_API_KEY。"
		case service.ErrorProviderRateLimit:
			status = http.StatusServiceUnavailable
			resp.Message = "DeepSeek 请求过于频繁，请稍后重试。"
		case service.ErrorProvider, service.ErrorProviderResponseInvalid:
			status = http.StatusBadGateway
			resp.Message = "DeepSeek 服务返回异常。"
		case service.ErrorNetwork:
			status = http.StatusBadGateway
			resp.Message = "无法连接 DeepSeek 服务。"
		case service.ErrorNetworkTimeout:
			status = http.StatusGatewayTimeout
			resp.Message = "连接 DeepSeek 服务超时。"
		case service.ErrorStorageRead:
			status = http.StatusInternalServerError
			resp.Message = "读取会话数据失败。"
		case service.ErrorStorageWrite:
			status = http.StatusInternalServerError
			resp.Message = "写入会话数据失败。"
		case service.ErrorAgentLimit:
			status = http.StatusInternalServerError
			resp.Message = "Agent 达到最大工具调用轮数。"
		}
		return status, resp
	}

	var mcpErr *mcp.Error
	if !errors.As(err, &mcpErr) {
		return status, resp
	}
	resp.Code = string(mcpErr.Kind)
	switch mcpErr.Kind {
	case mcp.ErrorServerNotFound:
		status = http.StatusBadRequest
		resp.Message = "选择的 MCP Server 不存在。"
	case mcp.ErrorConfigurationInvalid:
		status = http.StatusInternalServerError
		resp.Message = "MCP 配置文件无效。"
	case mcp.ErrorRequestTimeout:
		status = http.StatusGatewayTimeout
		resp.Message = "MCP Server 请求超时。"
	case mcp.ErrorServerStartFailed:
		status = http.StatusBadGateway
		resp.Message = "MCP Server 进程启动失败。"
	case mcp.ErrorInitializeFailed:
		status = http.StatusBadGateway
		resp.Message = "MCP Server 初始化失败。"
	case mcp.ErrorToolsNotSupported:
		status = http.StatusBadGateway
		resp.Message = "MCP Server 没有提供工具。"
	case mcp.ErrorToolListFailed:
		status = http.StatusBadGateway
		resp.Message = "读取 MCP Server 工具列表失败。"
	case mcp.ErrorProcessStopped:
		status = http.StatusBadGateway
		resp.Message = "MCP Server 进程已经停止。"
	case mcp.ErrorProtocolResponseInvalid:
		status = http.StatusBadGateway
		resp.Message = "MCP Server 返回了无效数据。"
	}
	return status, resp
}

func (server *Server) logAPIError(operation string, conversationID string, err error, resp model.ErrorResponse) {
	args := []any{
		"component", "http",
		"operation", operation,
		"error_kind", resp.Code,
		"error", err,
	}
	if conversationID != "" {
		args = append(args, "conversation_id", conversationID)
	}
	if resp.ProviderStatus != 0 {
		args = append(args, "provider_status", resp.ProviderStatus)
	}
	slog.Error("请求处理失败", args...)
}

func (server *Server) writeAPIError(w http.ResponseWriter, operation string, conversationID string, err error) {
	status, resp := server.publicError(err)
	server.logAPIError(operation, conversationID, err, resp)
	w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	w.WriteHeader(status)
	if encodeErr := json.NewEncoder(w).Encode(resp); encodeErr != nil {
		slog.Error("错误响应 JSON 写入失败",
			"component", "http",
			"operation", operation,
			"error_kind", service.ErrorInternal,
			"error", encodeErr)
	}
}

func (server *Server) writeSSEError(w http.ResponseWriter, flusher http.Flusher, operation string,
	conversationID string, err error) {
	_, resp := server.publicError(err)
	server.logAPIError(operation, conversationID, err, resp)
	frame := map[string]any{
		"type":    "error",
		"code":    resp.Code,
		"message": resp.Message,
	}
	if resp.ProviderStatus != 0 {
		frame["providerStatus"] = resp.ProviderStatus
	}
	data, marshalErr := json.Marshal(frame)
	if marshalErr != nil {
		slog.Error("SSE 错误事件序列化失败",
			"component", "http",
			"operation", operation,
			"error_kind", service.ErrorInternal,
			"error", marshalErr)
		return
	}
	if _, writeErr := fmt.Fprintf(w, "data: %s\n\n", data); writeErr != nil {
		slog.Error("SSE 错误事件写入失败",
			"component", "http",
			"operation", operation,
			"error_kind", service.ErrorInternal,
			"error", writeErr)
		return
	}
	flusher.Flush()
}

func (server *Server) invalidRequestError(operation string, err error) *service.AppError {
	return service.NewAppError(service.ErrorInvalidRequest, operation, 0, err)
}

// ============================================================================
// MCP Server 选择路由
// ============================================================================

type FrontendMCPServerSelectionJSON struct {
	SelectedMCPServerNames []string `json:"selectedServerNames"`
}

type MCPServerListJSON struct {
	Servers []mcp.MCPServerStatus `json:"servers"`
}

func (server *Server) handleListMCPServers(responseWriter http.ResponseWriter, httpRequest *http.Request) {
	if server.mcpServerManager == nil {
		server.writeAPIError(
			responseWriter,
			"handleListMCPServers",
			"",
			mcp.NewError(
				mcp.ErrorConfigurationInvalid,
				"handleListMCPServers",
				"",
				fmt.Errorf("MCPServerManager 尚未初始化"),
			),
		)
		return
	}

	responseWriter.Header().Set("Content-Type", "application/json;charset=UTF-8")
	if encodeServerListError := json.NewEncoder(responseWriter).Encode(MCPServerListJSON{
		Servers: server.mcpServerManager.ListConfiguredMCPServers(),
	}); encodeServerListError != nil {
		slog.Error("MCP Server 列表 JSON 写入失败",
			"component", "http",
			"operation", "handleListMCPServers",
			"error_kind", service.ErrorInternal,
			"error", encodeServerListError)
	}
}

func (server *Server) handleSelectMCPServers(responseWriter http.ResponseWriter, httpRequest *http.Request) {
	var frontendMCPServerSelectionJSON FrontendMCPServerSelectionJSON
	if decodeSelectionError := json.NewDecoder(httpRequest.Body).Decode(
		&frontendMCPServerSelectionJSON,
	); decodeSelectionError != nil {
		server.writeAPIError(
			responseWriter,
			"handleSelectMCPServers.decodeSelection",
			"",
			server.invalidRequestError(
				"handleSelectMCPServers.decodeSelection",
				decodeSelectionError,
			),
		)
		return
	}
	if server.mcpServerManager == nil {
		server.writeAPIError(
			responseWriter,
			"handleSelectMCPServers",
			"",
			mcp.NewError(
				mcp.ErrorConfigurationInvalid,
				"handleSelectMCPServers",
				"",
				fmt.Errorf("MCPServerManager 尚未初始化"),
			),
		)
		return
	}

	mcpServerStatuses, applySelectionError :=
		server.mcpServerManager.StartSelectedMCPServers(
			httpRequest.Context(),
			frontendMCPServerSelectionJSON.SelectedMCPServerNames,
			server.registry,
		)
	if applySelectionError != nil {
		server.writeAPIError(
			responseWriter,
			"handleSelectMCPServers.startSelectedMCPServers",
			"",
			applySelectionError,
		)
		return
	}

	responseWriter.Header().Set("Content-Type", "application/json;charset=UTF-8")
	if encodeServerStatusesError := json.NewEncoder(responseWriter).Encode(MCPServerListJSON{
		Servers: mcpServerStatuses,
	}); encodeServerStatusesError != nil {
		slog.Error("MCP Server 选择结果 JSON 写入失败",
			"component", "http",
			"operation", "handleSelectMCPServers",
			"error_kind", service.ErrorInternal,
			"error", encodeServerStatusesError)
	}
}

// ============================================================================
// Chat 路由
// ============================================================================

func (server *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var webAgentTaskRequest model.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&webAgentTaskRequest); err != nil {
		server.writeAPIError(w, "handleChat.decode", "",
			server.invalidRequestError("handleChat.decode", err))
		return
	}
	server.assignWebAgentConversationID(&webAgentTaskRequest)
	if validateWebAgentTaskError := server.validateWebAgentTaskRequest(
		webAgentTaskRequest,
	); validateWebAgentTaskError != nil {
		server.writeAPIError(w, "handleChat.validate", webAgentTaskRequest.ConversationId,
			server.invalidRequestError("handleChat.validate", validateWebAgentTaskError))
		return
	}
	cfg := server.loadConfig()
	if strings.TrimSpace(cfg.ApiKey) == "" {
		server.writeAPIError(w, "handleChat.config", webAgentTaskRequest.ConversationId,
			service.NewAppError(service.ErrorConfig, "handleChat.config", 0,
				fmt.Errorf("DEEPSEEK_API_KEY 未配置")))
		return
	}
	conversationID := webAgentTaskRequest.ConversationId
	conversationToolRegistry, createToolRegistryError :=
		server.createConversationToolRegistry(
			conversationID,
			webAgentTaskRequest.WorkingDirectory,
		)
	if createToolRegistryError != nil {
		server.writeAPIError(
			w,
			"handleChat.createConversationToolRegistry",
			conversationID,
			createToolRegistryError,
		)
		return
	}

	unlockConversationExecution :=
		server.conversationExecutionLocks.LockConversation(conversationID)
	defer unlockConversationExecution()

	runContext, endConversationRun := server.conversationRunRegistry.BeginRun(
		conversationID,
		r.Context(),
	)
	defer endConversationRun()

	runResult, runAgentError := service.RunAgentTask(
		agent.UserTaskInput{Message: webAgentTaskRequest.Message},
		agent.AgentExecutionEnvironment{
			WorkingDirectory: webAgentTaskRequest.WorkingDirectory,
			ConversationID:   conversationID,
			Context:          runContext,
		},
		systemPromptBase,
		cfg,
		conversationToolRegistry,
		server.projectConversationStore,
		server.tokenCounter,
		server.modelContextWindowTokens,
		service.AgentRunOptions{
			Context:                runContext,
			KeepRecentMemoryTokens: cfg.KeepRecentMemoryTokens,
		},
	)
	if runAgentError != nil {
		server.writeAPIError(w, "handleChat.run", conversationID, runAgentError)
		return
	}
	resp := model.ChatResponse{
		ConversationId: conversationID,
		Reply:          runResult.FinalText(),
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("聊天响应 JSON 写入失败",
			"component", "http",
			"operation", "handleChat.encode",
			"conversation_id", conversationID,
			"error_kind", service.ErrorInternal,
			"error", err)
	}
}

func (server *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream;charset=UTF-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		server.writeAPIError(w, "handleChatStream.flusher", "",
			service.NewAppError(service.ErrorInternal, "handleChatStream.flusher", 0,
				fmt.Errorf("ResponseWriter 不支持 http.Flusher")))
		return
	}
	var webAgentTaskRequest model.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&webAgentTaskRequest); err != nil {
		server.writeSSEError(w, flusher, "handleChatStream.decode", "",
			server.invalidRequestError("handleChatStream.decode", err))
		return
	}

	server.assignWebAgentConversationID(&webAgentTaskRequest)
	if validateWebAgentTaskError := server.validateWebAgentTaskRequest(
		webAgentTaskRequest,
	); validateWebAgentTaskError != nil {
		server.writeSSEError(
			w,
			flusher,
			"handleChatStream.validate",
			webAgentTaskRequest.ConversationId,
			server.invalidRequestError("handleChatStream.validate", validateWebAgentTaskError),
		)
		return
	}
	conversationId := webAgentTaskRequest.ConversationId
	convIdJSON, _ := json.Marshal(map[string]string{
		"type":           "conversation_id",
		"conversationId": conversationId,
	})
	fmt.Fprintf(w, "data: %s\n\n", convIdJSON)
	flusher.Flush()

	cfg := server.loadConfig()
	if strings.TrimSpace(cfg.ApiKey) == "" {
		server.writeSSEError(w, flusher, "handleChatStream.config", conversationId,
			service.NewAppError(service.ErrorConfig, "handleChatStream.config", 0,
				fmt.Errorf("DEEPSEEK_API_KEY 未配置")))
		return
	}
	conversationToolRegistry, createToolRegistryError :=
		server.createConversationToolRegistry(
			conversationId,
			webAgentTaskRequest.WorkingDirectory,
		)
	if createToolRegistryError != nil {
		server.writeSSEError(
			w,
			flusher,
			"handleChatStream.createConversationToolRegistry",
			conversationId,
			createToolRegistryError,
		)
		return
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		unlockConversationExecution :=
			server.conversationExecutionLocks.LockConversation(conversationId)
		defer unlockConversationExecution()

		runContext, endConversationRun := server.conversationRunRegistry.BeginRun(
			conversationId,
			r.Context(),
		)
		defer endConversationRun()

		runResult, runAgentError := service.RunAgentTask(
			agent.UserTaskInput{Message: webAgentTaskRequest.Message},
			agent.AgentExecutionEnvironment{
				WorkingDirectory: webAgentTaskRequest.WorkingDirectory,
				ConversationID:   conversationId,
				Context:          runContext,
			},
			systemPromptBase,
			cfg,
			conversationToolRegistry,
			server.projectConversationStore,
			server.tokenCounter,
			server.modelContextWindowTokens,
			service.AgentRunOptions{
				Context:                runContext,
				KeepRecentMemoryTokens: cfg.KeepRecentMemoryTokens,
				StreamText:             true,
				ReceiveEvent: func(agentEvent agent.AgentEvent) {
					server.writeAgentEventSSE(w, flusher, agentEvent)
				},
			},
		)
		if runAgentError != nil {
			server.writeSSEError(w, flusher, "handleChatStream.run", conversationId, runAgentError)
			return
		}
		server.writeAgentRunFinishedSSE(w, flusher, runResult)
	}()
	<-done
}

func (server *Server) assignWebAgentConversationID(webAgentTaskRequest *model.ChatRequest) {
	webAgentTaskRequest.ConversationId =
		strings.TrimSpace(webAgentTaskRequest.ConversationId)
	if webAgentTaskRequest.ConversationId == "" {
		webAgentTaskRequest.ConversationId = service.GenerateConversationId()
	}
}

func (server *Server) writeAgentRunFinishedSSE(
	responseWriter http.ResponseWriter,
	responseWriterFlusher http.Flusher,
	runResult agent.AgentRunResult,
) {
	if runResult == nil {
		return
	}
	finishedEvent := map[string]any{
		"text": runResult.FinalText(),
	}
	if cancelledResult, isCancelled := runResult.(agent.AgentCancelledResult); isCancelled {
		finishedEvent["type"] = "cancelled"
		finishedEvent["reason"] = cancelledResult.Reason
		finishedEvent["partialText"] = cancelledResult.PartialText
	} else if runResult.FinalText() == "" {
		return
	} else {
		finishedEvent["type"] = "done"
	}
	finishedJSON, encodeFinishedError := json.Marshal(finishedEvent)
	if encodeFinishedError != nil {
		return
	}
	fmt.Fprintf(responseWriter, "data: %s\n\n", finishedJSON)
	responseWriterFlusher.Flush()
}

// handleStopConversation 取消指定会话当前正在执行的 Agent.Run。
// 路径：POST /api/conversations/{id}/stop
func (server *Server) handleStopConversation(
	responseWriter http.ResponseWriter,
	httpRequest *http.Request,
) {
	conversationID := strings.TrimSpace(httpRequest.PathValue("id"))
	if conversationID == "" {
		server.writeAPIError(responseWriter, "handleStopConversation.validate", "",
			server.invalidRequestError("handleStopConversation.validate",
				fmt.Errorf("conversationId 不能为空")))
		return
	}
	cancelled := server.conversationRunRegistry.CancelRun(conversationID)
	responseWriter.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(responseWriter).Encode(map[string]any{
		"conversationId": conversationID,
		"cancelled":      cancelled,
	})
}

func (server *Server) validateWebAgentTaskRequest(webAgentTaskRequest model.ChatRequest) error {
	if strings.TrimSpace(webAgentTaskRequest.Message) == "" {
		return fmt.Errorf("message 不能为空")
	}
	return (agent.AgentExecutionEnvironment{
		WorkingDirectory: webAgentTaskRequest.WorkingDirectory,
		ConversationID:   webAgentTaskRequest.ConversationId,
	}).Validate()
}

func (server *Server) writeAgentEventSSE(
	responseWriter http.ResponseWriter,
	responseWriterFlusher http.Flusher,
	receivedAgentEvent agent.AgentEvent,
) {
	eventJSONFields := map[string]any{}
	switch concreteAgentEvent := receivedAgentEvent.(type) {
	case agent.AgentMemoryReferenceReadyEvent:
		eventJSONFields = map[string]any{
			"type":           "memory_reference_ready",
			"conversationId": concreteAgentEvent.ConversationID,
			"sessionFile":    concreteAgentEvent.RelativeSessionFilePath,
		}
	case agent.AgentRoundStartedEvent:
		eventJSONFields = map[string]any{
			"type":                  "round_started",
			"round":                 concreteAgentEvent.Round,
			"preparedRequestTokens": concreteAgentEvent.PreparedRequestTokens,
		}
	case agent.AgentTextDeltaEvent:
		eventJSONFields = map[string]any{
			"type": "text_delta",
			"text": concreteAgentEvent.Text,
		}
	case agent.AgentToolStartedEvent:
		eventJSONFields = map[string]any{
			"type":      "tool_started",
			"round":     concreteAgentEvent.Round,
			"toolUseId": concreteAgentEvent.ToolUseID,
			"toolName":  concreteAgentEvent.ToolName,
		}
	case agent.AgentToolSucceededEvent:
		eventJSONFields = map[string]any{
			"type":      "tool_succeeded",
			"round":     concreteAgentEvent.Round,
			"toolUseId": concreteAgentEvent.ToolUseID,
			"toolName":  concreteAgentEvent.ToolName,
		}
	case agent.AgentToolFailedEvent:
		eventJSONFields = map[string]any{
			"type":      "tool_failed",
			"round":     concreteAgentEvent.Round,
			"toolUseId": concreteAgentEvent.ToolUseID,
			"toolName":  concreteAgentEvent.ToolName,
			"message":   concreteAgentEvent.Cause.Error(),
		}
	case agent.AgentCurrentRunCompactedEvent:
		eventJSONFields = map[string]any{
			"type":           "current_run_compacted",
			"messagesBefore": concreteAgentEvent.MessagesBefore,
			"messagesAfter":  concreteAgentEvent.MessagesAfter,
		}
	case agent.AgentStoredMemoryCompressedEvent:
		eventJSONFields = map[string]any{
			"type":         "stored_memory_compressed",
			"tokensBefore": concreteAgentEvent.TokensBefore,
			"tokensAfter":  concreteAgentEvent.TokensAfter,
		}
	case agent.AgentStoredMemoryCompressionFailedEvent:
		eventJSONFields = map[string]any{
			"type":    "stored_memory_compression_failed",
			"message": concreteAgentEvent.Cause.Error(),
		}
	case agent.AgentMemorySaveFailedEvent:
		eventJSONFields = map[string]any{
			"type":    "memory_save_failed",
			"message": concreteAgentEvent.Cause.Error(),
		}
	case agent.AgentCancelledEvent:
		eventJSONFields = map[string]any{
			"type":        "cancelled",
			"text":        concreteAgentEvent.PartialText,
			"reason":      concreteAgentEvent.Reason,
			"partialText": concreteAgentEvent.PartialText,
		}
	case agent.AgentCompletedEvent:
		eventJSONFields = map[string]any{
			"type": "agent_completed",
			"text": concreteAgentEvent.Result.FinalText(),
		}
		if cancelledResult, isCancelled := concreteAgentEvent.Result.(agent.AgentCancelledResult); isCancelled {
			eventJSONFields["resultKind"] = "cancelled"
			eventJSONFields["reason"] = cancelledResult.Reason
		}
	default:
		return
	}
	encodedAgentEvent, encodeAgentEventError := json.Marshal(eventJSONFields)
	if encodeAgentEventError != nil {
		return
	}
	fmt.Fprintf(responseWriter, "data: %s\n\n", encodedAgentEvent)
	responseWriterFlusher.Flush()
}

// ============================================================================
// Harness 路由
// ============================================================================

// buildHarnessRuntimeDependencies 用当前全局资源装配一个项目目录的
// Harness Runtime 依赖。被管理 Agent 的工具表现取全局注册表（排除
// run_subagent），command、file、activate_skill、create_skill 和当前全局 MCP
// 工具自动全部可见，不按 Agent 过滤。
func (server *Server) buildHarnessRuntimeDependencies(
	applicationConfig config.Config,
) harness.RuntimeDependencies {
	return harness.BuildRuntimeDependencies(
		server.harnessPrompts,
		applicationConfig,
		server.projectConversationStore,
		server.tokenCounter,
		server.modelContextWindowTokens,
		server.conversationEventReceivers,
		server.conversationExecutionLocks,
		server.harnessMCPLiveStatusText,
		func() *tool.Registry {
			return server.registry.CopyExcludingTools(generalSubAgentToolName)
		},
	)
}

// harnessMCPLiveStatusText 生成当前运行中的 MCP Server 及工具名称的
// 自然语言实况；没有运行中的 Server 时明确说明无 MCP 资源。
func (server *Server) harnessMCPLiveStatusText() string {
	noMCPResourceText := "- 当前没有运行中的 MCP Server；" +
		"被管理 Agent 只有 command、file、activate_skill、create_skill 四个基础工具。"
	if server.mcpServerManager == nil {
		return noMCPResourceText
	}
	runningServerLines := make([]string, 0)
	for _, mcpServerStatus := range server.mcpServerManager.ListConfiguredMCPServers() {
		if mcpServerStatus.Status != "running" {
			continue
		}
		runningServerLines = append(runningServerLines, fmt.Sprintf(
			"- %s：工具 %s",
			mcpServerStatus.Name,
			strings.Join(mcpServerStatus.RegisteredAgentToolNames, "、"),
		))
	}
	if len(runningServerLines) == 0 {
		return noMCPResourceText
	}
	return strings.Join(runningServerLines, "\n")
}

// validateHarnessChatRequest 校验 Harness 对话请求：message 必填，
// workingDirectory 必须是存在的绝对目录；Harness 会话编号固定为 harness。
func (server *Server) validateHarnessChatRequest(harnessChatRequest model.ChatRequest) error {
	if strings.TrimSpace(harnessChatRequest.Message) == "" {
		return fmt.Errorf("message 不能为空")
	}
	return (agent.AgentExecutionEnvironment{
		WorkingDirectory: harnessChatRequest.WorkingDirectory,
		ConversationID:   harness.HarnessConversationID,
	}).Validate()
}

// validateHarnessRequestCommon 校验 workingDirectory 和 Harness prompt 配置，
// 三个 Harness 路由共用；返回 nil 表示可以继续。
func (server *Server) validateHarnessRequestCommon(
	responseWriter http.ResponseWriter,
	operation string,
	workingDirectory string,
) bool {
	if validateEnvironmentError := (agent.AgentExecutionEnvironment{
		WorkingDirectory: workingDirectory,
		ConversationID:   harness.HarnessConversationID,
	}).Validate(); validateEnvironmentError != nil {
		server.writeAPIError(responseWriter, operation, harness.HarnessConversationID,
			server.invalidRequestError(operation, validateEnvironmentError))
		return false
	}
	if server.harnessPromptsLoadError != nil {
		server.writeAPIError(responseWriter, operation, harness.HarnessConversationID,
			service.NewAppError(service.ErrorConfig, operation, 0,
				server.harnessPromptsLoadError))
		return false
	}
	return true
}

func (server *Server) handleHarnessChatStream(
	responseWriter http.ResponseWriter,
	httpRequest *http.Request,
) {
	var harnessChatRequest model.ChatRequest
	if decodeRequestError := json.NewDecoder(httpRequest.Body).Decode(
		&harnessChatRequest,
	); decodeRequestError != nil {
		server.writeAPIError(responseWriter, "handleHarnessChatStream.decode", "",
			server.invalidRequestError("handleHarnessChatStream.decode", decodeRequestError))
		return
	}
	// 先校验再设置 SSE 头：校验失败返回真实的 400 JSON 错误。
	if validateRequestError := server.validateHarnessChatRequest(harnessChatRequest); validateRequestError != nil {
		server.writeAPIError(responseWriter, "handleHarnessChatStream.validate",
			harness.HarnessConversationID,
			server.invalidRequestError("handleHarnessChatStream.validate", validateRequestError))
		return
	}
	if server.harnessPromptsLoadError != nil {
		server.writeAPIError(responseWriter, "handleHarnessChatStream.prompts",
			harness.HarnessConversationID,
			service.NewAppError(service.ErrorConfig,
				"handleHarnessChatStream.prompts", 0, server.harnessPromptsLoadError))
		return
	}
	cfg := server.loadConfig()
	if strings.TrimSpace(cfg.ApiKey) == "" {
		server.writeAPIError(responseWriter, "handleHarnessChatStream.config",
			harness.HarnessConversationID,
			service.NewAppError(service.ErrorConfig,
				"handleHarnessChatStream.config", 0,
				fmt.Errorf("DEEPSEEK_API_KEY 未配置")))
		return
	}

	responseWriter.Header().Set("Content-Type", "text/event-stream;charset=UTF-8")
	responseWriter.Header().Set("Cache-Control", "no-cache")
	responseWriter.Header().Set("Connection", "keep-alive")
	flusher, ok := responseWriter.(http.Flusher)
	if !ok {
		server.writeAPIError(responseWriter, "handleHarnessChatStream.flusher", "",
			service.NewAppError(service.ErrorInternal,
				"handleHarnessChatStream.flusher", 0,
				fmt.Errorf("ResponseWriter 不支持 http.Flusher")))
		return
	}

	harnessRuntime, createRuntimeError := harness.GetOrCreateRuntime(
		harnessChatRequest.WorkingDirectory,
		server.buildHarnessRuntimeDependencies(cfg),
	)
	if createRuntimeError != nil {
		server.writeSSEError(responseWriter, flusher,
			"handleHarnessChatStream.runtime", harness.HarnessConversationID,
			createRuntimeError)
		return
	}

	conversationIDJSON, _ := json.Marshal(map[string]string{
		"type":           "conversation_id",
		"conversationId": harness.HarnessConversationID,
	})
	fmt.Fprintf(responseWriter, "data: %s\n\n", conversationIDJSON)
	flusher.Flush()

	done := make(chan struct{})
	go func() {
		defer close(done)
		unlockConversationExecution :=
			server.conversationExecutionLocks.LockConversation(
				harness.HarnessConversationID,
			)
		defer unlockConversationExecution()

		// 先处理完成队列中积压的 Agent 汇报，再执行用户消息，
		// 避免 Agent 失败/完成回调被用户对话锁挡住。
		harnessRuntime.DrainPendingReports()

		runContext, endConversationRun := server.conversationRunRegistry.BeginRun(
			harness.HarnessConversationID,
			httpRequest.Context(),
		)
		defer endConversationRun()

		runResult, runAgentError := service.RunAgentTask(
			agent.UserTaskInput{Message: harnessChatRequest.Message},
			agent.AgentExecutionEnvironment{
				WorkingDirectory: harnessChatRequest.WorkingDirectory,
				ConversationID:   harness.HarnessConversationID,
				Context:          runContext,
			},
			harnessRuntime.HarnessSystemPromptWithLiveStatus(),
			cfg,
			harnessRuntime.HarnessTools(),
			server.projectConversationStore,
			server.tokenCounter,
			server.modelContextWindowTokens,
			service.AgentRunOptions{
				Context:                runContext,
				KeepRecentMemoryTokens: cfg.KeepRecentMemoryTokens,
				StreamText:             true,
				ReceiveEvent: func(receivedAgentEvent agent.AgentEvent) {
					server.writeAgentEventSSE(responseWriter, flusher, receivedAgentEvent)
				},
			},
		)
		if runAgentError != nil {
			server.writeSSEError(responseWriter, flusher,
				"handleHarnessChatStream.run", harness.HarnessConversationID,
				runAgentError)
			return
		}
		server.writeAgentRunFinishedSSE(responseWriter, flusher, runResult)
	}()
	<-done
}

func (server *Server) handleHarnessAgents(
	responseWriter http.ResponseWriter,
	httpRequest *http.Request,
) {
	workingDirectory := httpRequest.URL.Query().Get("workingDirectory")
	if !server.validateHarnessRequestCommon(
		responseWriter,
		"handleHarnessAgents",
		workingDirectory,
	) {
		return
	}
	harnessRuntime, createRuntimeError := harness.GetOrCreateRuntime(
		workingDirectory,
		server.buildHarnessRuntimeDependencies(server.loadConfig()),
	)
	if createRuntimeError != nil {
		server.writeAPIError(responseWriter, "handleHarnessAgents.runtime",
			harness.HarnessConversationID, createRuntimeError)
		return
	}
	agentRegistry := harnessRuntime.AgentRegistry()
	responseWriter.Header().Set("Content-Type", "application/json;charset=UTF-8")
	if encodeAgentsError := json.NewEncoder(responseWriter).Encode(map[string]any{
		"agents":        agentRegistry.ListAgents(),
		"maximumAgents": agentRegistry.MaximumAgents(),
		"runningAgents": agentRegistry.RunningAgentCount(),
	}); encodeAgentsError != nil {
		slog.Error("Harness 名单 JSON 写入失败",
			"component", "http",
			"operation", "handleHarnessAgents.encode",
			"error_kind", service.ErrorInternal,
			"error", encodeAgentsError)
	}
}

func (server *Server) handleHarnessAgentMemory(
	responseWriter http.ResponseWriter,
	httpRequest *http.Request,
) {
	workingDirectory := httpRequest.URL.Query().Get("workingDirectory")
	if !server.validateHarnessRequestCommon(
		responseWriter,
		"handleHarnessAgentMemory",
		workingDirectory,
	) {
		return
	}
	agentName := httpRequest.PathValue("name")
	harnessRuntime, createRuntimeError := harness.GetOrCreateRuntime(
		workingDirectory,
		server.buildHarnessRuntimeDependencies(server.loadConfig()),
	)
	if createRuntimeError != nil {
		server.writeAPIError(responseWriter, "handleHarnessAgentMemory.runtime",
			harness.HarnessConversationID, createRuntimeError)
		return
	}
	agentRecord, agentFound :=
		harnessRuntime.AgentRegistry().GetAgent(agentName)
	if !agentFound {
		server.writeAPIError(responseWriter, "handleHarnessAgentMemory.agent",
			harness.HarnessConversationID,
			server.invalidRequestError("handleHarnessAgentMemory.agent",
				fmt.Errorf("没有名为 %q 的 Agent", agentName)))
		return
	}
	agentSession, loadSessionError := server.projectConversationStore.LoadConversation(
		workingDirectory,
		agentRecord.ConversationID,
	)
	if loadSessionError != nil {
		server.writeAPIError(responseWriter, "handleHarnessAgentMemory.load",
			harness.HarnessConversationID,
			service.NewAppError(service.ErrorStorageRead,
				"handleHarnessAgentMemory.load", 0, loadSessionError))
		return
	}
	responseWriter.Header().Set("Content-Type", "application/json;charset=UTF-8")
	if encodeMemoryError := json.NewEncoder(responseWriter).Encode(agentSession); encodeMemoryError != nil {
		slog.Error("Harness Agent 记忆 JSON 写入失败",
			"component", "http",
			"operation", "handleHarnessAgentMemory.encode",
			"error_kind", service.ErrorInternal,
			"error", encodeMemoryError)
	}
}

// handleHarnessAgentHeartbeat 是远程保活接口：被管理 Agent 每轮循环调用一次，
// 刷新注册表中的最后心跳时间，主管理据此判断 Agent 是否仍在工作。
// 路径：POST /api/harness/agents/{name}/heartbeat
func (server *Server) handleHarnessAgentHeartbeat(
	responseWriter http.ResponseWriter,
	httpRequest *http.Request,
) {
	workingDirectory := httpRequest.URL.Query().Get("workingDirectory")
	if !server.validateHarnessRequestCommon(
		responseWriter,
		"handleHarnessAgentHeartbeat",
		workingDirectory,
	) {
		return
	}
	agentName := strings.TrimSpace(httpRequest.PathValue("name"))
	if agentName == "" {
		server.writeAPIError(responseWriter, "handleHarnessAgentHeartbeat.validate",
			harness.HarnessConversationID,
			server.invalidRequestError("handleHarnessAgentHeartbeat.validate",
				fmt.Errorf("agent 名称不能为空")))
		return
	}
	harnessRuntime, createRuntimeError := harness.GetOrCreateRuntime(
		workingDirectory,
		server.buildHarnessRuntimeDependencies(server.loadConfig()),
	)
	if createRuntimeError != nil {
		server.writeAPIError(responseWriter, "handleHarnessAgentHeartbeat.runtime",
			harness.HarnessConversationID, createRuntimeError)
		return
	}
	agentRecord, agentFound :=
		harnessRuntime.AgentRegistry().GetAgent(agentName)
	if !agentFound {
		server.writeAPIError(responseWriter, "handleHarnessAgentHeartbeat.agent",
			harness.HarnessConversationID,
			server.invalidRequestError("handleHarnessAgentHeartbeat.agent",
				fmt.Errorf("没有名为 %q 的 Agent", agentName)))
		return
	}
	_ = agentRecord
	if heartbeatError := harnessRuntime.AgentRegistry().HeartbeatAgent(
		agentName,
	); heartbeatError != nil {
		server.writeAPIError(responseWriter, "handleHarnessAgentHeartbeat.write",
			harness.HarnessConversationID, heartbeatError)
		return
	}
	recordAfterHeartbeat, _ := harnessRuntime.AgentRegistry().GetAgent(agentName)
	responseWriter.Header().Set("Content-Type", "application/json;charset=UTF-8")
	_ = json.NewEncoder(responseWriter).Encode(map[string]any{
		"agent":           agentName,
		"conversationId":  recordAfterHeartbeat.ConversationID,
		"status":          string(recordAfterHeartbeat.Status),
		"heartbeat":       "ok",
		"lastHeartbeatAt": recordAfterHeartbeat.LastHeartbeatAt,
	})
}

// ============================================================================
// 会话管理路由
// ============================================================================

func (server *Server) handleConversationEvents(
	responseWriter http.ResponseWriter,
	request *http.Request,
) {
	conversationID := request.PathValue("id")
	if strings.TrimSpace(conversationID) == "" {
		server.writeAPIError(
			responseWriter,
			"handleConversationEvents.conversationID",
			"",
			server.invalidRequestError(
				"handleConversationEvents.conversationID",
				fmt.Errorf("缺少 conversationId"),
			),
		)
		return
	}

	responseWriter.Header().Set(
		"Content-Type",
		"text/event-stream;charset=UTF-8",
	)
	responseWriter.Header().Set("Cache-Control", "no-cache")
	responseWriter.Header().Set("Connection", "keep-alive")
	responseWriter.Header().Set("X-Accel-Buffering", "no")

	responseWriterFlusher, supportsFlush :=
		responseWriter.(http.Flusher)
	if !supportsFlush {
		server.writeAPIError(
			responseWriter,
			"handleConversationEvents.flusher",
			conversationID,
			service.NewAppError(
				service.ErrorInternal,
				"handleConversationEvents.flusher",
				0,
				fmt.Errorf("ResponseWriter 不支持 http.Flusher"),
			),
		)
		return
	}

	conversationEventChannel :=
		server.conversationEventReceivers.AddReceiver(conversationID)
	defer server.conversationEventReceivers.RemoveReceiver(
		conversationID,
		conversationEventChannel,
	)

	fmt.Fprint(responseWriter, ": connected\n\n")
	responseWriterFlusher.Flush()

	keepAliveTicker := time.NewTicker(15 * time.Second)
	defer keepAliveTicker.Stop()

	for {
		select {
		case eventJSON := <-conversationEventChannel:
			fmt.Fprintf(responseWriter, "data: %s\n\n", eventJSON)
			responseWriterFlusher.Flush()

		case <-keepAliveTicker.C:
			fmt.Fprint(responseWriter, ": keep-alive\n\n")
			responseWriterFlusher.Flush()

		case <-request.Context().Done():
			return
		}
	}
}

func (server *Server) handleListConversations(w http.ResponseWriter, r *http.Request) {
	workingDirectory := r.URL.Query().Get("workingDirectory")
	if !filepath.IsAbs(workingDirectory) {
		server.writeAPIError(w, "handleListConversations.validate", "",
			server.invalidRequestError("handleListConversations.validate",
				fmt.Errorf("workingDirectory 必须是绝对路径")))
		return
	}
	summaries, err := server.projectConversationStore.ListConversations(workingDirectory)
	if err != nil {
		server.writeAPIError(w, "handleListConversations.list", "",
			service.NewAppError(service.ErrorStorageRead,
				"store.ListConversations", 0, err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(summaries)
}

func (server *Server) handleGetConversation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	workingDirectory := r.URL.Query().Get("workingDirectory")
	session, err := server.projectConversationStore.LoadConversation(workingDirectory, id)
	if err != nil {
		server.writeAPIError(w, "handleGetConversation.load", id,
			service.NewAppError(service.ErrorStorageRead,
				"store.LoadConversation", 0, err))
		return
	}
	if session == nil {
		http.Error(w, "会话不存在", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(session)
}

func (server *Server) handleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	workingDirectory := r.URL.Query().Get("workingDirectory")
	if err := server.projectConversationStore.DeleteConversation(workingDirectory, id); err != nil {
		server.writeAPIError(w, "handleDeleteConversation.delete", id,
			service.NewAppError(service.ErrorStorageWrite,
				"store.DeleteConversation", 0, err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type RecentApplicationLogsJSON struct {
	Logs []json.RawMessage `json:"logs"`
}

func (server *Server) handleListRecentApplicationLogs(
	responseWriter http.ResponseWriter,
	httpRequest *http.Request,
) {
	const defaultMaximumLogEntries = 200
	const hardMaximumLogEntries = 500

	maximumLogEntries := defaultMaximumLogEntries
	if requestedLimit := strings.TrimSpace(
		httpRequest.URL.Query().Get("limit"),
	); requestedLimit != "" {
		convertedLimit, convertLimitError := strconv.Atoi(requestedLimit)
		if convertLimitError != nil ||
			convertedLimit < 1 ||
			convertedLimit > hardMaximumLogEntries {
			server.writeAPIError(
				responseWriter,
				"handleListRecentApplicationLogs.validateLimit",
				"",
				server.invalidRequestError(
					"handleListRecentApplicationLogs.validateLimit",
					fmt.Errorf("limit 必须是 1 到 %d 的整数", hardMaximumLogEntries),
				),
			)
			return
		}
		maximumLogEntries = convertedLimit
	}

	recentApplicationLogs, readLogsError :=
		server.readRecentApplicationLogs(server.applicationLogFilePath, maximumLogEntries)
	if readLogsError != nil {
		server.writeAPIError(
			responseWriter,
			"handleListRecentApplicationLogs.read",
			"",
			service.NewAppError(
				service.ErrorStorageRead,
				"handleListRecentApplicationLogs.read",
				0,
				readLogsError,
			),
		)
		return
	}

	responseWriter.Header().Set("Content-Type", "application/json;charset=UTF-8")
	if encodeLogsError := json.NewEncoder(responseWriter).Encode(
		RecentApplicationLogsJSON{Logs: recentApplicationLogs},
	); encodeLogsError != nil {
		slog.Error(
			"最近日志 JSON 写入失败",
			"component", "http",
			"operation", "handleListRecentApplicationLogs.encode",
			"error_kind", service.ErrorInternal,
			"error", encodeLogsError,
		)
	}
}

func (server *Server) readRecentApplicationLogs(
	logFilePath string,
	maximumLogEntries int,
) ([]json.RawMessage, error) {
	if maximumLogEntries < 1 {
		return nil, fmt.Errorf("maximumLogEntries 必须大于 0")
	}
	applicationLogFile, openLogFileError := os.Open(logFilePath)
	if os.IsNotExist(openLogFileError) {
		return []json.RawMessage{}, nil
	}
	if openLogFileError != nil {
		return nil, fmt.Errorf("打开日志文件失败: %w", openLogFileError)
	}
	defer applicationLogFile.Close()

	recentApplicationLogs := make([]json.RawMessage, 0, maximumLogEntries)
	logLineScanner := bufio.NewScanner(applicationLogFile)
	logLineScanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for logLineScanner.Scan() {
		logLine := append([]byte(nil), logLineScanner.Bytes()...)
		if !json.Valid(logLine) {
			continue
		}
		if len(recentApplicationLogs) == maximumLogEntries {
			copy(recentApplicationLogs, recentApplicationLogs[1:])
			recentApplicationLogs[len(recentApplicationLogs)-1] =
				json.RawMessage(logLine)
			continue
		}
		recentApplicationLogs = append(
			recentApplicationLogs,
			json.RawMessage(logLine),
		)
	}
	if scanLogFileError := logLineScanner.Err(); scanLogFileError != nil {
		return nil, fmt.Errorf("读取日志文件失败: %w", scanLogFileError)
	}
	return recentApplicationLogs, nil
}

// ============================================================================
// 元老院路由
// ============================================================================

func (server *Server) handleCouncil(w http.ResponseWriter, r *http.Request) {
	var req service.CouncilRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.writeAPIError(w, "handleCouncil.decode", "",
			server.invalidRequestError("handleCouncil.decode", err))
		return
	}
	if req.Topic == "" {
		server.writeAPIError(w, "handleCouncil.validate", "",
			server.invalidRequestError("handleCouncil.validate", fmt.Errorf("topic 不能为空")))
		return
	}
	if req.MaxRounds < 1 {
		req.MaxRounds = 3
	}
	cfg := server.loadConfig()
	transcript, totalTokens, err := service.RunCouncil(
		req.Topic, req.MaxRounds, req.Interruption, server.personalities, cfg)
	if err != nil {
		server.writeAPIError(w, "handleCouncil.run", "", err)
		return
	}
	resp := service.CouncilResponse{
		Topic:       req.Topic,
		Rounds:      req.MaxRounds,
		Transcript:  transcript,
		TotalTokens: totalTokens,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (server *Server) handleCouncilStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream;charset=UTF-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		server.writeAPIError(w, "handleCouncilStream.flusher", "",
			service.NewAppError(service.ErrorInternal, "handleCouncilStream.flusher", 0,
				fmt.Errorf("ResponseWriter 不支持 http.Flusher")))
		return
	}
	var req service.CouncilRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.writeSSEError(w, flusher, "handleCouncilStream.decode", "",
			server.invalidRequestError("handleCouncilStream.decode", err))
		return
	}
	if req.Topic == "" {
		server.writeSSEError(w, flusher, "handleCouncilStream.validate", "",
			server.invalidRequestError("handleCouncilStream.validate", fmt.Errorf("topic 不能为空")))
		return
	}
	if req.MaxRounds < 1 {
		req.MaxRounds = 3
	}

	cfg := server.loadConfig()
	topicFrame, _ := json.Marshal(map[string]string{"type": "topic", "text": req.Topic})
	fmt.Fprintf(w, "data: %s\n\n", topicFrame)
	flusher.Flush()

	done := make(chan struct{})
	go func() {
		defer close(done)
		names := make([]string, 0, len(server.personalities))
		for name := range server.personalities {
			names = append(names, name)
		}
		sort.Strings(names)

		history := []model.Message{
			{Role: "user", Content: []model.MessageContentBlock{
				model.TextContentBlock{Text: "【元老院议题】" + req.Topic},
			}},
		}
		if req.Interruption != "" {
			history = append(history, model.Message{
				Role: "user",
				Content: []model.MessageContentBlock{
					model.TextContentBlock{Text: "【公民插话】" + req.Interruption},
				},
			})
		}

		for round := 1; round <= req.MaxRounds; round++ {
			roundFrame, _ := json.Marshal(map[string]any{"type": "round", "round": round})
			fmt.Fprintf(w, "data: %s\n\n", roundFrame)
			flusher.Flush()

			for _, name := range names {
				messages := make([]model.Message, len(history))
				copy(messages, history)
				resp, err := service.Chat(r.Context(), messages, server.personalities[name], cfg, nil, 4096)
				if err != nil {
					server.writeSSEError(w, flusher, "handleCouncilStream.chat", "", err)
					return
				}
				history = append(history, model.Message{
					Role: "assistant",
					Content: []model.MessageContentBlock{
						model.TextContentBlock{Text: "【" + name + "】" + resp.Text},
					},
				})
				speechFrame, _ := json.Marshal(map[string]any{
					"type": "speech", "round": round, "agent": name, "text": resp.Text,
				})
				fmt.Fprintf(w, "data: %s\n\n", speechFrame)
				flusher.Flush()
			}
		}
		doneFrame, _ := json.Marshal(map[string]string{"type": "done"})
		fmt.Fprintf(w, "data: %s\n\n", doneFrame)
		flusher.Flush()
	}()
	<-done
}
