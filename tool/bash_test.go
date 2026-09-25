package tool

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNativeCommandToolRunsInSuppliedWorkingDirectories(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("command tool is Windows-only")
	}
	firstWorkingDirectory := t.TempDir()
	secondWorkingDirectory := t.TempDir()
	_ = os.WriteFile(filepath.Join(firstWorkingDirectory, "value.txt"), []byte("first-match"), 0644)
	_ = os.WriteFile(filepath.Join(secondWorkingDirectory, "value.txt"), []byte("second-match"), 0644)
	commandTool := NewNativeCommandTool()

	for _, testCase := range []struct {
		workingDirectory string
		expectedText     string
	}{
		{firstWorkingDirectory, "first-match"},
		{secondWorkingDirectory, "second-match"},
	} {
		commandResult, executeCommandError := commandTool.Execute(
			map[string]any{
				"program": "rg",
				"args":    []string{"-n", "match", "value.txt"},
			},
			ToolExecutionEnvironment{WorkingDirectory: testCase.workingDirectory},
		)
		if executeCommandError != nil {
			t.Fatal(executeCommandError)
		}
		if !strings.Contains(commandResult, "status:") {
			t.Fatalf("missing structured status in result=%q", commandResult)
		}
		if !strings.Contains(commandResult, testCase.expectedText) {
			t.Fatalf("result=%q want contains %q", commandResult, testCase.expectedText)
		}
	}
}

func TestNativeCommandToolRejectsUnknownProgram(t *testing.T) {
	commandTool := NewNativeCommandTool()
	_, executeCommandError := commandTool.Execute(
		map[string]any{
			"program": "not-a-real-program",
			"args":    []string{},
		},
		ToolExecutionEnvironment{WorkingDirectory: t.TempDir()},
	)
	if executeCommandError == nil {
		t.Fatal("expected unknown program error")
	}
	if !strings.Contains(executeCommandError.Error(), "不在白名单") {
		t.Fatalf("error=%v", executeCommandError)
	}
}

func TestNativeCommandToolHonorsParentContextCancel(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("command tool is Windows-only")
	}
	commandTool := NewNativeCommandTool()
	parentContext, cancelParent := context.WithCancel(context.Background())
	cancelParent()

	_, executeCommandError := commandTool.Execute(
		map[string]any{
			"program": "go",
			"args":    []string{"env", "GOVERSION"},
		},
		ToolExecutionEnvironment{
			WorkingDirectory: t.TempDir(),
			Context:          parentContext,
		},
	)
	if executeCommandError == nil {
		t.Fatal("expected cancellation error")
	}
	errorText := strings.ToLower(executeCommandError.Error())
	if !strings.Contains(executeCommandError.Error(), "取消") &&
		!strings.Contains(errorText, "cancel") {
		t.Fatalf("error=%v", executeCommandError)
	}
}
