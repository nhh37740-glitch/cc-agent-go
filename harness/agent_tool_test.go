package harness

import (
	"encoding/json"
	"strings"
	"testing"

	"cc-agent-go/memory"
	"cc-agent-go/service"
	"cc-agent-go/tool"
)

// newUnitTestRuntime 构建一个不启动任何后台执行的 Runtime，
// 供无 API 单元测试使用。
func newUnitTestRuntime(t *testing.T, maximumAgents int) *Runtime {
	t.Helper()
	workingDirectory := t.TempDir()
	agentRegistry, loadRegistryError :=
		LoadAgentRegistry(workingDirectory, maximumAgents)
	if loadRegistryError != nil {
		t.Fatalf("加载注册表失败: %v", loadRegistryError)
	}
	return &Runtime{
		workingDirectory: workingDirectory,
		agentRegistry:    agentRegistry,
		completionQueue:  make(chan ManagedAgentRecord, 8),
		dependencies: RuntimeDependencies{
			ConversationStore:          memory.NewProjectConversationStore(),
			ConversationEventReceivers: service.NewConversationEventReceivers(),
			ConversationExecutionLocks: service.NewConversationExecutionLocks(),
			HarnessSystemPrompt:        "测试 Harness prompt",
		},
	}
}

func unitTestExecutionEnvironment(workingDirectory string) tool.ToolExecutionEnvironment {
	return tool.ToolExecutionEnvironment{
		WorkingDirectory: workingDirectory,
		ConversationID:   HarnessConversationID,
	}
}

func TestAgentToolMissingAgentName(t *testing.T) {
	unitRuntime := newUnitTestRuntime(t, 3)
	agentToolUnderTest := newManagedAgentTool(unitRuntime)

	_, executeError := agentToolUnderTest.Execute(
		map[string]any{"request": "做点什么"},
		unitTestExecutionEnvironment(unitRuntime.workingDirectory),
	)
	if executeError == nil || !strings.Contains(executeError.Error(), "agent") {
		t.Fatalf("缺少 agent 参数时应报含 agent 的错误，实际: %v", executeError)
	}
}

func TestAgentToolMissingRequest(t *testing.T) {
	unitRuntime := newUnitTestRuntime(t, 3)
	agentToolUnderTest := newManagedAgentTool(unitRuntime)

	_, executeError := agentToolUnderTest.Execute(
		map[string]any{"agent": "coder"},
		unitTestExecutionEnvironment(unitRuntime.workingDirectory),
	)
	if executeError == nil || !strings.Contains(executeError.Error(), "request") {
		t.Fatalf("缺少 request 参数时应报含 request 的错误，实际: %v", executeError)
	}
}

func TestAgentToolPoolFullErrorNamesLimitAndRoster(t *testing.T) {
	unitRuntime := newUnitTestRuntime(t, 2)
	for _, agentName := range []string{"alpha", "beta"} {
		if _, _, upsertError :=
			unitRuntime.agentRegistry.UpsertAgent(agentName); upsertError != nil {
			t.Fatalf("预填注册表失败: %v", upsertError)
		}
	}
	agentToolUnderTest := newManagedAgentTool(unitRuntime)

	_, executeError := agentToolUnderTest.Execute(
		map[string]any{"agent": "gamma", "request": "新任务"},
		unitTestExecutionEnvironment(unitRuntime.workingDirectory),
	)
	if executeError == nil {
		t.Fatal("满池时应返回错误")
	}
	for _, expectedPart := range []string{"2", "alpha", "beta", "forget"} {
		if !strings.Contains(executeError.Error(), expectedPart) {
			t.Fatalf(
				"满池错误应包含 %q（上限、名单和补救引导），实际: %v",
				expectedPart,
				executeError,
			)
		}
	}
	if _, found := unitRuntime.agentRegistry.GetAgent("gamma"); found {
		t.Fatal("满池时不应创建新 Agent")
	}
}

func TestAgentToolForgetUnknownAgent(t *testing.T) {
	unitRuntime := newUnitTestRuntime(t, 3)
	agentToolUnderTest := newManagedAgentTool(unitRuntime)

	_, executeError := agentToolUnderTest.Execute(
		map[string]any{"agent": "nobody", "forget": true},
		unitTestExecutionEnvironment(unitRuntime.workingDirectory),
	)
	if executeError == nil || !strings.Contains(executeError.Error(), "nobody") {
		t.Fatalf("forget 不存在的 Agent 应报含名字的错误，实际: %v", executeError)
	}
}

