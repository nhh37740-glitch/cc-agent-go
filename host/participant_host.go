package host

import "cc-agent-go/agent"

type ParticipantTurn struct {
	ParticipantName  string
	WorkingDirectory string
	ConversationID   string
	Task             string
}

type AgentRunner interface {
	Run(
		agentTaskInput agent.AgentTaskInput,
		executionEnvironment agent.AgentExecutionEnvironment,
	) (agent.AgentRunResult, error)
}

// RunParticipantTurn 展示狼人杀、剧本杀和元老院主持人如何调用同一个 Agent。
// 角色、回合、顺序和胜负由调用它的主持人保存，不写入 agent 包。
func RunParticipantTurn(
	configuredAgent AgentRunner,
	participantTurn ParticipantTurn,
) (agent.AgentRunResult, error) {
	return configuredAgent.Run(
		agent.HostedAgentTaskInput{Task: participantTurn.Task},
		agent.AgentExecutionEnvironment{
			WorkingDirectory: participantTurn.WorkingDirectory,
			ConversationID:   participantTurn.ConversationID,
		},
	)
}
