package host

import (
	"testing"

	"cc-agent-go/agent"
)

type recordedAgentRunner struct {
	taskInput   agent.AgentTaskInput
	environment agent.AgentExecutionEnvironment
}

func (agentRunner *recordedAgentRunner) Run(
	taskInput agent.AgentTaskInput,
	environment agent.AgentExecutionEnvironment,
) (agent.AgentRunResult, error) {
	agentRunner.taskInput = taskInput
	agentRunner.environment = environment
	return agent.AgentCompletedResult{
		Text:       "role result",
		MemorySave: agent.AgentMemorySaved{StoredMemoryTokens: 10},
	}, nil
}

func TestHostPassesRoleTaskDirectoryAndConversationToUnchangedAgent(t *testing.T) {
	agentRunner := &recordedAgentRunner{}
	runResult, runTurnError := RunParticipantTurn(
		agentRunner,
		ParticipantTurn{
			ParticipantName:  "player 3",
			WorkingDirectory: "C:/games/werewolf-1",
			ConversationID:   "player-3",
			Task:             "choose night action",
		},
	)
	if runTurnError != nil {
		t.Fatal(runTurnError)
	}
	if runResult.FinalText() != "role result" ||
		agentRunner.taskInput.TaskText() != "choose night action" ||
		agentRunner.environment.WorkingDirectory != "C:/games/werewolf-1" ||
		agentRunner.environment.ConversationID != "player-3" {
		t.Fatalf("host did not pass the exact turn: %#v", agentRunner)
	}
}
