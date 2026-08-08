package harness

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"cc-agent-go/config"
	"cc-agent-go/memory"
	"cc-agent-go/modeltoken"
	"cc-agent-go/service"
	"cc-agent-go/tool"
)

// TestHarnessAgentLifecycleIntegration 用真实 DeepSeek 验证完整链路：
// 启动即返回 running → 后台完成 → 注册表写回 → 完成队列消费者收取并
// 触发 Harness 自调用 → 同名 Agent 复用同一份会话记忆。
// 设置 RUN_HARNESS_DEEPSEEK_INTEGRATION=1 才运行（沿用
// RUN_DEEPSEEK_TOKEN_INTEGRATION 的环境变量守卫模式）。
func TestHarnessAgentLifecycleIntegration(t *testing.T) {
	if os.Getenv("RUN_HARNESS_DEEPSEEK_INTEGRATION") != "1" {
		t.Skip("set RUN_HARNESS_DEEPSEEK_INTEGRATION=1 to call DeepSeek")
	}
	applicationConfig := config.Load()
	if strings.TrimSpace(applicationConfig.ApiKey) == "" {
		t.Skip("DEEPSEEK_API_KEY 未配置")
	}
	tokenizerConfiguration, loadTokenizerError :=
		config.LoadModelTokenizerConfiguration(applicationConfig.Model)
	if loadTokenizerError != nil {
		t.Fatalf("加载 tokenizer 配置失败: %v", loadTokenizerError)
	}
	tokenCounter, createTokenCounterError :=
		modeltoken.NewHuggingFaceJSONTokenCounter(tokenizerConfiguration)
	if createTokenCounterError != nil {
		t.Fatalf("创建 token 计数器失败: %v", createTokenCounterError)
	}
	defer tokenCounter.Close()

	workingDirectory := t.TempDir()
	conversationStore := memory.NewProjectConversationStore()
	integrationRuntime, createRuntimeError := GetOrCreateRuntime(
		workingDirectory,
		RuntimeDependencies{
			ApplicationConfig:          applicationConfig,
			ConversationStore:          conversationStore,
			TokenCounter:               tokenCounter,
			ModelContextWindowTokens:   tokenizerConfiguration.MaximumContextTokens,
			ConversationEventReceivers: service.NewConversationEventReceivers(),
			ConversationExecutionLocks: service.NewConversationExecutionLocks(),
			HarnessSystemPrompt: "你是 Harness，负责编排被管理 Agent。" +
				"收到被管理 Agent 的完成或失败汇报后，用一两句中文直接向用户总结，" +
				"禁止调用任何工具。",
			ManagedAgentSystemPrompt: "你是一个执行型 Agent。最终回复必须包含：" +
				"你是谁、执行了什么任务、结果如何（关键文件与数值）。",
			ManagedAgentToolRegistries: func() *tool.Registry {
				return tool.NewRegistry()
			},
		},
	)
	if createRuntimeError != nil {
		t.Fatalf("创建 Runtime 失败: %v", createRuntimeError)
	}
	executionEnvironment := tool.ToolExecutionEnvironment{
		WorkingDirectory: workingDirectory,
		ConversationID:   HarnessConversationID,
	}

	// 一轮连续启动两个 Agent：工具必须立即返回 running，不等待后台执行。
	for _, agentName := range []string{"intcoder", "intwriter"} {
		launchResultJSON, launchError := integrationRuntime.HarnessTools().Execute(
			"agent",
			map[string]any{
				"agent":   agentName,
				"request": "不要使用任何工具，直接回复：INTEGRATION_OK_" + agentName,
			},
			executionEnvironment,
		)
		if launchError != nil {
			t.Fatalf("启动 %s 失败: %v", agentName, launchError)
		}
		var launchResult map[string]any
		if decodeError := json.Unmarshal(
			[]byte(launchResultJSON),
			&launchResult,
		); decodeError != nil {
			t.Fatalf("启动结果不是 JSON: %v", decodeError)
		}
		if launchResult["status"] != string(ManagedAgentStatusRunning) {
			t.Fatalf(
				"%s 应立即返回 running，实际: %v",
				agentName,
				launchResult["status"],
			)
		}
		if launchResult["conversationId"] != "harness-agent-"+agentName {
			t.Fatalf(
				"%s 会话 ID = %v，期望 harness-agent-%s",
				agentName,
				launchResult["conversationId"],
				agentName,
			)
		}
		if launchResult["created"] != true {
			t.Fatalf("%s 首次启动 created 应为 true，实际: %v", agentName, launchResult)
		}
	}

	// 等两个 Agent 都完成（状态写回注册表）。
	waitForCondition(t, 240*time.Second, "两个 Agent 完成", func() bool {
		for _, agentName := range []string{"intcoder", "intwriter"} {
			agentRecord, found :=
				integrationRuntime.AgentRegistry().GetAgent(agentName)
			if !found || agentRecord.Status != ManagedAgentStatusCompleted {
				return false
			}
		}
		return true
	})
	coderRecord, _ := integrationRuntime.AgentRegistry().GetAgent("intcoder")
	if !strings.Contains(coderRecord.Result, "INTEGRATION_OK_intcoder") {
		t.Fatalf("intcoder 汇报应包含 INTEGRATION_OK_intcoder，实际: %q",
			coderRecord.Result)
	}

	// 完成队列消费者逐条收取并触发 Harness 自调用：
	// 收取标记翻转 + Harness 自己的会话文件出现。
	waitForCondition(t, 240*time.Second, "完成队列收取并自调用", func() bool {
		for _, agentName := range []string{"intcoder", "intwriter"} {
			agentRecord, found :=
				integrationRuntime.AgentRegistry().GetAgent(agentName)
			if !found || !agentRecord.ResultCollected {
				return false
			}
		}
		return true
	})
	harnessSession, loadHarnessSessionError :=
		conversationStore.LoadConversation(workingDirectory, HarnessConversationID)
	if loadHarnessSessionError != nil {
		t.Fatalf("Harness 自调用应产生 harness 会话文件: %v", loadHarnessSessionError)
	}
	if len(harnessSession.Messages) == 0 {
		t.Fatal("Harness 会话应有自调用产生的消息")
	}

	// 同名复用记忆：第二次派任务 created=false，会话消息在原有基础上增长。
	coderSessionBefore, loadCoderSessionError :=
		conversationStore.LoadConversation(workingDirectory, "harness-agent-intcoder")
	if loadCoderSessionError != nil {
		t.Fatalf("读取 intcoder 会话失败: %v", loadCoderSessionError)
	}
	messageCountBefore := len(coderSessionBefore.Messages)

	secondLaunchJSON, secondLaunchError := integrationRuntime.HarnessTools().Execute(
		"agent",
		map[string]any{
			"agent":   "intcoder",
			"request": "不要使用任何工具，直接回复：SECOND_TASK_DONE",
		},
		executionEnvironment,
	)
	if secondLaunchError != nil {
		t.Fatalf("第二次启动 intcoder 失败: %v", secondLaunchError)
	}
	var secondLaunchResult map[string]any
	if decodeError := json.Unmarshal(
		[]byte(secondLaunchJSON),
		&secondLaunchResult,
	); decodeError != nil {
		t.Fatalf("第二次启动结果不是 JSON: %v", decodeError)
	}
	if secondLaunchResult["created"] != false {
		t.Fatalf("同名第二次启动 created 应为 false，实际: %v",
			secondLaunchResult["created"])
	}

	waitForCondition(t, 240*time.Second, "intcoder 第二次完成", func() bool {
		agentRecord, _ := integrationRuntime.AgentRegistry().GetAgent("intcoder")
		return agentRecord.Status == ManagedAgentStatusCompleted &&
			strings.Contains(agentRecord.Result, "SECOND_TASK_DONE")
	})
	coderSessionAfter, loadCoderSessionAfterError :=
		conversationStore.LoadConversation(workingDirectory, "harness-agent-intcoder")
	if loadCoderSessionAfterError != nil {
		t.Fatalf("第二次完成后读取 intcoder 会话失败: %v", loadCoderSessionAfterError)
	}
	if len(coderSessionAfter.Messages) <= messageCountBefore {
		t.Fatalf(
			"同名复用记忆：消息数应增长（之前 %d，之后 %d）",
			messageCountBefore,
			len(coderSessionAfter.Messages),
		)
	}
	if coderTaskCount := func() int {
		agentRecord, _ := integrationRuntime.AgentRegistry().GetAgent("intcoder")
		return agentRecord.TaskCount
	}(); coderTaskCount != 2 {
		t.Fatalf("intcoder 任务数应为 2，实际 %d", coderTaskCount)
	}
}

// waitForCondition 每 2 秒检查一次条件，超时失败。
func waitForCondition(
	t *testing.T,
	deadline time.Duration,
	description string,
	condition func() bool,
) {
	t.Helper()
	deadlineTime := time.Now().Add(deadline)
	for time.Now().Before(deadlineTime) {
		if condition() {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("等待超时：%s", description)
}
