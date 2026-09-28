package httpapi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"cc-agent-go/agent"
	"cc-agent-go/config"
	"cc-agent-go/harness"
	"cc-agent-go/model"
	"cc-agent-go/service"
	"cc-agent-go/tool"
)

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
