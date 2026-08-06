package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSlugForAgentName 固定 slug 清洗规则（任务 2.3）：
// 小写保留 [a-z0-9-_]，其余折叠为 -，空结果回退 agent-<序号>。
func TestSlugForAgentName(t *testing.T) {
	testCases := []struct {
		agentName     string
		fallbackIndex int
		expectedSlug  string
	}{
		{"Coder", 1, "coder"},
		{"browser dev", 1, "browser-dev"},
		{"元老一", 1, "agent-1"},
		{"!!", 3, "agent-3"},
		{"a/b\\c", 1, "a-b-c"},
		{"  spaced  ", 2, "spaced"},
	}
	for _, testCase := range testCases {
		actualSlug := SlugForAgentName(testCase.agentName, testCase.fallbackIndex)
		if actualSlug != testCase.expectedSlug {
			t.Fatalf("SlugForAgentName(%q, %d) = %q，期望 %q",
				testCase.agentName, testCase.fallbackIndex,
				actualSlug, testCase.expectedSlug)
		}
	}
}

// TestAgentRegistryUpsertAndReload 固定同名 upsert 和重启后重载（任务 2.3）。
func TestAgentRegistryUpsertAndReload(t *testing.T) {
	workingDirectory := t.TempDir()
	agentRegistry, loadError := LoadAgentRegistry(workingDirectory, 11)
	if loadError != nil {
		t.Fatalf("LoadAgentRegistry: %v", loadError)
	}

	createdRecord, created, upsertError := agentRegistry.UpsertAgent("编码员")
	if upsertError != nil {
		t.Fatalf("UpsertAgent: %v", upsertError)
	}
	if !created {
		t.Fatal("第一次 UpsertAgent 应返回 created=true")
	}
	if createdRecord.ConversationID != "harness-agent-agent-1" {
		t.Fatalf("中文名称会话 ID = %q，期望 harness-agent-agent-1",
			createdRecord.ConversationID)
	}

	reloadedRecord, createdAgain, upsertAgainError :=
		agentRegistry.UpsertAgent("编码员")
	if upsertAgainError != nil {
		t.Fatalf("同名 UpsertAgent: %v", upsertAgainError)
	}
	if createdAgain {
		t.Fatal("同名 UpsertAgent 不应再次创建")
	}
	if reloadedRecord.ConversationID != createdRecord.ConversationID {
		t.Fatalf("同名会话 ID 变化：%q → %q",
			createdRecord.ConversationID, reloadedRecord.ConversationID)
	}
	if agentRegistry.AgentCount() != 1 {
		t.Fatalf("同名 upsert 后 Agent 数 = %d，期望 1", agentRegistry.AgentCount())
	}

	// 模拟服务重启：重新从磁盘加载，记录必须延续。
	reloadedRegistry, reloadError := LoadAgentRegistry(workingDirectory, 11)
	if reloadError != nil {
		t.Fatalf("重新加载 LoadAgentRegistry: %v", reloadError)
	}
	persistedRecord, found := reloadedRegistry.GetAgent("编码员")
	if !found {
		t.Fatal("重启后注册表丢失了 Agent 记录")
	}
	if persistedRecord.ConversationID != createdRecord.ConversationID {
		t.Fatalf("重启后会话 ID = %q，期望 %q",
			persistedRecord.ConversationID, createdRecord.ConversationID)
	}
}

