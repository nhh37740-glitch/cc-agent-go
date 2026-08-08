package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"cc-agent-go/agent"
	"cc-agent-go/config"
	"cc-agent-go/harness"
	"cc-agent-go/mcp"
	"cc-agent-go/memory"
	"cc-agent-go/model"
	"cc-agent-go/modeltoken"
	"cc-agent-go/service"
	"cc-agent-go/tool"
)

// ============================================================================
// 人格加载
// ============================================================================

// loadPersonalities 扫描 personalities/ 目录下所有 .md 文件，
// 返回 map[agent名]文件内容。agent 名 = 文件名去掉 .md 后缀。
func loadPersonalities(dir string) (map[string]string, error) {
	result := make(map[string]string)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("读取人格目录失败: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			slog.Warn("读取人格文件失败",
				"component", "startup",
				"operation", "loadPersonalities",
				"file_name", name,
				"error", err)
			continue
		}
		agentName := strings.TrimSuffix(name, ".md")
		result[agentName] = strings.TrimSpace(string(data))
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("personalities/ 目录下没有 .md 文件")
	}
	return result, nil
}

var personalities map[string]string

const systemPromptBase = `你是一个 AI 助手。你可以使用 bash 工具执行 shell 命令来操作文件。
当用户要求创建文件、读文件、执行命令时，你必须调用 bash 工具，不要只用文字说明。
需要旧会话信息时，按照本次 system message 提供的会话文件位置读取必要片段。
如果 activate_skill 工具列出的技能和用户需求匹配，先调用 activate_skill。`

var registry = tool.NewRegistry()
var mcpServerManager *mcp.MCPServerManager
var conversationEventReceivers = service.NewConversationEventReceivers()
var conversationExecutionLocks = service.NewConversationExecutionLocks()
var projectConversationStore = memory.NewProjectConversationStore()
var configuredTokenCounter agent.AgentTokenCounter
var configuredModelContextWindowTokens int
var harnessPrompts harness.Prompts
var harnessPromptsLoadError error

const generalSubAgentToolName = service.GeneralSubAgentToolName

const generalSubAgentToolDescription = `同时运行一个或多个临时通用 SubAgent。
每个 subAgentTasks 元素必须包含唯一 taskId、完整任务和 maximumRounds。
根据任务需要选择 maximumRounds：简单任务使用较小数值，浏览器搜索等多步骤任务使用较大数值。
只提交彼此独立、可以同时执行的任务。`

