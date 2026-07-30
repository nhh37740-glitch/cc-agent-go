package tool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOneBashToolRunsInTwoSuppliedWorkingDirectories(t *testing.T) {
	firstWorkingDirectory := t.TempDir()
	secondWorkingDirectory := t.TempDir()
	_ = os.WriteFile(filepath.Join(firstWorkingDirectory, "value.txt"), []byte("first"), 0644)
	_ = os.WriteFile(filepath.Join(secondWorkingDirectory, "value.txt"), []byte("second"), 0644)
	bashTool := NewBashTool()
	for _, testCase := range []struct {
		workingDirectory string
		expectedText     string
	}{
		{firstWorkingDirectory, "first"},
		{secondWorkingDirectory, "second"},
	} {
		commandResult, executeCommandError := bashTool.Execute(
			map[string]any{"command": "cat value.txt"},
			ToolExecutionEnvironment{WorkingDirectory: testCase.workingDirectory},
		)
		if executeCommandError != nil {
			t.Fatal(executeCommandError)
		}
		if !strings.HasSuffix(strings.TrimSpace(commandResult), testCase.expectedText) {
			t.Fatalf("result=%q want=%q", commandResult, testCase.expectedText)
		}
	}
}