// TestAgentRegistrySeparateProjects 固定两个项目目录互不干扰（任务 2.3）。
func TestAgentRegistrySeparateProjects(t *testing.T) {
	firstDirectory := t.TempDir()
	secondDirectory := t.TempDir()

	firstRegistry, _ := LoadAgentRegistry(firstDirectory, 11)
	secondRegistry, _ := LoadAgentRegistry(secondDirectory, 11)
	if _, _, err := firstRegistry.UpsertAgent("coder"); err != nil {
		t.Fatalf("第一个项目 UpsertAgent: %v", err)
	}

	if firstRegistry.AgentCount() != 1 || secondRegistry.AgentCount() != 0 {
		t.Fatalf("项目间注册表互相干扰：%d / %d",
			firstRegistry.AgentCount(), secondRegistry.AgentCount())
	}
	firstFilePath, _ := AgentRegistryFilePath(firstDirectory)
	secondFilePath, _ := AgentRegistryFilePath(secondDirectory)
	if firstFilePath == secondFilePath {
		t.Fatal("两个项目的注册表文件路径相同")
	}
	if _, statError := os.Stat(firstFilePath); statError != nil {
		t.Fatalf("第一个项目的 agents.json 未写入: %v", statError)
	}
}

// TestAgentRegistryPoolLimitAndForget 固定池上限、满池错误和 forget（任务 2.3）。
func TestAgentRegistryPoolLimitAndForget(t *testing.T) {
	workingDirectory := t.TempDir()
	agentRegistry, _ := LoadAgentRegistry(workingDirectory, 2)

	if _, _, err := agentRegistry.UpsertAgent("alpha"); err != nil {
		t.Fatalf("UpsertAgent alpha: %v", err)
	}
	if _, _, err := agentRegistry.UpsertAgent("beta"); err != nil {
		t.Fatalf("UpsertAgent beta: %v", err)
	}

	_, _, poolFullError := agentRegistry.UpsertAgent("gamma")
	if poolFullError == nil {
		t.Fatal("满池时创建第三个 Agent 应返回错误")
	}
	for _, expectedContent := range []string{"上限 2", "alpha", "beta", "forget"} {
		if !strings.Contains(poolFullError.Error(), expectedContent) {
			t.Fatalf("满池错误 %q 缺少内容 %q", poolFullError, expectedContent)
		}
	}

	// forget 释放名额；会话文件必须保留。
	alphaRecord, _ := agentRegistry.GetAgent("alpha")
	sessionsDirectory := filepath.Join(workingDirectory, ".cc-agent", "sessions")
	if mkdirError := os.MkdirAll(sessionsDirectory, 0o755); mkdirError != nil {
		t.Fatalf("创建 sessions 目录: %v", mkdirError)
	}
	sessionFilePath := filepath.Join(
		sessionsDirectory,
		alphaRecord.ConversationID+".json",
	)
	if writeError := os.WriteFile(sessionFilePath, []byte("{}"), 0o644); writeError != nil {
		t.Fatalf("写入假会话文件: %v", writeError)
	}

	if _, forgetError := agentRegistry.ForgetAgent("alpha"); forgetError != nil {
		t.Fatalf("ForgetAgent: %v", forgetError)
	}
	if agentRegistry.AgentCount() != 1 {
		t.Fatalf("forget 后 Agent 数 = %d，期望 1", agentRegistry.AgentCount())
	}
	if _, statError := os.Stat(sessionFilePath); statError != nil {
		t.Fatalf("forget 删除了会话文件: %v", statError)
	}

	if _, created, err := agentRegistry.UpsertAgent("gamma"); err != nil || !created {
		t.Fatalf("forget 后新建 Agent 失败: created=%v err=%v", created, err)
	}
	if _, unknownForgetError := agentRegistry.ForgetAgent("ghost"); unknownForgetError == nil {
		t.Fatal("forget 不存在的名字应返回错误")
	}
}

// TestAgentRegistrySlugConflict 固定 slug 冲突时自动加后缀（任务 2.3）。
func TestAgentRegistrySlugConflict(t *testing.T) {
	workingDirectory := t.TempDir()
	agentRegistry, _ := LoadAgentRegistry(workingDirectory, 11)

	firstRecord, _, _ := agentRegistry.UpsertAgent("agent!")
	secondRecord, _, _ := agentRegistry.UpsertAgent("agent?")
	if firstRecord.Slug != "agent" {
		t.Fatalf("第一个 slug = %q，期望 agent", firstRecord.Slug)
	}
	if secondRecord.Slug != "agent-2" {
		t.Fatalf("冲突 slug = %q，期望 agent-2", secondRecord.Slug)
	}
	if firstRecord.ConversationID == secondRecord.ConversationID {
		t.Fatal("slug 冲突导致两个 Agent 共用会话 ID")
	}
}

