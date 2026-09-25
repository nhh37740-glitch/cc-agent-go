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

// heartbeatStaleAfterSeconds 超过该秒数没有心跳即视为可能卡死。
const heartbeatStaleAfterSeconds = 300.0

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

// HarnessManagerAgentsFilePath 返回主管理专属 AGENTS.md。
func HarnessManagerAgentsFilePath(workingDirectory string) (string, error) {
	harnessDirectoryPath, buildHarnessDirectoryError :=
		HarnessDirectoryPath(workingDirectory)
	if buildHarnessDirectoryError != nil {
		return "", buildHarnessDirectoryError
	}
	return filepath.Join(harnessDirectoryPath, "AGENTS.md"), nil
}

// SharedDirectoryPath 返回共享目录 `.cc-agent/harness/shared`。
func SharedDirectoryPath(workingDirectory string) (string, error) {
	harnessDirectoryPath, buildHarnessDirectoryError :=
		HarnessDirectoryPath(workingDirectory)
	if buildHarnessDirectoryError != nil {
		return "", buildHarnessDirectoryError
	}
	return filepath.Join(harnessDirectoryPath, "shared"), nil
}

// ResidentDirectoryPath 返回常驻 Agent 目录。
func ResidentDirectoryPath(workingDirectory string, residentSlug string) (string, error) {
	harnessDirectoryPath, buildHarnessDirectoryError :=
		HarnessDirectoryPath(workingDirectory)
	if buildHarnessDirectoryError != nil {
		return "", buildHarnessDirectoryError
	}
	return filepath.Join(harnessDirectoryPath, "residents", residentSlug), nil
}

// RelativeHarnessPath 把绝对路径转为相对项目根的 slash 路径。
func RelativeHarnessPath(workingDirectory string, absolutePath string) (string, error) {
	relativePath, relError := filepath.Rel(workingDirectory, absolutePath)
	if relError != nil {
		return "", relError
	}
	return filepath.ToSlash(relativePath), nil
}
