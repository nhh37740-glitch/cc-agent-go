package host

import (
	"testing"

	"cc-agent-go/agent"
)

// regressionAgentRunner 记录 RunParticipantTurn 实际传入的任务输入和执行环境。
type regressionAgentRunner struct {
	receivedInput       agent.AgentTaskInput
	receivedEnvironment agent.AgentExecutionEnvironment
}

func (runner *regressionAgentRunner) Run(
	agentTaskInput agent.AgentTaskInput,
	executionEnvironment agent.AgentExecutionEnvironment,
) (agent.AgentRunResult, error) {
	runner.receivedInput = agentTaskInput
	runner.receivedEnvironment = executionEnvironment
	return agent.AgentCompletedResult{Text: "发言完毕"}, nil
}

// TestRunParticipantTurnRegression 固定主持人调用的传参契约（v16 任务 1.2）：
// 任务必须包装为 HostedAgentTaskInput，执行环境原样传递，结果原样返回。
func TestRunParticipantTurnRegression(t *testing.T) {
	workingDirectory := t.TempDir()
	runner := &regressionAgentRunner{}
	runResult, runError := RunParticipantTurn(runner, ParticipantTurn{
		ParticipantName:  "苏格拉底",
		WorkingDirectory: workingDirectory,
		ConversationID:   "council-socrates",
		Task:             "请就议题发言",
	})
	if runError != nil {
		t.Fatalf("RunParticipantTurn 返回错误: %v", runError)
	}

	hostedInput, isHostedInput := runner.receivedInput.(agent.HostedAgentTaskInput)
	if !isHostedInput {
		t.Fatalf("任务输入类型 = %T，期望 agent.HostedAgentTaskInput", runner.receivedInput)
	}
	if hostedInput.Task != "请就议题发言" {
		t.Fatalf("HostedAgentTaskInput.Task = %q，期望 %q", hostedInput.Task, "请就议题发言")
	}
	if runner.receivedEnvironment.WorkingDirectory != workingDirectory ||
		runner.receivedEnvironment.ConversationID != "council-socrates" {
		t.Fatalf("执行环境 = %+v，期望目录 %q 会话 %q",
			runner.receivedEnvironment, workingDirectory, "council-socrates")
	}
	if runResult.FinalText() != "发言完毕" {
		t.Fatalf("FinalText = %q，期望 %q", runResult.FinalText(), "发言完毕")
	}
}
