package agent

import (
	"strings"
	"testing"

	"cc-agent-go/memory"
	"cc-agent-go/model"
)

// seedConversationForRecentTest 向测试目录写入带摘要的会话。
func seedConversationForRecentTest(
	t *testing.T,
	projectStore *memory.ProjectConversationStore,
	workingDirectory string,
) {
	t.Helper()
	seededConversation := &model.SessionJson{
		ConversationId: "recent-test",
		Messages: []model.Message{
			{
				Role: "assistant",
				Content: []model.MessageContentBlock{
					model.TextContentBlock{Text: "[历史会话摘要] 早期的关键决策：用 Go 实现"},
				},
			},
			{
				Role: "user",
				Content: []model.MessageContentBlock{
					model.TextContentBlock{Text: "第一轮问题"},
				},
			},
			{
				Role: "assistant",
				Content: []model.MessageContentBlock{
					model.TextContentBlock{Text: "第一轮回复"},
				},
			},
			{
				Role: "user",
				Content: []model.MessageContentBlock{
					model.TextContentBlock{Text: "第二轮问题"},
				},
			},
			{
				Role: "assistant",
				Content: []model.MessageContentBlock{
					model.TextContentBlock{Text: "第二轮回复"},
				},
			},
		},
	}
	if saveError := projectStore.SaveConversation(
		workingDirectory,
		seededConversation,
	); saveError != nil {
		t.Fatalf("写入测试会话失败: %v", saveError)
	}
}

func recentTestEnvironment(workingDirectory string) AgentExecutionEnvironment {
	return AgentExecutionEnvironment{
		WorkingDirectory: workingDirectory,
		ConversationID:   "recent-test",
	}
}

func TestLoadRecentConversationNoSessionFile(t *testing.T) {
	projectStore := memory.NewProjectConversationStore()
	workingDirectory := t.TempDir()

	historyMessages, loadError := loadRecentConversation(
		recentTestEnvironment(workingDirectory),
		projectStore,
		20000,
		exactRuneTestTokenCounter{},
	)
	if loadError != nil {
		t.Fatalf("loadRecentConversation: %v", loadError)
	}
	if len(historyMessages) != 0 {
		t.Fatalf("无会话时应返回空列表，实际 %d 条", len(historyMessages))
	}
}

func TestLoadRecentConversationKeepsSummaryAndRecent(t *testing.T) {
	projectStore := memory.NewProjectConversationStore()
	workingDirectory := t.TempDir()
	seedConversationForRecentTest(t, projectStore, workingDirectory)

	// 预算足够大：摘要 + 全部消息都带上。
	historyMessages, loadError := loadRecentConversation(
		recentTestEnvironment(workingDirectory),
		projectStore,
		20000,
		exactRuneTestTokenCounter{},
	)
	if loadError != nil {
		t.Fatalf("loadRecentConversation: %v", loadError)
	}
	if len(historyMessages) != 5 {
		t.Fatalf("大预算应返回全部 5 条（摘要 + 4 对话），实际 %d 条", len(historyMessages))
	}
	if !strings.HasPrefix(historyMessages[0].Content[0].(model.TextContentBlock).Text, "[历史会话摘要]") {
		t.Fatalf("第一条应为历史摘要，实际 %q",
			historyMessages[0].Content[0].(model.TextContentBlock).Text)
	}
}

func TestLoadRecentConversationTrimsToBudget(t *testing.T) {
	projectStore := memory.NewProjectConversationStore()
	workingDirectory := t.TempDir()
	seedConversationForRecentTest(t, projectStore, workingDirectory)

	// 预算 11：摘要不计入；从最新往回，第二轮两条（各 5 rune）共 10 ≤ 11，
	// 第一轮两条超出，被裁剪。
	historyMessages, loadError := loadRecentConversation(
		recentTestEnvironment(workingDirectory),
		projectStore,
		11,
		exactRuneTestTokenCounter{},
	)
	if loadError != nil {
		t.Fatalf("loadRecentConversation: %v", loadError)
	}
	if len(historyMessages) != 3 {
		t.Fatalf("预算裁剪后应为 3 条（摘要 + 第二轮两条），实际 %d 条: %+v",
			len(historyMessages), historyMessages)
	}
	if !strings.Contains(historyMessages[1].Content[0].(model.TextContentBlock).Text, "第二轮") {
		t.Fatalf("裁剪后最近消息应保留第二轮，实际 %+v", historyMessages)
	}
}

func TestLoadRecentConversationZeroBudgetReturnsNothing(t *testing.T) {
	projectStore := memory.NewProjectConversationStore()
	workingDirectory := t.TempDir()
	seedConversationForRecentTest(t, projectStore, workingDirectory)

	historyMessages, loadError := loadRecentConversation(
		recentTestEnvironment(workingDirectory),
		projectStore,
		0,
		exactRuneTestTokenCounter{},
	)
	if loadError != nil {
		t.Fatalf("loadRecentConversation: %v", loadError)
	}
	if len(historyMessages) != 0 {
		t.Fatalf("预算为 0 时应不装配历史，实际 %d 条", len(historyMessages))
	}
}
