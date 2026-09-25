package service

import (
	"context"

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
	Context       context.Context
	MaximumRounds int
	ReceiveEvent  agent.AgentEventReceiver
	StreamText    bool
	// RoundHeartbeat 每轮模型调用前调用一次；Harness 用它做保活。
	RoundHeartbeat func()
	// KeepRecentMemoryTokens 主动附带的最近历史 token 预算；
	// 0 或负数表示不附带历史（旧行为，靠工具现读）。
	KeepRecentMemoryTokens int
	// MaximumOutputTokens 覆盖单次模型输出上限；0 表示用默认（4096）。
	MaximumOutputTokens int
	// MaximumToolResultTokens 覆盖单次工具结果截断上限；0 表示用默认（8000）。
	MaximumToolResultTokens int
	// MaximumStoredMemoryTokens 覆盖会话记忆压缩阈值；0 表示用配置默认（100000）。
	MaximumStoredMemoryTokens int
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
	maximumStoredMemoryTokens := runOptions.MaximumStoredMemoryTokens
	if maximumStoredMemoryTokens < 1 {
		maximumStoredMemoryTokens = applicationConfig.CompressionThreshold
	}
	if maximumStoredMemoryTokens < 1 {
		maximumStoredMemoryTokens = 100000
	}
	maximumOutputTokens := runOptions.MaximumOutputTokens
	if maximumOutputTokens < 1 {
		maximumOutputTokens = defaultMaximumOutputTokens
	}
	maximumToolResultTokens := runOptions.MaximumToolResultTokens
	if maximumToolResultTokens < 1 {
		maximumToolResultTokens = defaultMaximumToolResultTokens
	}
	runContext := runOptions.Context
	if runContext == nil {
		runContext = context.Background()
	}
	if executionEnvironment.Context == nil {
		executionEnvironment.Context = runContext
	}
	callDeepSeek := func(
		modelCallRequest agent.AgentModelCallRequest,
	) (model.ApiResponse, error) {
		modelCallContext := modelCallRequest.Context
		if modelCallContext == nil {
			modelCallContext = runContext
		}
		if runOptions.StreamText {
			returnedResponse, callDeepSeekError := ChatStream(
				modelCallContext,
				modelCallRequest.Messages,
				modelCallRequest.SystemPrompt,
				applicationConfig,
				modelCallRequest.ToolDefinitions,
				modelCallRequest.MaximumOutputTokens,
				modelCallRequest.ReceiveTextDelta,
			)
			if callDeepSeekError != nil {
				if returnedResponse != nil {
					return *returnedResponse, callDeepSeekError
				}
				return model.ApiResponse{}, callDeepSeekError
			}
			return *returnedResponse, nil
		}
		returnedResponse, callDeepSeekError := Chat(
			modelCallContext,
			modelCallRequest.Messages,
			modelCallRequest.SystemPrompt,
			applicationConfig,
			modelCallRequest.ToolDefinitions,
			modelCallRequest.MaximumOutputTokens,
		)
		if callDeepSeekError != nil {
			if returnedResponse != nil {
				return *returnedResponse, callDeepSeekError
			}
			return model.ApiResponse{}, callDeepSeekError
		}
		return *returnedResponse, nil
	}
	configuredAgent, createAgentError := agent.NewAgent(
		agent.AgentConfiguration{
			ModelName:                 applicationConfig.Model,
			BaseSystemPrompt:          systemPrompt,
			MaximumRounds:             maximumRounds,
			MaximumOutputTokens:       maximumOutputTokens,
			MaximumToolResultTokens:   maximumToolResultTokens,
			MaximumStoredMemoryTokens: maximumStoredMemoryTokens,
			ModelContextWindowTokens:  modelContextWindowTokens,
			KeepRecentMemoryTokens:    runOptions.KeepRecentMemoryTokens,
			RoundHeartbeat:            runOptions.RoundHeartbeat,
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