func TestAgentToolForgetFreesSlot(t *testing.T) {
	unitRuntime := newUnitTestRuntime(t, 2)
	for _, agentName := range []string{"alpha", "beta"} {
		if _, _, upsertError :=
			unitRuntime.agentRegistry.UpsertAgent(agentName); upsertError != nil {
			t.Fatalf("预填注册表失败: %v", upsertError)
		}
	}
	agentToolUnderTest := newManagedAgentTool(unitRuntime)

	forgetResultJSON, executeError := agentToolUnderTest.Execute(
		map[string]any{"agent": "alpha", "forget": true},
		unitTestExecutionEnvironment(unitRuntime.workingDirectory),
	)
	if executeError != nil {
		t.Fatalf("forget 失败: %v", executeError)
	}
	var forgetResult map[string]any
	if decodeError := json.Unmarshal([]byte(forgetResultJSON), &forgetResult); decodeError != nil {
		t.Fatalf("forget 结果不是 JSON: %v", decodeError)
	}
	if forgetResult["forgotten"] != true {
		t.Fatalf("forget 结果应标记 forgotten=true，实际: %v", forgetResult)
	}
	if agentCount := unitRuntime.agentRegistry.AgentCount(); agentCount != 1 {
		t.Fatalf("forget 后名额应释放（剩 1 个），实际 %d 个", agentCount)
	}
	if _, _, upsertError := unitRuntime.agentRegistry.UpsertAgent("gamma"); upsertError != nil {
		t.Fatalf("forget 释放名额后应能新建 Agent: %v", upsertError)
	}
}

func TestAgentToolRejectsRunningAgent(t *testing.T) {
	unitRuntime := newUnitTestRuntime(t, 3)
	if _, _, upsertError := unitRuntime.agentRegistry.UpsertAgent("coder"); upsertError != nil {
		t.Fatalf("预填注册表失败: %v", upsertError)
	}
	if markRunningError := unitRuntime.agentRegistry.MarkRunning("coder"); markRunningError != nil {
		t.Fatalf("标记 running 失败: %v", markRunningError)
	}
	agentToolUnderTest := newManagedAgentTool(unitRuntime)

	_, executeError := agentToolUnderTest.Execute(
		map[string]any{"agent": "coder", "request": "第二个任务"},
		unitTestExecutionEnvironment(unitRuntime.workingDirectory),
	)
	if executeError == nil || !strings.Contains(executeError.Error(), "正在运行") {
		t.Fatalf("运行中的 Agent 再派任务应报'正在运行'，实际: %v", executeError)
	}
}

func TestBuildContinuationInstruction(t *testing.T) {
	completedInstruction := buildContinuationInstruction(ManagedAgentRecord{
		Name:           "coder",
		ConversationID: "harness-agent-coder",
		Status:         ManagedAgentStatusCompleted,
		Result:         "我是 coder，完成了编码任务",
	})
	for _, expectedPart := range []string{"coder", "harness-agent-coder", "完成了", "我是 coder"} {
		if !strings.Contains(completedInstruction, expectedPart) {
			t.Fatalf("完成汇报应包含 %q，实际: %s", expectedPart, completedInstruction)
		}
	}

	failedInstruction := buildContinuationInstruction(ManagedAgentRecord{
		Name:           "coder",
		ConversationID: "harness-agent-coder",
		Status:         ManagedAgentStatusFailed,
		LastError:      "网络超时",
	})
	for _, expectedPart := range []string{"coder", "失败", "网络超时"} {
		if !strings.Contains(failedInstruction, expectedPart) {
			t.Fatalf("失败汇报应包含 %q，实际: %s", expectedPart, failedInstruction)
		}
	}
}

func TestHarnessSystemPromptWithLiveStatus(t *testing.T) {
	unitRuntime := newUnitTestRuntime(t, 11)

	emptyPrompt := unitRuntime.HarnessSystemPromptWithLiveStatus()
	for _, expectedPart := range []string{
		"测试 Harness prompt", "上限 11", "已创建 0", "还可创建 11", "没有已创建",
	} {
		if !strings.Contains(emptyPrompt, expectedPart) {
			t.Fatalf("空名单实况应包含 %q，实际:\n%s", expectedPart, emptyPrompt)
		}
	}

	if _, _, upsertError := unitRuntime.agentRegistry.UpsertAgent("coder"); upsertError != nil {
		t.Fatalf("upsert 失败: %v", upsertError)
	}
	if _, _, upsertError := unitRuntime.agentRegistry.UpsertAgent("writer"); upsertError != nil {
		t.Fatalf("upsert 失败: %v", upsertError)
	}
	if markRunningError := unitRuntime.agentRegistry.MarkRunning("coder"); markRunningError != nil {
		t.Fatalf("标记 running 失败: %v", markRunningError)
	}

	rosterPrompt := unitRuntime.HarnessSystemPromptWithLiveStatus()
	for _, expectedPart := range []string{
		"已创建 2", "运行中 1", "还可创建 9",
		"coder", "writer", "harness-agent-coder",
	} {
		if !strings.Contains(rosterPrompt, expectedPart) {
			t.Fatalf("名单实况应包含 %q，实际:\n%s", expectedPart, rosterPrompt)
		}
	}
}
