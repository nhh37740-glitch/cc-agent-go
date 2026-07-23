package main

import (
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
	"strings"
	"syscall"
	"time"

	"cc-agent-go/config"
	"cc-agent-go/mcp"
	"cc-agent-go/model"
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

// ============================================================================
// System Prompt
// ============================================================================
// System Prompt + Memory
// ============================================================================

const memoryRule = `## memory/AGENT.MD 长期记忆规则

memory/AGENT.MD 是你的长期记忆文件。如果用户在对话中表达了会反复使用的偏好、项目规则、任务状态或重要决策，你必须主动用 bash 工具创建或更新该文件。
更新方式：bash 执行 echo "内容" >> memory/AGENT.MD
（bash 工具的工作目录已经是 workspace/，所以直接写 memory/AGENT.MD 即可）`

const systemPromptBase = `你是一个 AI 助手。你可以使用 bash 工具执行 shell 命令来操作文件。
当用户要求创建文件、读文件、执行命令时，你必须调用 bash 工具，不要只用文字说明。
你需要结合对话历史中的上下文来理解用户的追问和省略表达。
如果 activate_skill 工具列出的技能和用户需求匹配，先调用 activate_skill 激活技能再回答。`

// loadMemory 读取 memory/AGENT.MD（相对于工作目录 workspace/）。
func loadMemory() string {
	data, err := os.ReadFile("workspace/memory/AGENT.MD")
	if err != nil {
		return ""
	}
	return string(data)
}

// buildSystemPrompt 拼装完整提示词：基础提示词 + 记忆规则 + 当前记忆内容。
func buildSystemPrompt() string {
	var sb strings.Builder
	sb.WriteString(systemPromptBase)
	sb.WriteString("\n\n")
	sb.WriteString(memoryRule)
	mem := loadMemory()
	if mem != "" {
		sb.WriteString("\n\n当前 workspace/memory/AGENT.MD 内容：\n")
		sb.WriteString(mem)
	}
	return sb.String()
}

var registry = tool.NewRegistry()
var mcpServerManager *mcp.MCPServerManager
var conversationEventReceivers = service.NewConversationEventReceivers()
var conversationExecutionLocks = service.NewConversationExecutionLocks()

const generalSubAgentToolName = "run_subagent"

const generalSubAgentToolDescription = `同时运行一个或多个临时通用 SubAgent。
每个 subAgentTasks 元素必须包含唯一 taskId，以及完成任务需要的全部文件位置、执行动作和返回内容。
只提交彼此独立、可以同时执行的任务。`

var generalSubAgentToolInputSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"subAgentTasks": map[string]any{
			"type":     "array",
			"minItems": 1,
			"maxItems": 5,
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
				},
				"required": []string{"taskId", "task"},
			},
		},
	},
	"required": []string{"subAgentTasks"},
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
	executeRunSubAgentTool := func(
		toolArguments map[string]any,
	) (string, error) {
		applicationConfig := config.Load()
		runSubAgentToolInput, decodeToolArgumentsError :=
			decodeAndValidateRunSubAgentToolInput(
				toolArguments,
				applicationConfig.MaximumParallelSubAgents,
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

	return mainAgentToolRegistry.RegisterFunctionTool(
		generalSubAgentToolName,
		generalSubAgentToolDescription,
		generalSubAgentToolInputSchema,
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
	conversationStore := service.NewStore(applicationConfig.SessionsDir)
	backgroundReplyToolRegistry :=
		registry.CopyExcludingTools(generalSubAgentToolName)

	backgroundReplyText, _, continueMainAgentError :=
		service.ContinueConversationAfterSubAgentsStream(
			subAgentResults,
			parentConversationID,
			buildSystemPrompt(),
			applicationConfig,
			backgroundReplyToolRegistry,
			conversationStore,
			func(token string) {
				sendBackgroundAgentReplyToken(
					parentConversationID,
					BackgroundAgentReplyTokenEvent{
						Type:      "background_reply_token",
						MessageID: backgroundReplyMessageID,
						Token:     token,
					},
				)
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
			Text:      backgroundReplyText,
		},
	)
}

func createConversationToolRegistry(
	parentConversationID string,
) (*tool.Registry, error) {
	conversationToolRegistry :=
		registry.CopyExcludingTools(generalSubAgentToolName)
	registerGeneralSubAgentError := registerGeneralSubAgentTool(
		conversationToolRegistry,
		parentConversationID,
		continueMainAgentAfterSubAgents,
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
	var req model.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, "handleChat.decode", "",
			invalidRequestError("handleChat.decode", err))
		return
	}
	cfg := config.Load()
	store := service.NewStore(cfg.SessionsDir)

	conversationID := req.ConversationId
	if conversationID == "" {
		conversationID = service.GenerateConversationId()
	}
	conversationToolRegistry, createToolRegistryError :=
		createConversationToolRegistry(conversationID)
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

	reply, convId, err := service.Run(req.Message, conversationID,
		buildSystemPrompt(), cfg, conversationToolRegistry, store)
	if err != nil {
		writeAPIError(w, "handleChat.run", convId, err)
		return
	}
	resp := model.ChatResponse{ConversationId: convId, Reply: reply}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("聊天响应 JSON 写入失败",
			"component", "http",
			"operation", "handleChat.encode",
			"conversation_id", convId,
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
	var req model.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeSSEError(w, flusher, "handleChatStream.decode", "",
			invalidRequestError("handleChatStream.decode", err))
		return
	}

	// 预先生成 conversationId，作为首帧 SSE 发送
	conversationId := req.ConversationId
	if conversationId == "" {
		conversationId = service.GenerateConversationId()
	}
	convIdJSON, _ := json.Marshal(map[string]string{
		"type":           "conversation_id",
		"conversationId": conversationId,
	})
	fmt.Fprintf(w, "data: %s\n\n", convIdJSON)
	flusher.Flush()

	cfg := config.Load()
	store := service.NewStore(cfg.SessionsDir)
	conversationToolRegistry, createToolRegistryError :=
		createConversationToolRegistry(conversationId)
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

		reply, _, err := service.RunStream(req.Message, conversationId,
			buildSystemPrompt(), cfg, conversationToolRegistry, store,
			func(token string) {
				if len(token) > 0 && token[0] == '{' {
					fmt.Fprintf(w, "data: %s\n\n", token)
				} else {
					jsonToken, _ := json.Marshal(token)
					fmt.Fprintf(w, "data: %s\n\n", jsonToken)
				}
				flusher.Flush()
			})
		if err != nil {
			writeSSEError(w, flusher, "handleChatStream.run", conversationId, err)
		}
		if reply != "" {
			doneJSON, _ := json.Marshal(map[string]string{"type": "done", "text": reply})
			fmt.Fprintf(w, "data: %s\n\n", doneJSON)
			flusher.Flush()
		}
	}()
	<-done
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
	cfg := config.Load()
	store := service.NewStore(cfg.SessionsDir)
	summaries, err := store.ListConversations()
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
	cfg := config.Load()
	store := service.NewStore(cfg.SessionsDir)
	session, err := store.LoadConversation(id)
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
	cfg := config.Load()
	store := service.NewStore(cfg.SessionsDir)
	if err := store.DeleteConversation(id); err != nil {
		writeAPIError(w, "handleDeleteConversation.delete", id,
			service.NewAppError(service.ErrorStorageWrite,
				"store.DeleteConversation", 0, err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

	if err := registry.Register(tool.NewBashTool("workspace")); err != nil {
		slog.Error("工具注册失败", "component", "startup", "tool_name", "bash", "error", err)
		os.Exit(1)
	}
	if err := registry.Register(tool.NewSkillTool("workspace/skills")); err != nil {
		slog.Error("工具注册失败", "component", "startup", "tool_name", "activate_skill", "error", err)
		os.Exit(1)
	}
	if err := registry.Register(tool.NewCreateSkillTool("workspace/skills")); err != nil {
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
	http.HandleFunc("POST /api/council", handleCouncil)
	http.HandleFunc("POST /api/council/stream", handleCouncilStream)
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
		config.Load().MaximumParallelSubAgents)

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