func buildGeneralSubAgentToolInputSchema(
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

func decodeAndValidateRunSubAgentToolInput(
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

func registerGeneralSubAgentTool(
	mainAgentToolRegistry *tool.Registry,
	parentConversationID string,
	completedResultsCallback service.CompletedSubAgentResultsCallback,
) error {
	applicationConfig := config.Load()

	executeRunSubAgentTool := func(
		toolArguments map[string]any,
		toolExecutionEnvironment tool.ToolExecutionEnvironment,
	) (string, error) {
		runSubAgentToolInput, decodeToolArgumentsError :=
			decodeAndValidateRunSubAgentToolInput(
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
				ConversationStore:        projectConversationStore,
				TokenCounter:             configuredTokenCounter,
				ModelContextWindowTokens: configuredModelContextWindowTokens,
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
		buildGeneralSubAgentToolInputSchema(
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

func sendEncodedConversationEvent(
	conversationID string,
	conversationEventJSON []byte,
) {
	conversationEventReceivers.SendEventJSON(
		conversationID,
		conversationEventJSON,
	)
}

func sendBackgroundAgentReplyStarted(
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
	sendEncodedConversationEvent(conversationID, conversationEventJSON)
}

func sendBackgroundAgentReplyToken(
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
	sendEncodedConversationEvent(conversationID, conversationEventJSON)
}

func sendBackgroundAgentReplyCompleted(
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
	sendEncodedConversationEvent(conversationID, conversationEventJSON)
}

func sendBackgroundAgentReplyFailed(
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
	sendEncodedConversationEvent(conversationID, conversationEventJSON)
}

func continueMainAgentAfterSubAgents(
	workingDirectory string,
	parentConversationID string,
	subAgentResults []service.SubAgentResult,
) {
	backgroundReplyMessageID := service.GenerateConversationId()
	sendBackgroundAgentReplyStarted(
		parentConversationID,
		BackgroundAgentReplyStartedEvent{
			Type:      "background_reply_started",
			MessageID: backgroundReplyMessageID,
		},
	)

	unlockConversationExecution :=
		conversationExecutionLocks.LockConversation(parentConversationID)
	defer unlockConversationExecution()

	applicationConfig := config.Load()
	backgroundReplyToolRegistry := tool.NewRegistry()
	internalContinuationTask, encodeSubAgentResultsError :=
		agent.EncodeInternalContinuationTask(subAgentResults)
	if encodeSubAgentResultsError != nil {
		sendBackgroundAgentReplyFailed(
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
		projectConversationStore,
		configuredTokenCounter,
		configuredModelContextWindowTokens,
		service.AgentRunOptions{
			StreamText: true,
			ReceiveEvent: func(agentEvent agent.AgentEvent) {
				textDeltaEvent, isTextDelta :=
					agentEvent.(agent.AgentTextDeltaEvent)
				if !isTextDelta {
					return
				}
				sendBackgroundAgentReplyToken(
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
		sendBackgroundAgentReplyFailed(
			parentConversationID,
			BackgroundAgentReplyFailedEvent{
				Type:      "background_reply_failed",
				MessageID: backgroundReplyMessageID,
				Message:   "SubAgent 完成后调用主 Agent失败。",
			},
		)
		return
	}

	sendBackgroundAgentReplyCompleted(
		parentConversationID,
		BackgroundAgentReplyCompletedEvent{
			Type:      "background_reply_completed",
			MessageID: backgroundReplyMessageID,
			Text:      backgroundReplyResult.FinalText(),
		},
	)
}

func createConversationToolRegistry(
	parentConversationID string,
	workingDirectory string,
) (*tool.Registry, error) {
	conversationToolRegistry :=
		registry.CopyExcludingTools(generalSubAgentToolName)
	registerGeneralSubAgentError := registerGeneralSubAgentTool(
		conversationToolRegistry,
		parentConversationID,
		func(
			completedParentConversationID string,
			subAgentResults []service.SubAgentResult,
		) {
			continueMainAgentAfterSubAgents(
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

func publicError(err error) (int, model.ErrorResponse) {
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

func logAPIError(operation string, conversationID string, err error, resp model.ErrorResponse) {
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

func writeAPIError(w http.ResponseWriter, operation string, conversationID string, err error) {
	status, resp := publicError(err)
	logAPIError(operation, conversationID, err, resp)
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

func writeSSEError(w http.ResponseWriter, flusher http.Flusher, operation string,
	conversationID string, err error) {
	_, resp := publicError(err)
	logAPIError(operation, conversationID, err, resp)
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

func invalidRequestError(operation string, err error) *service.AppError {
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

func handleListMCPServers(responseWriter http.ResponseWriter, httpRequest *http.Request) {
	if mcpServerManager == nil {
		writeAPIError(
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
		Servers: mcpServerManager.ListConfiguredMCPServers(),
	}); encodeServerListError != nil {
		slog.Error("MCP Server 列表 JSON 写入失败",
			"component", "http",
			"operation", "handleListMCPServers",
			"error_kind", service.ErrorInternal,
			"error", encodeServerListError)
	}
}

func handleSelectMCPServers(responseWriter http.ResponseWriter, httpRequest *http.Request) {
	var frontendMCPServerSelectionJSON FrontendMCPServerSelectionJSON
	if decodeSelectionError := json.NewDecoder(httpRequest.Body).Decode(
		&frontendMCPServerSelectionJSON,
	); decodeSelectionError != nil {
		writeAPIError(
			responseWriter,
			"handleSelectMCPServers.decodeSelection",
			"",
			invalidRequestError(
				"handleSelectMCPServers.decodeSelection",
				decodeSelectionError,
			),
		)
		return
	}
	if mcpServerManager == nil {
		writeAPIError(
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
		mcpServerManager.StartSelectedMCPServers(
			httpRequest.Context(),
			frontendMCPServerSelectionJSON.SelectedMCPServerNames,
			registry,
		)
	if applySelectionError != nil {
		writeAPIError(
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

func handleChat(w http.ResponseWriter, r *http.Request) {
	var webAgentTaskRequest model.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&webAgentTaskRequest); err != nil {
		writeAPIError(w, "handleChat.decode", "",
			invalidRequestError("handleChat.decode", err))
		return
	}
	assignWebAgentConversationID(&webAgentTaskRequest)
	if validateWebAgentTaskError := validateWebAgentTaskRequest(
		webAgentTaskRequest,
	); validateWebAgentTaskError != nil {
		writeAPIError(w, "handleChat.validate", webAgentTaskRequest.ConversationId,
			invalidRequestError("handleChat.validate", validateWebAgentTaskError))
		return
	}
	cfg := config.Load()
	if strings.TrimSpace(cfg.ApiKey) == "" {
		writeAPIError(w, "handleChat.config", webAgentTaskRequest.ConversationId,
			service.NewAppError(service.ErrorConfig, "handleChat.config", 0,
				fmt.Errorf("DEEPSEEK_API_KEY 未配置")))
		return
	}
	conversationID := webAgentTaskRequest.ConversationId
	conversationToolRegistry, createToolRegistryError :=
		createConversationToolRegistry(
			conversationID,
			webAgentTaskRequest.WorkingDirectory,
		)
	if createToolRegistryError != nil {
		writeAPIError(
			w,
			"handleChat.createConversationToolRegistry",
			conversationID,
			createToolRegistryError,
		)
		return
	}

	unlockConversationExecution :=
		conversationExecutionLocks.LockConversation(conversationID)
	defer unlockConversationExecution()

	runResult, runAgentError := service.RunAgentTask(
		agent.UserTaskInput{Message: webAgentTaskRequest.Message},
		agent.AgentExecutionEnvironment{
			WorkingDirectory: webAgentTaskRequest.WorkingDirectory,
			ConversationID:   conversationID,
		},
		systemPromptBase,
		cfg,
		conversationToolRegistry,
		projectConversationStore,
		configuredTokenCounter,
		configuredModelContextWindowTokens,
		service.AgentRunOptions{},
	)
	if runAgentError != nil {
		writeAPIError(w, "handleChat.run", conversationID, runAgentError)
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

func handleChatStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream;charset=UTF-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, "handleChatStream.flusher", "",
			service.NewAppError(service.ErrorInternal, "handleChatStream.flusher", 0,
				fmt.Errorf("ResponseWriter 不支持 http.Flusher")))
		return
	}
	var webAgentTaskRequest model.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&webAgentTaskRequest); err != nil {
		writeSSEError(w, flusher, "handleChatStream.decode", "",
			invalidRequestError("handleChatStream.decode", err))
		return
	}

	assignWebAgentConversationID(&webAgentTaskRequest)
	if validateWebAgentTaskError := validateWebAgentTaskRequest(
		webAgentTaskRequest,
	); validateWebAgentTaskError != nil {
		writeSSEError(
			w,
			flusher,
			"handleChatStream.validate",
			webAgentTaskRequest.ConversationId,
			invalidRequestError("handleChatStream.validate", validateWebAgentTaskError),
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

	cfg := config.Load()
	if strings.TrimSpace(cfg.ApiKey) == "" {
		writeSSEError(w, flusher, "handleChatStream.config", conversationId,
			service.NewAppError(service.ErrorConfig, "handleChatStream.config", 0,
				fmt.Errorf("DEEPSEEK_API_KEY 未配置")))
		return
	}
	conversationToolRegistry, createToolRegistryError :=
		createConversationToolRegistry(
			conversationId,
			webAgentTaskRequest.WorkingDirectory,
		)
	if createToolRegistryError != nil {
		writeSSEError(
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
			conversationExecutionLocks.LockConversation(conversationId)
		defer unlockConversationExecution()

		runResult, runAgentError := service.RunAgentTask(
			agent.UserTaskInput{Message: webAgentTaskRequest.Message},
			agent.AgentExecutionEnvironment{
				WorkingDirectory: webAgentTaskRequest.WorkingDirectory,
				ConversationID:   conversationId,
			},
			systemPromptBase,
			cfg,
			conversationToolRegistry,
			projectConversationStore,
			configuredTokenCounter,
			configuredModelContextWindowTokens,
			service.AgentRunOptions{
				StreamText: true,
				ReceiveEvent: func(agentEvent agent.AgentEvent) {
					writeAgentEventSSE(w, flusher, agentEvent)
				},
			},
		)
		if runAgentError != nil {
			writeSSEError(w, flusher, "handleChatStream.run", conversationId, runAgentError)
			return
		}
		if runResult.FinalText() != "" {
			doneJSON, _ := json.Marshal(map[string]string{
				"type": "done",
				"text": runResult.FinalText(),
			})
			fmt.Fprintf(w, "data: %s\n\n", doneJSON)
			flusher.Flush()
		}
	}()
	<-done
}

func assignWebAgentConversationID(webAgentTaskRequest *model.ChatRequest) {
	webAgentTaskRequest.ConversationId =
		strings.TrimSpace(webAgentTaskRequest.ConversationId)
	if webAgentTaskRequest.ConversationId == "" {
		webAgentTaskRequest.ConversationId = service.GenerateConversationId()
	}
}

func validateWebAgentTaskRequest(webAgentTaskRequest model.ChatRequest) error {
	if strings.TrimSpace(webAgentTaskRequest.Message) == "" {
		return fmt.Errorf("message 不能为空")
	}
	return (agent.AgentExecutionEnvironment{
		WorkingDirectory: webAgentTaskRequest.WorkingDirectory,
		ConversationID:   webAgentTaskRequest.ConversationId,
	}).Validate()
}

func writeAgentEventSSE(
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
	case agent.AgentCompletedEvent:
		eventJSONFields = map[string]any{
			"type": "agent_completed",
			"text": concreteAgentEvent.Result.FinalText(),
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
// run_subagent），bash、activate_skill、create_skill 和当前全局 MCP
// 工具自动全部可见，不按 Agent 过滤。
func buildHarnessRuntimeDependencies(
	applicationConfig config.Config,
) harness.RuntimeDependencies {
	return harness.BuildRuntimeDependencies(
		harnessPrompts,
		applicationConfig,
		projectConversationStore,
		configuredTokenCounter,
		configuredModelContextWindowTokens,
		conversationEventReceivers,
		conversationExecutionLocks,
		harnessMCPLiveStatusText,
		func() *tool.Registry {
			return registry.CopyExcludingTools(generalSubAgentToolName)
		},
	)
}

// harnessMCPLiveStatusText 生成当前运行中的 MCP Server 及工具名称的
// 自然语言实况；没有运行中的 Server 时明确说明无 MCP 资源。
func harnessMCPLiveStatusText() string {
	noMCPResourceText := "- 当前没有运行中的 MCP Server；" +
		"被管理 Agent 只有 bash、activate_skill、create_skill 三个基础工具。"
	if mcpServerManager == nil {
		return noMCPResourceText
	}
	runningServerLines := make([]string, 0)
	for _, mcpServerStatus := range mcpServerManager.ListConfiguredMCPServers() {
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
func validateHarnessChatRequest(harnessChatRequest model.ChatRequest) error {
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
func validateHarnessRequestCommon(
	responseWriter http.ResponseWriter,
	operation string,
	workingDirectory string,
) bool {
	if validateEnvironmentError := (agent.AgentExecutionEnvironment{
		WorkingDirectory: workingDirectory,
		ConversationID:   harness.HarnessConversationID,
	}).Validate(); validateEnvironmentError != nil {
		writeAPIError(responseWriter, operation, harness.HarnessConversationID,
			invalidRequestError(operation, validateEnvironmentError))
		return false
	}
	if harnessPromptsLoadError != nil {
		writeAPIError(responseWriter, operation, harness.HarnessConversationID,
			service.NewAppError(service.ErrorConfig, operation, 0,
				harnessPromptsLoadError))
		return false
	}
	return true
}

func handleHarnessChatStream(
	responseWriter http.ResponseWriter,
	httpRequest *http.Request,
) {
	var harnessChatRequest model.ChatRequest
	if decodeRequestError := json.NewDecoder(httpRequest.Body).Decode(
		&harnessChatRequest,
	); decodeRequestError != nil {
		writeAPIError(responseWriter, "handleHarnessChatStream.decode", "",
			invalidRequestError("handleHarnessChatStream.decode", decodeRequestError))
		return
	}
	// 先校验再设置 SSE 头：校验失败返回真实的 400 JSON 错误。
	if validateRequestError := validateHarnessChatRequest(harnessChatRequest); validateRequestError != nil {
		writeAPIError(responseWriter, "handleHarnessChatStream.validate",
			harness.HarnessConversationID,
			invalidRequestError("handleHarnessChatStream.validate", validateRequestError))
		return
	}
	if harnessPromptsLoadError != nil {
		writeAPIError(responseWriter, "handleHarnessChatStream.prompts",
			harness.HarnessConversationID,
			service.NewAppError(service.ErrorConfig,
				"handleHarnessChatStream.prompts", 0, harnessPromptsLoadError))
		return
	}
	cfg := config.Load()
	if strings.TrimSpace(cfg.ApiKey) == "" {
		writeAPIError(responseWriter, "handleHarnessChatStream.config",
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
		writeAPIError(responseWriter, "handleHarnessChatStream.flusher", "",
			service.NewAppError(service.ErrorInternal,
				"handleHarnessChatStream.flusher", 0,
				fmt.Errorf("ResponseWriter 不支持 http.Flusher")))
		return
	}

	harnessRuntime, createRuntimeError := harness.GetOrCreateRuntime(
		harnessChatRequest.WorkingDirectory,
		buildHarnessRuntimeDependencies(cfg),
	)
	if createRuntimeError != nil {
		writeSSEError(responseWriter, flusher,
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
			conversationExecutionLocks.LockConversation(
				harness.HarnessConversationID,
			)
		defer unlockConversationExecution()

		runResult, runAgentError := service.RunAgentTask(
			agent.UserTaskInput{Message: harnessChatRequest.Message},
			agent.AgentExecutionEnvironment{
				WorkingDirectory: harnessChatRequest.WorkingDirectory,
				ConversationID:   harness.HarnessConversationID,
			},
			harnessRuntime.HarnessSystemPromptWithLiveStatus(),
			cfg,
			harnessRuntime.HarnessTools(),
			projectConversationStore,
			configuredTokenCounter,
			configuredModelContextWindowTokens,
			service.AgentRunOptions{
				StreamText: true,
				ReceiveEvent: func(receivedAgentEvent agent.AgentEvent) {
					writeAgentEventSSE(responseWriter, flusher, receivedAgentEvent)
				},
			},
		)
		if runAgentError != nil {
			writeSSEError(responseWriter, flusher,
				"handleHarnessChatStream.run", harness.HarnessConversationID,
				runAgentError)
			return
		}
		if runResult.FinalText() != "" {
			doneJSON, _ := json.Marshal(map[string]string{
				"type": "done",
				"text": runResult.FinalText(),
			})
			fmt.Fprintf(responseWriter, "data: %s\n\n", doneJSON)
			flusher.Flush()
		}
	}()
	<-done
}

func handleHarnessAgents(
	responseWriter http.ResponseWriter,
	httpRequest *http.Request,
) {
	workingDirectory := httpRequest.URL.Query().Get("workingDirectory")
	if !validateHarnessRequestCommon(
		responseWriter,
		"handleHarnessAgents",
		workingDirectory,
	) {
		return
	}
	harnessRuntime, createRuntimeError := harness.GetOrCreateRuntime(
		workingDirectory,
		buildHarnessRuntimeDependencies(config.Load()),
	)
	if createRuntimeError != nil {
		writeAPIError(responseWriter, "handleHarnessAgents.runtime",
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

func handleHarnessAgentMemory(
	responseWriter http.ResponseWriter,
	httpRequest *http.Request,
) {
	workingDirectory := httpRequest.URL.Query().Get("workingDirectory")
	if !validateHarnessRequestCommon(
		responseWriter,
		"handleHarnessAgentMemory",
		workingDirectory,
	) {
		return
	}
	agentName := httpRequest.PathValue("name")
	harnessRuntime, createRuntimeError := harness.GetOrCreateRuntime(
		workingDirectory,
		buildHarnessRuntimeDependencies(config.Load()),
	)
	if createRuntimeError != nil {
		writeAPIError(responseWriter, "handleHarnessAgentMemory.runtime",
			harness.HarnessConversationID, createRuntimeError)
		return
	}
	agentRecord, agentFound :=
		harnessRuntime.AgentRegistry().GetAgent(agentName)
	if !agentFound {
		writeAPIError(responseWriter, "handleHarnessAgentMemory.agent",
			harness.HarnessConversationID,
			invalidRequestError("handleHarnessAgentMemory.agent",
				fmt.Errorf("没有名为 %q 的 Agent", agentName)))
		return
	}
	agentSession, loadSessionError := projectConversationStore.LoadConversation(
		workingDirectory,
		agentRecord.ConversationID,
	)
	if loadSessionError != nil {
		writeAPIError(responseWriter, "handleHarnessAgentMemory.load",
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

// ============================================================================
// 会话管理路由
// ============================================================================

func handleConversationEvents(
	responseWriter http.ResponseWriter,
	request *http.Request,
) {
	conversationID := request.PathValue("id")
	if strings.TrimSpace(conversationID) == "" {
		writeAPIError(
			responseWriter,
			"handleConversationEvents.conversationID",
			"",
			invalidRequestError(
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
		writeAPIError(
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
		conversationEventReceivers.AddReceiver(conversationID)
	defer conversationEventReceivers.RemoveReceiver(
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

func handleListConversations(w http.ResponseWriter, r *http.Request) {
	workingDirectory := r.URL.Query().Get("workingDirectory")
	if !filepath.IsAbs(workingDirectory) {
		writeAPIError(w, "handleListConversations.validate", "",
			invalidRequestError("handleListConversations.validate",
				fmt.Errorf("workingDirectory 必须是绝对路径")))
		return
	}
	summaries, err := projectConversationStore.ListConversations(workingDirectory)
	if err != nil {
		writeAPIError(w, "handleListConversations.list", "",
			service.NewAppError(service.ErrorStorageRead,
				"store.ListConversations", 0, err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(summaries)
}

func handleGetConversation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	workingDirectory := r.URL.Query().Get("workingDirectory")
	session, err := projectConversationStore.LoadConversation(workingDirectory, id)
	if err != nil {
		writeAPIError(w, "handleGetConversation.load", id,
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

func handleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	workingDirectory := r.URL.Query().Get("workingDirectory")
	if err := projectConversationStore.DeleteConversation(workingDirectory, id); err != nil {
		writeAPIError(w, "handleDeleteConversation.delete", id,
			service.NewAppError(service.ErrorStorageWrite,
				"store.DeleteConversation", 0, err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type RecentApplicationLogsJSON struct {
	Logs []json.RawMessage `json:"logs"`
}

func handleListRecentApplicationLogs(
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
			writeAPIError(
				responseWriter,
				"handleListRecentApplicationLogs.validateLimit",
				"",
				invalidRequestError(
					"handleListRecentApplicationLogs.validateLimit",
					fmt.Errorf("limit 必须是 1 到 %d 的整数", hardMaximumLogEntries),
				),
			)
			return
		}
		maximumLogEntries = convertedLimit
	}

	recentApplicationLogs, readLogsError :=
		readRecentApplicationLogs(applicationLogFilePath, maximumLogEntries)
	if readLogsError != nil {
		writeAPIError(
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

func readRecentApplicationLogs(
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

func handleCouncil(w http.ResponseWriter, r *http.Request) {
	var req service.CouncilRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, "handleCouncil.decode", "",
			invalidRequestError("handleCouncil.decode", err))
		return
	}
	if req.Topic == "" {
		writeAPIError(w, "handleCouncil.validate", "",
			invalidRequestError("handleCouncil.validate", fmt.Errorf("topic 不能为空")))
		return
	}
	if req.MaxRounds < 1 {
		req.MaxRounds = 3
	}
	cfg := config.Load()
	transcript, totalTokens, err := service.RunCouncil(
		req.Topic, req.MaxRounds, req.Interruption, personalities, cfg)
	if err != nil {
		writeAPIError(w, "handleCouncil.run", "", err)
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

func handleCouncilStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream;charset=UTF-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, "handleCouncilStream.flusher", "",
			service.NewAppError(service.ErrorInternal, "handleCouncilStream.flusher", 0,
				fmt.Errorf("ResponseWriter 不支持 http.Flusher")))
		return
	}
	var req service.CouncilRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeSSEError(w, flusher, "handleCouncilStream.decode", "",
			invalidRequestError("handleCouncilStream.decode", err))
		return
	}
	if req.Topic == "" {
		writeSSEError(w, flusher, "handleCouncilStream.validate", "",
			invalidRequestError("handleCouncilStream.validate", fmt.Errorf("topic 不能为空")))
		return
	}
	if req.MaxRounds < 1 {
		req.MaxRounds = 3
	}

	cfg := config.Load()
	topicFrame, _ := json.Marshal(map[string]string{"type": "topic", "text": req.Topic})
	fmt.Fprintf(w, "data: %s\n\n", topicFrame)
	flusher.Flush()

	done := make(chan struct{})
	go func() {
		defer close(done)
		names := make([]string, 0, len(personalities))
		for name := range personalities {
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
				resp, err := service.Chat(messages, personalities[name], cfg, nil, 4096)
				if err != nil {
					writeSSEError(w, flusher, "handleCouncilStream.chat", "", err)
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

// ============================================================================
// main
// ============================================================================

const applicationLogFilePath = "logs/server.jsonl"

func configureApplicationLogger(
	logFilePath string,
	standardErrorWriter io.Writer,
) (*os.File, error) {
	logDirectoryPath := filepath.Dir(logFilePath)
	if createLogDirectoryError := os.MkdirAll(logDirectoryPath, 0o755); createLogDirectoryError != nil {
		return nil, fmt.Errorf("创建日志目录失败: %w", createLogDirectoryError)
	}

	applicationLogFile, openLogFileError := os.OpenFile(
		logFilePath,
		os.O_CREATE|os.O_APPEND|os.O_WRONLY,
		0o644,
	)
	if openLogFileError != nil {
		return nil, fmt.Errorf("打开日志文件失败: %w", openLogFileError)
	}

	logOutputWriter := io.MultiWriter(standardErrorWriter, applicationLogFile)
	slog.SetDefault(slog.New(slog.NewJSONHandler(logOutputWriter, nil)))
	return applicationLogFile, nil
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	applicationLogFile, configureLoggerError :=
		configureApplicationLogger(applicationLogFilePath, os.Stderr)
	if configureLoggerError != nil {
		slog.Error("日志文件初始化失败，继续只向 stderr 输出",
			"component", "startup",
			"operation", "configureApplicationLogger",
			"error_kind", service.ErrorStorageWrite,
			"error", configureLoggerError)
	} else {
		defer applicationLogFile.Close()
	}

	applicationConfig := config.Load()
	modelTokenizerConfiguration, loadTokenizerConfigurationError :=
		config.LoadModelTokenizerConfiguration(applicationConfig.Model)
	if loadTokenizerConfigurationError != nil {
		slog.Error("模型 tokenizer 配置加载失败",
			"component", "startup",
			"operation", "config.LoadModelTokenizerConfiguration",
			"error_kind", service.ErrorConfig,
			"error", loadTokenizerConfigurationError)
		os.Exit(1)
	}
	loadedTokenCounter, createTokenCounterError :=
		modeltoken.NewHuggingFaceJSONTokenCounter(modelTokenizerConfiguration)
	if createTokenCounterError != nil {
		slog.Error("模型 tokenizer 启动失败",
			"component", "startup",
			"operation", "modeltoken.NewHuggingFaceJSONTokenCounter",
			"error_kind", service.ErrorConfig,
			"error", createTokenCounterError)
		os.Exit(1)
	}
	defer loadedTokenCounter.Close()
	configuredTokenCounter = loadedTokenCounter
	configuredModelContextWindowTokens =
		modelTokenizerConfiguration.MaximumContextTokens

	var err error
	personalities, err = loadPersonalities("personalities")
	if err != nil {
		slog.Error("加载人格失败",
			"component", "startup",
			"operation", "loadPersonalities",
			"error_kind", service.ErrorConfig,
			"error", err)
		os.Exit(1)
	}
	names := make([]string, 0, len(personalities))
	for name := range personalities {
		names = append(names, name)
	}
	sort.Strings(names)
	slog.Info("人格加载完成",
		"component", "startup",
		"operation", "loadPersonalities",
		"personality_count", len(names),
		"personality_names", strings.Join(names, ", "))

	if err := registry.Register(tool.NewBashTool()); err != nil {
		slog.Error("工具注册失败", "component", "startup", "tool_name", "bash", "error", err)
		os.Exit(1)
	}
	if err := registry.Register(tool.NewSkillTool()); err != nil {
		slog.Error("工具注册失败", "component", "startup", "tool_name", "activate_skill", "error", err)
		os.Exit(1)
	}
	if err := registry.Register(tool.NewCreateSkillTool()); err != nil {
		slog.Error("工具注册失败", "component", "startup", "tool_name", "create_skill", "error", err)
		os.Exit(1)
	}
	if err := registry.Register(tool.NewFileTool()); err != nil {
		slog.Error("工具注册失败", "component", "startup", "tool_name", "create_skill", "error", err)
		os.Exit(1)
	}
	mcpServerManager, err = mcp.NewMCPServerManager(
		"config/mcp_servers.json",
		"mcp/protocol/2025-11-25/messages.json",
	)
	if err != nil {
		slog.Error("MCP Server 配置加载失败",
			"component", "startup",
			"operation", "mcp.NewMCPServerManager",
			"error_kind", mcp.ErrorConfigurationInvalid,
			"error", err)
		os.Exit(1)
	}

	// Harness prompt 缺失时服务继续启动，但 Harness 路由返回配置错误。
	harnessPrompts, harnessPromptsLoadError = harness.LoadPrompts(
		"harness/system_prompt.md",
		"harness/managed_agent_prompt.md",
	)
	if harnessPromptsLoadError != nil {
		slog.Error("Harness prompt 加载失败，Harness 路由将返回配置错误",
			"component", "startup",
			"operation", "harness.LoadPrompts",
			"error_kind", service.ErrorConfig,
			"error", harnessPromptsLoadError)
	}

	http.HandleFunc("POST /api/chat", handleChat)
	http.HandleFunc("POST /api/chat/stream", handleChatStream)
	http.HandleFunc("GET /api/mcp/servers", handleListMCPServers)
	http.HandleFunc("PUT /api/mcp/servers", handleSelectMCPServers)
	http.HandleFunc("GET /api/conversations", handleListConversations)
	http.HandleFunc(
		"GET /api/conversations/{id}/events",
		handleConversationEvents,
	)
	http.HandleFunc("GET /api/conversations/{id}", handleGetConversation)
	http.HandleFunc("DELETE /api/conversations/{id}", handleDeleteConversation)
	http.HandleFunc("GET /api/logs", handleListRecentApplicationLogs)
	http.HandleFunc("POST /api/council", handleCouncil)
	http.HandleFunc("POST /api/council/stream", handleCouncilStream)
	http.HandleFunc("POST /api/harness/chat/stream", handleHarnessChatStream)
	http.HandleFunc("GET /api/harness/agents", handleHarnessAgents)
	http.HandleFunc(
		"GET /api/harness/agents/{name}/memory",
		handleHarnessAgentMemory,
	)
	http.HandleFunc("GET /harness", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "harness.html")
	})
	http.HandleFunc("GET /council", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "council.html")
	})
	http.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "index.html")
	})

	slog.Info("cc-agent-go v15 启动",
		"component", "startup",
		"address", "http://localhost:8080",
		"maximum_parallel_subagents",
		applicationConfig.MaximumParallelSubAgents,
		"maximum_subagent_rounds",
		applicationConfig.MaximumSubAgentRounds)

	httpServer := &http.Server{Addr: ":8080"}
	httpServerFinished := make(chan error, 1)
	go func() {
		httpServerFinished <- httpServer.ListenAndServe()
	}()

	shutdownSignal := make(chan os.Signal, 1)
	signal.Notify(shutdownSignal, os.Interrupt, syscall.SIGTERM)
	select {
	case receivedSignal := <-shutdownSignal:
		slog.Info("收到服务停止信号",
			"component", "startup",
			"operation", "main.waitForShutdown",
			"signal", receivedSignal.String())
	case listenError := <-httpServerFinished:
		if !errors.Is(listenError, http.ErrServerClosed) {
			slog.Error("HTTP 服务运行失败",
				"component", "http",
				"operation", "http.Server.ListenAndServe",
				"error_kind", service.ErrorInternal,
				"error", listenError)
		}
	}
	signal.Stop(shutdownSignal)

	if closeMCPServersError := mcpServerManager.CloseAllStartedMCPServers(registry); closeMCPServersError != nil {
		slog.Error("停止 MCP Server 失败",
			"component", "mcp_client",
			"operation", "CloseAllStartedMCPServers",
			"error_kind", mcp.ErrorProcessStopped,
			"error", closeMCPServersError)
	}

	shutdownContext, cancelHTTPShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelHTTPShutdown()
	if shutdownHTTPServerError := httpServer.Shutdown(shutdownContext); shutdownHTTPServerError != nil {
		slog.Error("HTTP 服务停止失败",
			"component", "http",
			"operation", "http.Server.Shutdown",
			"error_kind", service.ErrorInternal,
			"error", shutdownHTTPServerError)
	}
}
