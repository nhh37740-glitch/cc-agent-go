package harness

import (
	"encoding/json"
	"strings"
	"testing"

	"cc-agent-go/model"
)

// seedHarnessSession 向测试目录写入一份 Harness 会话文件。
func seedHarnessSession(t *testing.T, unitRuntime *Runtime) {
	t.Helper()
	seededConversation := &model.SessionJson{
		ConversationId: HarnessConversationID,
		Title:          "测试 Harness 会话",
		Messages: []model.Message{
			{
				Role: "user",
				Content: []model.MessageContentBlock{
					model.TextContentBlock{Text: "帮我实现元老院应用"},
				},
			},
			{
				Role: "assistant",
				Content: []model.MessageContentBlock{
					model.TextContentBlock{Text: "我派 coding Agent 去实现 council"},
					model.ToolUseContentBlock{ID: "t1", Name: "agent"},
				},
			},
			{
				Role: "user",
				Content: []model.MessageContentBlock{
					model.TextContentBlock{Text: "进度如何"},
				},
			},
			{
				Role: "assistant",
				Content: []model.MessageContentBlock{
					model.TextContentBlock{Text: "还在运行中"},
					model.ToolResultContentBlock{ToolUseID: "t1", Content: "running"},
				},
			},
		},
	}
	if saveError := unitRuntime.dependencies.ConversationStore.SaveConversation(
		unitRuntime.workingDirectory,
		seededConversation,
	); saveError != nil {
		t.Fatalf("写入测试会话失败: %v", saveError)
	}
}

type memoryToolResult struct {
	ConversationID string `json:"conversationId"`
	Title          string `json:"title"`
	MessageCount   int    `json:"messageCount"`
	ReturnedCount  int    `json:"returnedCount"`
	Messages       []struct {
		Role string `json:"role"`
		Text string `json:"text"`
	} `json:"messages"`
}

func executeMemoryTool(
	t *testing.T,
	unitRuntime *Runtime,
	toolArguments map[string]any,
) memoryToolResult {
	t.Helper()
	memoryToolUnderTest := newHarnessMemoryTool(unitRuntime)
	memoryResultJSON, executeError := memoryToolUnderTest.Execute(
		toolArguments,
		unitTestExecutionEnvironment(unitRuntime.workingDirectory),
	)
	if executeError != nil {
		t.Fatalf("memory 工具执行失败: %v", executeError)
	}
	var memoryResult memoryToolResult
	if decodeError := json.Unmarshal(
		[]byte(memoryResultJSON),
		&memoryResult,
	); decodeError != nil {
		t.Fatalf("memory 结果不是 JSON: %v", decodeError)
	}
	return memoryResult
}

func TestMemoryToolReturnsRecentMessages(t *testing.T) {
	unitRuntime := newUnitTestRuntime(t, 3)
	seedHarnessSession(t, unitRuntime)

	memoryResult := executeMemoryTool(
		t,
		unitRuntime,
		map[string]any{"recentMessages": float64(2)},
	)
	if memoryResult.Title != "测试 Harness 会话" {
		t.Fatalf("标题 = %q，期望 %q", memoryResult.Title, "测试 Harness 会话")
	}
	if memoryResult.MessageCount != 4 {
		t.Fatalf("消息总数 = %d，期望 4", memoryResult.MessageCount)
	}
	if memoryResult.ReturnedCount != 2 {
		t.Fatalf("返回消息数 = %d，期望 2", memoryResult.ReturnedCount)
	}
	if !strings.Contains(memoryResult.Messages[1].Text, "还在运行中") {
		t.Fatalf("最近一条应是'还在运行中'，实际: %q", memoryResult.Messages[1].Text)
	}
	if !strings.Contains(memoryResult.Messages[1].Text, "[工具结果]") {
		t.Fatalf("工具结果块应保留占位，实际: %q", memoryResult.Messages[1].Text)
	}
}

func TestMemoryToolDefaultsToTwentyMessages(t *testing.T) {
	unitRuntime := newUnitTestRuntime(t, 3)
	seedHarnessSession(t, unitRuntime)

	memoryResult := executeMemoryTool(t, unitRuntime, map[string]any{})
	if memoryResult.ReturnedCount != 4 {
		t.Fatalf("默认 20 条时应返回全部 4 条，实际 %d", memoryResult.ReturnedCount)
	}
}

func TestMemoryToolPatternFiltersMessages(t *testing.T) {
	unitRuntime := newUnitTestRuntime(t, 3)
	seedHarnessSession(t, unitRuntime)

	memoryResult := executeMemoryTool(
		t,
		unitRuntime,
		map[string]any{"pattern": "council|元老院"},
	)
	if memoryResult.ReturnedCount != 2 {
		t.Fatalf("pattern 应匹配 2 条消息，实际 %d", memoryResult.ReturnedCount)
	}
	for _, matchedMessage := range memoryResult.Messages {
		if !strings.Contains(matchedMessage.Text, "council") &&
			!strings.Contains(matchedMessage.Text, "元老院") {
			t.Fatalf("匹配消息应包含 council 或元老院，实际: %q", matchedMessage.Text)
		}
	}
}

func TestMemoryToolRejectsInvalidPattern(t *testing.T) {
	unitRuntime := newUnitTestRuntime(t, 3)
	seedHarnessSession(t, unitRuntime)

	memoryToolUnderTest := newHarnessMemoryTool(unitRuntime)
	_, executeError := memoryToolUnderTest.Execute(
		map[string]any{"pattern": "([invalid"},
		unitTestExecutionEnvironment(unitRuntime.workingDirectory),
	)
	if executeError == nil || !strings.Contains(executeError.Error(), "pattern") {
		t.Fatalf("无效正则应报含 pattern 的错误，实际: %v", executeError)
	}
}

func TestMemoryToolRejectsInvalidRecentMessages(t *testing.T) {
	unitRuntime := newUnitTestRuntime(t, 3)
	seedHarnessSession(t, unitRuntime)

	memoryToolUnderTest := newHarnessMemoryTool(unitRuntime)
	_, executeError := memoryToolUnderTest.Execute(
		map[string]any{"recentMessages": "二十"},
		unitTestExecutionEnvironment(unitRuntime.workingDirectory),
	)
	if executeError == nil ||
		!strings.Contains(executeError.Error(), "recentMessages") {
		t.Fatalf(
			"非法 recentMessages 应报含 recentMessages 的错误，实际: %v",
			executeError,
		)
	}
}

// TestMemoryToolWithoutSessionFile 回归测试：Harness 会话文件尚未创建时
// 调用 memory 工具不应崩溃，应返回空记忆结果。
func TestMemoryToolWithoutSessionFile(t *testing.T) {
	unitRuntime := newUnitTestRuntime(t, 3)

	memoryResult := executeMemoryTool(t, unitRuntime, map[string]any{})
	if memoryResult.MessageCount != 0 {
		t.Fatalf("空会话 messageCount = %d，期望 0", memoryResult.MessageCount)
	}
	if memoryResult.ReturnedCount != 0 {
		t.Fatalf("空会话 returnedCount = %d，期望 0", memoryResult.ReturnedCount)
	}
}
