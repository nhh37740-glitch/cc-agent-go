package service

import (
	"cc-agent-go/agent"
	"cc-agent-go/config"
	"cc-agent-go/memory"
	"cc-agent-go/model"
	"cc-agent-go/tool"
)

const defaultMaximumAgentRounds = 50
const defaultMaximumOutputTokens = 4096
const defaultMaximumToolResultTokens = 8000

type AgentRunOptions struct {
	MaximumRounds int
	ReceiveEvent  agent.AgentEventReceiver
	StreamText    bool
}

func RunAgentTask(
	agentTaskInput agent.AgentTaskInput,
	executionEnvironment agent.AgentExecutionEnvironment,
	systemPrompt string,
	applicationConfig config.Config,
	availableTools *tool.Registry,
	conversationStore *memory.ProjectConversationStore,
	tokenCounter agent.AgentTokenCounter,
	modelContextWindowTokens int,
	runOptions AgentRunOptions,
) (agent.AgentRunResult, error) {
	maximumRounds := runOptions.MaximumRounds
	if maximumRounds < 1 {
		maximumRounds = defaultMaximumAgentRounds
	}
	maximumStoredMemoryTokens := applicationConfig.CompressionThreshold
	if maximumStoredMemoryTokens < 1 {
		maximumStoredMemoryTokens = 100000
	}
	callDeepSeek := func(
		modelCallRequest agent.AgentModelCallRequest,
	) (model.ApiResponse, error) {
		if runOptions.StreamText {
			returnedResponse, callDeepSeekError := ChatStream(
				modelCallRequest.Messages,
				modelCallRequest.SystemPrompt,
				applicationConfig,
				modelCallRequest.ToolDefinitions,
				modelCallRequest.MaximumOutputTokens,
				modelCallRequest.ReceiveTextDelta,
			)
			if callDeepSeekError != nil {
				return model.ApiResponse{}, callDeepSeekError
			}
			return *returnedResponse, nil
		}
		returnedResponse, callDeepSeekError := Chat(
			modelCallRequest.Messages,
			modelCallRequest.SystemPrompt,
			applicationConfig,
			modelCallRequest.ToolDefinitions,
			modelCallRequest.MaximumOutputTokens,
		)
		if callDeepSeekError != nil {
			return model.ApiResponse{}, callDeepSeekError
		}
		return *returnedResponse, nil
	}
	configuredAgent, createAgentError := agent.NewAgent(
		agent.AgentConfiguration{
			ModelName:                 applicationConfig.Model,
			BaseSystemPrompt:          systemPrompt,
			MaximumRounds:             maximumRounds,
			MaximumOutputTokens:       defaultMaximumOutputTokens,
			MaximumToolResultTokens:   defaultMaximumToolResultTokens,
			MaximumStoredMemoryTokens: maximumStoredMemoryTokens,
			ModelContextWindowTokens:  modelContextWindowTokens,
		},
		availableTools,
		conversationStore,
		callDeepSeek,
		tokenCounter,
		runOptions.ReceiveEvent,
	)
	if createAgentError != nil {
		return nil, createAgentError
	}
	return configuredAgent.Run(agentTaskInput, executionEnvironment)
}
