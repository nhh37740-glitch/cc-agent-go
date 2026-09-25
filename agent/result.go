package agent

type AgentMemorySaveResult interface {
	memorySaveResultKind() string
}

type AgentMemorySaved struct {
	StoredMemoryTokens int
}

func (AgentMemorySaved) memorySaveResultKind() string { return "saved" }

type AgentMemorySaveFailed struct {
	Cause error
}

func (AgentMemorySaveFailed) memorySaveResultKind() string { return "save_failed" }

type AgentRunResult interface {
	FinalText() string
	MemorySaveResult() AgentMemorySaveResult
	runResultKind() string
}

type AgentCompletedResult struct {
	Text       string
	MemorySave AgentMemorySaveResult
}

func (result AgentCompletedResult) FinalText() string { return result.Text }
func (result AgentCompletedResult) MemorySaveResult() AgentMemorySaveResult {
	return result.MemorySave
}
func (AgentCompletedResult) runResultKind() string { return "completed" }

type AgentTerminalToolCompletedResult struct {
	Text       string
	ToolName   string
	ToolResult string
	MemorySave AgentMemorySaveResult
}

func (result AgentTerminalToolCompletedResult) FinalText() string { return result.Text }
func (result AgentTerminalToolCompletedResult) MemorySaveResult() AgentMemorySaveResult {
	return result.MemorySave
}
func (AgentTerminalToolCompletedResult) runResultKind() string {
	return "terminal_tool_completed"
}

type AgentMaximumRoundsReachedResult struct {
	PartialText   string
	MaximumRounds int
	MemorySave    AgentMemorySaveResult
}

func (result AgentMaximumRoundsReachedResult) FinalText() string {
	return result.PartialText
}
func (result AgentMaximumRoundsReachedResult) MemorySaveResult() AgentMemorySaveResult {
	return result.MemorySave
}
func (AgentMaximumRoundsReachedResult) runResultKind() string {
	return "maximum_rounds_reached"
}

// AgentCancelledResult 表示用户停止或客户端断开后，保留已产生进度并结束循环。
type AgentCancelledResult struct {
	PartialText string
	Reason      string
	MemorySave  AgentMemorySaveResult
}

func (result AgentCancelledResult) FinalText() string {
	return result.PartialText
}
func (result AgentCancelledResult) MemorySaveResult() AgentMemorySaveResult {
	return result.MemorySave
}
func (AgentCancelledResult) runResultKind() string {
	return "cancelled"
}
