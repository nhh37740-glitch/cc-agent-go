package httpapi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"cc-agent-go/agent"
	"cc-agent-go/model"
	"cc-agent-go/service"
)

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
