package harness

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// HarnessConversationID 是 Harness 自己的固定会话 ID。
const HarnessConversationID = "harness"

// DefaultMaximumAgents 是 Agent 池容量默认值（MAX_HARNESS_AGENTS 未配置时）。
const DefaultMaximumAgents = 11

const managedAgentConversationIDPrefix = "harness-agent-"
const harnessDirectoryName = "harness"
const agentRegistryFileName = "agents.json"
const appsDirectoryName = "apps"
const appConfigurationFileName = "app.json"

var unsafeSlugCharacterPattern = regexp.MustCompile(`[^a-z0-9_-]+`)

// SlugForAgentName 把任意 Agent 名称清洗为安全 slug：
// 小写后仅保留 [a-z0-9-_]，其余字符折叠为一个 - 并去除首尾 -；
// 结果为空（例如纯中文名称）时回退为 agent-<fallbackIndex>。
func SlugForAgentName(agentName string, fallbackIndex int) string {
	cleanedSlug := unsafeSlugCharacterPattern.ReplaceAllString(
		strings.ToLower(strings.TrimSpace(agentName)),
		"-",
	)
	cleanedSlug = strings.Trim(cleanedSlug, "-")
	if cleanedSlug == "" {
		if fallbackIndex < 1 {
			fallbackIndex = 1
		}
		return fmt.Sprintf("agent-%d", fallbackIndex)
	}
	return cleanedSlug
}

// ManagedAgentConversationID 由 slug 生成被管理 Agent 的会话 ID。
func ManagedAgentConversationID(agentSlug string) string {
	return managedAgentConversationIDPrefix + agentSlug
}

// HarnessDirectoryPath 返回 <workingDirectory>/.cc-agent/harness。
func HarnessDirectoryPath(workingDirectory string) (string, error) {
	if !filepath.IsAbs(workingDirectory) {
		return "", fmt.Errorf("workingDirectory 必须是绝对路径")
	}
	return filepath.Join(workingDirectory, ".cc-agent", harnessDirectoryName), nil
}

// AppsDirectoryPath 返回 <workingDirectory>/.cc-agent/apps（应用目录）。
func AppsDirectoryPath(workingDirectory string) (string, error) {
	if !filepath.IsAbs(workingDirectory) {
		return "", fmt.Errorf("workingDirectory 必须是绝对路径")
	}
	return filepath.Join(workingDirectory, ".cc-agent", appsDirectoryName), nil
}

// AgentRegistryFilePath 返回 Agent 注册表文件 agents.json 的路径。
func AgentRegistryFilePath(workingDirectory string) (string, error) {
	harnessDirectoryPath, buildHarnessDirectoryError :=
		HarnessDirectoryPath(workingDirectory)
	if buildHarnessDirectoryError != nil {
		return "", buildHarnessDirectoryError
	}
	return filepath.Join(harnessDirectoryPath, agentRegistryFileName), nil
}
