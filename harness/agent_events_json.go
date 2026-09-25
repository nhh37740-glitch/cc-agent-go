package harness

import "cc-agent-go/agent"

// AgentEventJSONFields 把 agent.AgentEvent 编码为扇出给网页的 JSON 字段。
// 字段名与 main.go 的 writeAgentEventSSE 保持一致，网页渲染逻辑可以通用；
// 不认识的事件类型返回 nil（不扇出）。
func AgentEventJSONFields(receivedAgentEvent agent.AgentEvent) map[string]any {
	switch concreteAgentEvent := receivedAgentEvent.(type) {
	case agent.AgentMemoryReferenceReadyEvent:
		return map[string]any{
			"type":           "memory_reference_ready",
			"conversationId": concreteAgentEvent.ConversationID,
			"sessionFile":    concreteAgentEvent.RelativeSessionFilePath,
		}
	case agent.AgentRoundStartedEvent:
		return map[string]any{
			"type":                  "round_started",
			"round":                 concreteAgentEvent.Round,
			"preparedRequestTokens": concreteAgentEvent.PreparedRequestTokens,
		}
	case agent.AgentTextDeltaEvent:
		return map[string]any{
			"type": "text_delta",
			"text": concreteAgentEvent.Text,
		}
	case agent.AgentToolStartedEvent:
		return map[string]any{
			"type":      "tool_started",
			"round":     concreteAgentEvent.Round,
			"toolUseId": concreteAgentEvent.ToolUseID,
			"toolName":  concreteAgentEvent.ToolName,
		}
	case agent.AgentToolSucceededEvent:
		return map[string]any{
			"type":      "tool_succeeded",
			"round":     concreteAgentEvent.Round,
			"toolUseId": concreteAgentEvent.ToolUseID,
			"toolName":  concreteAgentEvent.ToolName,
		}
	case agent.AgentToolFailedEvent:
		return map[string]any{
			"type":      "tool_failed",
			"round":     concreteAgentEvent.Round,
			"toolUseId": concreteAgentEvent.ToolUseID,
			"toolName":  concreteAgentEvent.ToolName,
			"message":   concreteAgentEvent.Cause.Error(),
		}
	case agent.AgentCurrentRunCompactedEvent:
		return map[string]any{
			"type":           "current_run_compacted",
			"messagesBefore": concreteAgentEvent.MessagesBefore,
			"messagesAfter":  concreteAgentEvent.MessagesAfter,
		}
	case agent.AgentStoredMemoryCompressedEvent:
		return map[string]any{
			"type":         "stored_memory_compressed",
			"tokensBefore": concreteAgentEvent.TokensBefore,
			"tokensAfter":  concreteAgentEvent.TokensAfter,
		}
	case agent.AgentStoredMemoryCompressionFailedEvent:
		return map[string]any{
			"type":    "stored_memory_compression_failed",
			"message": concreteAgentEvent.Cause.Error(),
		}
	case agent.AgentMemorySaveFailedEvent:
		return map[string]any{
			"type":    "memory_save_failed",
			"message": concreteAgentEvent.Cause.Error(),
		}
	case agent.AgentCancelledEvent:
		return map[string]any{
			"type":        "cancelled",
			"text":        concreteAgentEvent.PartialText,
			"reason":      concreteAgentEvent.Reason,
			"partialText": concreteAgentEvent.PartialText,
		}
	case agent.AgentCompletedEvent:
		fields := map[string]any{
			"type": "agent_completed",
			"text": concreteAgentEvent.Result.FinalText(),
		}
		if cancelledResult, isCancelled := concreteAgentEvent.Result.(agent.AgentCancelledResult); isCancelled {
			fields["resultKind"] = "cancelled"
			fields["reason"] = cancelledResult.Reason
		}
		return fields
	default:
		return nil
	}
}
