package harness

import (
	"errors"
	"strings"
	"testing"

	"cc-agent-go/agent"
)

func TestBuildManagedAgentFinalReportFailureIncludesExecutionTrail(t *testing.T) {
	testCases := []struct {
		testName       string
		runResult      agent.AgentRunResult
		runAgentError  error
		trailText      string
		expectContains []string
	}{
		{
			testName:      "错误 + 轨迹",
			runResult:     nil,
			runAgentError: errors.New("模型调用超时"),
			trailText:     "[第 1 轮开始][调用工具 rg][文本增量部分]",
			expectContains: []string{
				"Status: 失败", "模型调用超时",
				"本次执行进度上下文", "调用工具 rg", "文本增量部分",
			},
		},
		{
			testName:      "无正文结果 + 轨迹",
			runResult:     agent.AgentMaximumRoundsReachedResult{PartialText: ""},
			runAgentError: nil,
			trailText:     "[第 1 轮开始][调用工具 bash][工具 bash 成功]",
			expectContains: []string{
				"Status: 失败（无正文）", "本次执行进度上下文",
				"调用工具 bash", "工具 bash 成功",
			},
		},
		{
			testName:      "无正文结果 + 空轨迹",
			runResult:     agent.AgentMaximumRoundsReachedResult{PartialText: ""},
			runAgentError: nil,
			trailText:     "",
			expectContains: []string{
				"Status: 失败（无正文）", "Task 原文",
			},
		},
		{
			testName:      "正常完成 + 轨迹（轨迹可选附带）",
			runResult:     agent.AgentCompletedResult{Text: "完成了"},
			runAgentError: nil,
			trailText:     "[第 1 轮开始]",
			expectContains: []string{
				"Status: 完成", "完成了", "本次执行进度上下文",
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.testName, func(t *testing.T) {
			trail := newAgentExecutionTrail()
			trail.recordText(testCase.trailText)
			report := buildManagedAgentFinalReport(
				ManagedAgentRecord{Name: "编码员", Kind: AgentKindResident},
				"写一个登录接口",
				testCase.runResult,
				testCase.runAgentError,
				trail,
			)
			if strings.TrimSpace(report) == "" {
				t.Fatal("汇报正文不应为空")
			}
			for _, expectedPart := range testCase.expectContains {
				if !strings.Contains(report, expectedPart) {
					t.Fatalf("汇报应包含 %q，实际:\n%s", expectedPart, report)
				}
			}
		})
	}
}

func TestAgentExecutionTrailTrimsToBudget(t *testing.T) {
	trail := newAgentExecutionTrail()
	trail.maximumTotalRunes = 100
	for index := 0; index < 50; index++ {
		trail.recordText("0123456789") // 10 runes each
	}
	if trail.currentTotalRunes > 100 {
		t.Fatalf("轨迹应裁剪到预算内，实际 %d runes", trail.currentTotalRunes)
	}
	trailText := trail.text()
	if strings.TrimSpace(trailText) == "" {
		t.Fatal("裁剪后仍应有尾部内容")
	}
	// 裁剪后应保留最近的内容（尾部），旧内容被丢弃。
	if !strings.Contains(trailText, "0123456789") {
		t.Fatal("裁剪后应包含最近文本")
	}
}

func TestAgentExecutionTrailRecordsToolEvents(t *testing.T) {
	trail := newAgentExecutionTrail()
	trail.recordEvent(agent.AgentRoundStartedEvent{Round: 1})
	trail.recordEvent(agent.AgentToolStartedEvent{ToolName: "rg"})
	trail.recordEvent(agent.AgentToolSucceededEvent{ToolName: "rg"})
	trail.recordEvent(agent.AgentTextDeltaEvent{Text: "搜索完成"})

	trailText := trail.text()
	for _, expectedPart := range []string{"第 1 轮开始", "调用工具 rg", "工具 rg 成功", "搜索完成"} {
		if !strings.Contains(trailText, expectedPart) {
			t.Fatalf("轨迹应包含 %q，实际:\n%s", expectedPart, trailText)
		}
	}
}

// TestConsumeFinishedAgentsRetriesWhenLockHeld 固定「主管理对话持锁时，
// 消费者不阻塞、放回队列重试」的行为：锁释放后记录仍可被取出。
func TestConsumeFinishedAgentsRetriesWhenLockHeld(t *testing.T) {
	unitRuntime := newUnitTestRuntime(t, 3)
	// 模拟用户对话持有 harness 锁。
	unlockUserConversation :=
		unitRuntime.dependencies.ConversationExecutionLocks.LockConversation(
			HarnessConversationID,
		)

	// 模拟一条完成记录入队。
	if _, _, upsertError := unitRuntime.agentRegistry.UpsertAgent("writer"); upsertError != nil {
		t.Fatalf("UpsertAgent: %v", upsertError)
	}
	finishedRecord, _ := unitRuntime.agentRegistry.GetAgent("writer")
	unitRuntime.completionQueue <- finishedRecord

	// 消费者尝试 TryLock：应失败（锁被用户对话持有），记录留在队列。
	unlockConsumer, locked :=
		unitRuntime.dependencies.ConversationExecutionLocks.TryLockConversation(
			HarnessConversationID,
		)
	if locked {
		unlockConsumer()
		t.Fatal("用户对话持锁时消费者 TryLock 不应成功")
	}

	// 锁释放后，队列里仍有那条记录。
	unlockUserConversation()
	select {
	case retriedRecord := <-unitRuntime.completionQueue:
		if retriedRecord.Name != "writer" {
			t.Fatalf("重试取出的记录 = %q，期望 writer", retriedRecord.Name)
		}
	default:
		t.Fatal("锁释放后队列应仍保留未汇报的记录")
	}
}