// TestAgentRegistryMarkAndCollect 固定状态迁移和结果收取（任务 2.3）。
func TestAgentRegistryMarkAndCollect(t *testing.T) {
	workingDirectory := t.TempDir()
	agentRegistry, _ := LoadAgentRegistry(workingDirectory, 11)
	if _, _, err := agentRegistry.UpsertAgent("coder"); err != nil {
		t.Fatalf("UpsertAgent: %v", err)
	}

	if err := agentRegistry.MarkRunning("coder"); err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}
	if collected, _ := agentRegistry.CollectFinishedResults(); len(collected) != 0 {
		t.Fatalf("running 状态不应被收取，收取到 %d 条", len(collected))
	}

	if err := agentRegistry.MarkCompleted("coder", "我是 coder，任务完成"); err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}
	collected, collectError := agentRegistry.CollectFinishedResults()
	if collectError != nil {
		t.Fatalf("CollectFinishedResults: %v", collectError)
	}
	if len(collected) != 1 || collected[0].Result != "我是 coder，任务完成" {
		t.Fatalf("收取结果 = %+v", collected)
	}
	if collected[0].TaskCount != 1 {
		t.Fatalf("TaskCount = %d，期望 1", collected[0].TaskCount)
	}

	// 第二次收取为空；重新加载后已收取标记仍然保持。
	if collectedAgain, _ := agentRegistry.CollectFinishedResults(); len(collectedAgain) != 0 {
		t.Fatalf("重复收取应返回空，收取到 %d 条", len(collectedAgain))
	}
	reloadedRegistry, _ := LoadAgentRegistry(workingDirectory, 11)
	if collectedAfterReload, _ := reloadedRegistry.CollectFinishedResults(); len(collectedAfterReload) != 0 {
		t.Fatalf("重启后重复收取应返回空，收取到 %d 条", len(collectedAfterReload))
	}

	if err := agentRegistry.MarkFailed("ghost", os.ErrNotExist); err == nil {
		t.Fatal("对不存在的 Agent 标记状态应返回错误")
	}
}

func TestAgentRegistryMarkResultCollected(t *testing.T) {
	workingDirectory := t.TempDir()
	agentRegistry, _ := LoadAgentRegistry(workingDirectory, 11)
	if _, _, err := agentRegistry.UpsertAgent("coder"); err != nil {
		t.Fatalf("UpsertAgent: %v", err)
	}
	if err := agentRegistry.MarkCompleted("coder", "任务完成"); err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}

	// 消费者逐条标记后，CollectFinishedResults 不再返回这条记录；
	// 重启重载后标记仍然保持。
	if err := agentRegistry.MarkResultCollected("coder"); err != nil {
		t.Fatalf("MarkResultCollected: %v", err)
	}
	if collected, _ := agentRegistry.CollectFinishedResults(); len(collected) != 0 {
		t.Fatalf("逐条标记后批量收取应为空，收取到 %d 条", len(collected))
	}
	reloadedRegistry, _ := LoadAgentRegistry(workingDirectory, 11)
	reloadedRecord, found := reloadedRegistry.GetAgent("coder")
	if !found || !reloadedRecord.ResultCollected {
		t.Fatalf("重载后已收取标记应保持，found=%v record=%+v", found, reloadedRecord)
	}

	if err := agentRegistry.MarkResultCollected("ghost"); err == nil {
		t.Fatal("对不存在的 Agent 标记已收取应返回错误")
	}
}
