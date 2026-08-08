package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiveStatusMCPSectionWithoutProvider(t *testing.T) {
	unitRuntime := newUnitTestRuntime(t, 11)

	prompt := unitRuntime.HarnessSystemPromptWithLiveStatus()
	if !strings.Contains(prompt, "没有运行中的 MCP Server") {
		t.Fatalf("无 MCP 提供者时应说明没有 MCP 资源，实际:\n%s", prompt)
	}
}

func TestLiveStatusMCPSectionWithRunningServers(t *testing.T) {
	unitRuntime := newUnitTestRuntime(t, 11)
	unitRuntime.dependencies.MCPLiveStatusText = func() string {
		return "- playwright：工具 mcp_playwright__browser_navigate、" +
			"mcp_playwright__browser_click"
	}

	prompt := unitRuntime.HarnessSystemPromptWithLiveStatus()
	for _, expectedPart := range []string{
		"playwright", "mcp_playwright__browser_navigate",
	} {
		if !strings.Contains(prompt, expectedPart) {
			t.Fatalf("有 MCP Server 时实况应包含 %q，实际:\n%s", expectedPart, prompt)
		}
	}
}

func TestLiveStatusAppsSectionWithoutApps(t *testing.T) {
	unitRuntime := newUnitTestRuntime(t, 11)

	prompt := unitRuntime.HarnessSystemPromptWithLiveStatus()
	if !strings.Contains(prompt, "没有已创建的应用") {
		t.Fatalf("无应用时应说明没有应用，实际:\n%s", prompt)
	}
}

func TestLiveStatusAppsSectionWithApps(t *testing.T) {
	unitRuntime := newUnitTestRuntime(t, 11)
	appsDirectoryPath, buildAppsPathError :=
		AppsDirectoryPath(unitRuntime.workingDirectory)
	if buildAppsPathError != nil {
		t.Fatalf("计算应用目录失败: %v", buildAppsPathError)
	}
	// 含 app.json 的目录才算应用；不含的目录被忽略。
	councilDirectory := filepath.Join(appsDirectoryPath, "council")
	if makeDirectoryError := os.MkdirAll(councilDirectory, 0o755); makeDirectoryError != nil {
		t.Fatalf("创建应用目录失败: %v", makeDirectoryError)
	}
	if writeAppConfigurationError := os.WriteFile(
		filepath.Join(councilDirectory, "app.json"),
		[]byte(`{"name":"council"}`),
		0o644,
	); writeAppConfigurationError != nil {
		t.Fatalf("写入 app.json 失败: %v", writeAppConfigurationError)
	}
	if makeDirectoryError := os.MkdirAll(
		filepath.Join(appsDirectoryPath, "not-an-app"),
		0o755,
	); makeDirectoryError != nil {
		t.Fatalf("创建无 app.json 目录失败: %v", makeDirectoryError)
	}

	prompt := unitRuntime.HarnessSystemPromptWithLiveStatus()
	if !strings.Contains(prompt, "council") ||
		!strings.Contains(prompt, "/apps/council/") {
		t.Fatalf("有应用时实况应包含应用名和页面地址，实际:\n%s", prompt)
	}
	if strings.Contains(prompt, "not-an-app") {
		t.Fatalf("无 app.json 的目录不应出现在实况里，实际:\n%s", prompt)
	}
}
