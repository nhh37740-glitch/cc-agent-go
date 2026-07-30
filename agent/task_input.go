package agent

import "strings"

type AgentTaskInput interface {
	TaskText() string
	ShouldSaveVisibleTask() bool
	taskInputKind() string
}

type UserTaskInput struct {
	Message string
}

func (input UserTaskInput) TaskText() string      { return input.Message }
func (UserTaskInput) ShouldSaveVisibleTask() bool { return true }
func (UserTaskInput) taskInputKind() string       { return "user" }
func (input UserTaskInput) isValid() bool         { return strings.TrimSpace(input.Message) != "" }

type InternalContinuationTaskInput struct {
	ContinuationInstruction string
}

func (input InternalContinuationTaskInput) TaskText() string {
	return input.ContinuationInstruction
}
func (InternalContinuationTaskInput) ShouldSaveVisibleTask() bool { return false }
func (InternalContinuationTaskInput) taskInputKind() string       { return "internal_continuation" }
func (input InternalContinuationTaskInput) isValid() bool {
	return strings.TrimSpace(input.ContinuationInstruction) != ""
}

type HostedAgentTaskInput struct {
	Task string
}

func (input HostedAgentTaskInput) TaskText() string      { return input.Task }
func (HostedAgentTaskInput) ShouldSaveVisibleTask() bool { return true }
func (HostedAgentTaskInput) taskInputKind() string       { return "hosted" }
func (input HostedAgentTaskInput) isValid() bool         { return strings.TrimSpace(input.Task) != "" }

func validateAgentTaskInput(agentTaskInput AgentTaskInput) bool {
	switch concreteTaskInput := agentTaskInput.(type) {
	case UserTaskInput:
		return concreteTaskInput.isValid()
	case InternalContinuationTaskInput:
		return concreteTaskInput.isValid()
	case HostedAgentTaskInput:
		return concreteTaskInput.isValid()
	default:
		return false
	}
}
