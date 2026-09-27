package tool

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLinuxCommandToolRunsInSelectedWorkspace(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux command adapter test")
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "value.txt"), []byte("portable-match\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := NewNativeCommandTool().Execute(
		map[string]any{"program": "rg", "args": []string{"portable-match", "value.txt"}},
		ToolExecutionEnvironment{WorkingDirectory: workspace},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "status: ok") || !strings.Contains(result, "portable-match") {
		t.Fatalf("unexpected command result: %q", result)
	}
}

func TestLinuxCommandToolRejectsShell(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux command adapter test")
	}
	_, err := NewNativeCommandTool().Execute(
		map[string]any{"program": "sh", "args": []string{"-c", "echo unwanted"}},
		ToolExecutionEnvironment{WorkingDirectory: t.TempDir()},
	)
	if err == nil || !strings.Contains(err.Error(), "已禁用") {
		t.Fatalf("expected shell rejection, got %v", err)
	}
}
