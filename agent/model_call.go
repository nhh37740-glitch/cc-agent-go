package agent

import "cc-agent-go/model"

type AgentModelCallRequest struct {
	SystemPrompt        string
	Messages            []model.Message
	ToolDefinitions     []map[string]any
	MaximumOutputTokens int
	ReceiveTextDelta    func(string)
}

type AgentModelCallFunction func(
	modelCallRequest AgentModelCallRequest,
) (model.ApiResponse, error)
