package agent

type AgentEvent interface {
	agentEventKind() string
}

type AgentEventReceiver func(AgentEvent)

type AgentMemoryReferenceReadyEvent struct {
	ConversationID          string
	RelativeSessionFilePath string
}

func (AgentMemoryReferenceReadyEvent) agentEventKind() string { return "memory_reference_ready" }

type AgentRoundStartedEvent struct {
	Round                 int
	PreparedRequestTokens int
}

func (AgentRoundStartedEvent) agentEventKind() string { return "round_started" }

type AgentTextDeltaEvent struct {
	Text string
}

func (AgentTextDeltaEvent) agentEventKind() string { return "text_delta" }

type AgentToolStartedEvent struct {
	Round     int
	ToolUseID string
	ToolName  string
}

func (AgentToolStartedEvent) agentEventKind() string { return "tool_started" }

type AgentToolSucceededEvent struct {
	Round     int
	ToolUseID string
	ToolName  string
}

func (AgentToolSucceededEvent) agentEventKind() string { return "tool_succeeded" }

type AgentToolFailedEvent struct {
	Round     int
	ToolUseID string
	ToolName  string
	Cause     error
}

func (AgentToolFailedEvent) agentEventKind() string { return "tool_failed" }

type AgentCurrentRunCompactedEvent struct {
	MessagesBefore int
	MessagesAfter  int
}

func (AgentCurrentRunCompactedEvent) agentEventKind() string { return "current_run_compacted" }

type AgentStoredMemoryCompressedEvent struct {
	TokensBefore int
	TokensAfter  int
}

func (AgentStoredMemoryCompressedEvent) agentEventKind() string { return "stored_memory_compressed" }

type AgentStoredMemoryCompressionFailedEvent struct {
	Cause error
}

func (AgentStoredMemoryCompressionFailedEvent) agentEventKind() string {
	return "stored_memory_compression_failed"
}

type AgentMemorySaveFailedEvent struct {
	Cause error
}

func (AgentMemorySaveFailedEvent) agentEventKind() string { return "memory_save_failed" }

type AgentCompletedEvent struct {
	Result AgentRunResult
}

func (AgentCompletedEvent) agentEventKind() string { return "completed" }
