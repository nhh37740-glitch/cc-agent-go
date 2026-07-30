package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cc-agent-go/memory"
	"cc-agent-go/model"
	"cc-agent-go/tool"
)

type exactRuneTestTokenCounter struct{}

func (exactRuneTestTokenCounter) CountPreparedModelRequest(
	preparedModelRequest PreparedModelRequest,
) (int, error) {
	requestJSON, encodeRequestError := json.Marshal(preparedModelRequest)
	return len([]rune(string(requestJSON))), encodeRequestError
}

func (exactRuneTestTokenCounter) CountText(textToCount string) (int, error) {
	return len([]rune(textToCount)), nil
}

func (exactRuneTestTokenCounter) TruncateText(
	textToTruncate string,
	maximumTokens int,
) (TokenTruncationResult, error) {
	textRunes := []rune(textToTruncate)
	if len(textRunes) <= maximumTokens {
		return TokenTruncationResult{
			Text: textToTruncate, OriginalTokens: len(textRunes),
			TruncatedTokens: len(textRunes),
		}, nil
	}
	return TokenTruncationResult{
		Text: string(textRunes[:maximumTokens]), OriginalTokens: len(textRunes),
		TruncatedTokens: maximumTokens, WasTruncated: true,
	}, nil
}

func TestAgentFirstModelRequestContainsCurrentTaskAndMemoryPathWithoutOldText(
	t *testing.T,
) {
	workingDirectory := t.TempDir()
	conversationStore := memory.NewProjectConversationStore()
	_, saveOldConversationError := conversationStore.AppendConversationTurn(
		workingDirectory,
		"same-session",
		[]model.Message{{
			Role: "user",
			Content: []model.MessageContentBlock{
				model.TextContentBlock{Text: "OLD_SECRET_MUST_NOT_BE_SENT"},
			},
		}},
		1, 1, 1, 1000,
	)
	if saveOldConversationError != nil {
		t.Fatal(saveOldConversationError)
	}

	var firstModelRequest AgentModelCallRequest
	configuredAgent := newTestAgent(
		t,
		tool.NewRegistry(),
		conversationStore,
		func(modelCallRequest AgentModelCallRequest) (model.ApiResponse, error) {
			firstModelRequest = modelCallRequest
			return model.ApiResponse{
				Text: "current result", InputTokens: 20, OutputTokens: 4,
			}, nil
		},
		nil,
	)
	_, runAgentError := configuredAgent.Run(
		UserTaskInput{Message: "CURRENT_TASK_ONLY"},
		AgentExecutionEnvironment{
			WorkingDirectory: workingDirectory,
			ConversationID:   "same-session",
		},
	)
	if runAgentError != nil {
		t.Fatal(runAgentError)
	}
	requestJSON, _ := json.Marshal(firstModelRequest.Messages)
	if strings.Contains(string(requestJSON), "OLD_SECRET_MUST_NOT_BE_SENT") {
		t.Fatal("第一次模型请求包含旧会话正文")
	}
	if !strings.Contains(string(requestJSON), "CURRENT_TASK_ONLY") {
		t.Fatal("第一次模型请求缺少当前任务")
	}
	if !strings.Contains(firstModelRequest.SystemPrompt, ".cc-agent/sessions/same-session.json") {
		t.Fatal("system prompt 缺少会话文件位置")
	}
}

func TestOneAgentUsesTwoWorkingDirectoriesWithoutMixingToolOrMemoryData(
	t *testing.T,
) {
	firstWorkingDirectory := t.TempDir()
	secondWorkingDirectory := t.TempDir()
	_ = os.WriteFile(filepath.Join(firstWorkingDirectory, "project.txt"), []byte("first"), 0644)
	_ = os.WriteFile(filepath.Join(secondWorkingDirectory, "project.txt"), []byte("second"), 0644)

	registeredTools := tool.NewRegistry()
	if registerBashError := registeredTools.Register(tool.NewBashTool()); registerBashError != nil {
		t.Fatal(registerBashError)
	}
	modelCallNumber := 0
	configuredAgent := newTestAgent(
		t,
		registeredTools,
		memory.NewProjectConversationStore(),
		func(modelCallRequest AgentModelCallRequest) (model.ApiResponse, error) {
			modelCallNumber++
			if modelCallNumber%2 == 1 {
				return model.ApiResponse{
					ToolCalls: []model.ToolCall{{
						ID: "read-project", Name: "bash",
						Input: map[string]any{"command": "cat project.txt"},
					}},
					InputTokens: 10, OutputTokens: 2,
				}, nil
			}
			toolResult := messageToolResultText(
				modelCallRequest.Messages[len(modelCallRequest.Messages)-1],
			)
			return model.ApiResponse{
				Text: toolResult, InputTokens: 15, OutputTokens: 3,
			}, nil
		},
		nil,
	)
	firstResult, firstRunError := configuredAgent.Run(
		UserTaskInput{Message: "read first"},
		AgentExecutionEnvironment{
			WorkingDirectory: firstWorkingDirectory,
			ConversationID:   "shared-name",
		},
	)
	if firstRunError != nil || !strings.HasSuffix(
		strings.TrimSpace(firstResult.FinalText()),
		"first",
	) {
		t.Fatalf("first result=%q error=%v", firstResult.FinalText(), firstRunError)
	}
	secondResult, secondRunError := configuredAgent.Run(
		UserTaskInput{Message: "read second"},
		AgentExecutionEnvironment{
			WorkingDirectory: secondWorkingDirectory,
			ConversationID:   "shared-name",
		},
	)
	if secondRunError != nil || !strings.HasSuffix(
		strings.TrimSpace(secondResult.FinalText()),
		"second",
	) {
		t.Fatalf("second result=%q error=%v", secondResult.FinalText(), secondRunError)
	}
	for _, workingDirectory := range []string{firstWorkingDirectory, secondWorkingDirectory} {
		sessionPath := filepath.Join(
			workingDirectory, ".cc-agent", "sessions", "shared-name.json",
		)
		if _, readSessionError := os.Stat(sessionPath); readSessionError != nil {
			t.Fatalf("missing session %s: %v", sessionPath, readSessionError)
		}
	}
}

func TestAgentExecutesDynamicTerminalToolWithoutToolNameBranch(t *testing.T) {
	workingDirectory := t.TempDir()
	registeredTools := tool.NewRegistry()
	receivedWorkingDirectory := ""
	registerTerminalToolError := registeredTools.RegisterTerminalFunctionTool(
		"caller_chosen_terminal_tool",
		"finish current run",
		map[string]any{"type": "object"},
		func(
			_ map[string]any,
			executionEnvironment tool.ToolExecutionEnvironment,
		) (string, error) {
			receivedWorkingDirectory = executionEnvironment.WorkingDirectory
			return `{"status":"started"}`, nil
		},
	)
	if registerTerminalToolError != nil {
		t.Fatal(registerTerminalToolError)
	}
	configuredAgent := newTestAgent(
		t,
		registeredTools,
		memory.NewProjectConversationStore(),
		func(AgentModelCallRequest) (model.ApiResponse, error) {
			return model.ApiResponse{
				ToolCalls: []model.ToolCall{{
					ID: "terminal-1", Name: "caller_chosen_terminal_tool",
					Input: map[string]any{},
				}},
				InputTokens: 10, OutputTokens: 2,
			}, nil
		},
		nil,
	)
	runResult, runAgentError := configuredAgent.Run(
		HostedAgentTaskInput{Task: "start external work"},
		AgentExecutionEnvironment{
			WorkingDirectory: workingDirectory,
			ConversationID:   "hosted-task",
		},
	)
	if runAgentError != nil {
		t.Fatal(runAgentError)
	}
	if _, isTerminalResult := runResult.(AgentTerminalToolCompletedResult); !isTerminalResult {
		t.Fatalf("result type = %T", runResult)
	}
	if receivedWorkingDirectory != workingDirectory {
		t.Fatalf("tool working directory = %q", receivedWorkingDirectory)
	}
}

func TestAgentRejectsOversizedRequestBeforeCallingModel(t *testing.T) {
	workingDirectory := t.TempDir()
	modelCallCount := 0
	configuredAgent, createAgentError := NewAgent(
		AgentConfiguration{
			BaseSystemPrompt:          "test",
			MaximumRounds:             2,
			MaximumOutputTokens:       20,
			MaximumToolResultTokens:   20,
			MaximumStoredMemoryTokens: 1000,
			ModelContextWindowTokens:  40,
		},
		tool.NewRegistry(),
		memory.NewProjectConversationStore(),
		func(AgentModelCallRequest) (model.ApiResponse, error) {
			modelCallCount++
			return model.ApiResponse{}, nil
		},
		exactRuneTestTokenCounter{},
		nil,
	)
	if createAgentError != nil {
		t.Fatal(createAgentError)
	}
	_, runAgentError := configuredAgent.Run(
		UserTaskInput{Message: strings.Repeat("large task ", 20)},
		AgentExecutionEnvironment{
			WorkingDirectory: workingDirectory,
			ConversationID:   "context-check",
		},
	)
	if runAgentError == nil ||
		!strings.Contains(runAgentError.Error(), "无法再压缩") {
		t.Fatalf("error=%v", runAgentError)
	}
	if modelCallCount != 0 {
		t.Fatalf("model call count=%d want=0", modelCallCount)
	}
}

func TestAgentReturnsFinalTextWithMemorySaveFailedResult(t *testing.T) {
	workingDirectory := t.TempDir()
	configuredAgent := newTestAgent(
		t,
		tool.NewRegistry(),
		memory.NewProjectConversationStore(),
		func(AgentModelCallRequest) (model.ApiResponse, error) {
			if removeDirectoryError := os.RemoveAll(workingDirectory); removeDirectoryError != nil {
				return model.ApiResponse{}, removeDirectoryError
			}
			if createBlockingFileError := os.WriteFile(
				workingDirectory,
				[]byte("not a directory"),
				0644,
			); createBlockingFileError != nil {
				return model.ApiResponse{}, createBlockingFileError
			}
			return model.ApiResponse{
				Text: "model result survives", InputTokens: 4, OutputTokens: 3,
			}, nil
		},
		nil,
	)
	runResult, runAgentError := configuredAgent.Run(
		UserTaskInput{Message: "finish even if saving fails"},
		AgentExecutionEnvironment{
			WorkingDirectory: workingDirectory,
			ConversationID:   "save-failure",
		},
	)
	if runAgentError != nil {
		t.Fatal(runAgentError)
	}
	if runResult.FinalText() != "model result survives" {
		t.Fatalf("final text=%q", runResult.FinalText())
	}
	if _, isSaveFailed := runResult.MemorySaveResult().(AgentMemorySaveFailed); !isSaveFailed {
		t.Fatalf("memory result=%T", runResult.MemorySaveResult())
	}
}

func TestStoredMemoryCompressionFailurePreservesConversation(t *testing.T) {
	workingDirectory := t.TempDir()
	conversationStore := memory.NewProjectConversationStore()
	existingMessages := make([]model.Message, 0, 6)
	for messageIndex := 0; messageIndex < 6; messageIndex++ {
		existingMessages = append(existingMessages, model.Message{
			Role: "user",
			Content: []model.MessageContentBlock{
				model.TextContentBlock{Text: "existing memory"},
			},
		})
	}
	_, saveConversationError := conversationStore.AppendConversationTurn(
		workingDirectory, "compression-failure", existingMessages,
		1, 1, 1, 100000,
	)
	if saveConversationError != nil {
		t.Fatal(saveConversationError)
	}
	compressionFailureEventReceived := false
	modelCallCount := 0
	configuredAgent, createAgentError := NewAgent(
		AgentConfiguration{
			BaseSystemPrompt:          "test",
			MaximumRounds:             2,
			MaximumOutputTokens:       100,
			MaximumToolResultTokens:   100,
			MaximumStoredMemoryTokens: 10,
			ModelContextWindowTokens:  100000,
		},
		tool.NewRegistry(),
		conversationStore,
		func(AgentModelCallRequest) (model.ApiResponse, error) {
			modelCallCount++
			if modelCallCount == 1 {
				return model.ApiResponse{
					Text: "new result", InputTokens: 4, OutputTokens: 2,
				}, nil
			}
			return model.ApiResponse{}, errors.New("compression provider failed")
		},
		exactRuneTestTokenCounter{},
		func(receivedAgentEvent AgentEvent) {
			if _, isCompressionFailure :=
				receivedAgentEvent.(AgentStoredMemoryCompressionFailedEvent); isCompressionFailure {
				compressionFailureEventReceived = true
			}
		},
	)
	if createAgentError != nil {
		t.Fatal(createAgentError)
	}
	runResult, runAgentError := configuredAgent.Run(
		UserTaskInput{Message: "append this"},
		AgentExecutionEnvironment{
			WorkingDirectory: workingDirectory,
			ConversationID:   "compression-failure",
		},
	)
	if runAgentError != nil || runResult.FinalText() != "new result" {
		t.Fatalf("result=%v error=%v", runResult, runAgentError)
	}
	if !compressionFailureEventReceived {
		t.Fatal("compression failure event was not emitted")
	}
	reloadedConversation, reloadConversationError :=
		memory.NewProjectConversationStore().LoadConversation(
			workingDirectory,
			"compression-failure",
		)
	if reloadConversationError != nil || len(reloadedConversation.Messages) != 8 {
		t.Fatalf("reloaded conversation=%#v error=%v", reloadedConversation, reloadConversationError)
	}
}

func newTestAgent(
	t *testing.T,
	registeredTools *tool.Registry,
	conversationStore *memory.ProjectConversationStore,
	callModel AgentModelCallFunction,
	receiveEvent AgentEventReceiver,
) *Agent {
	t.Helper()
	configuredAgent, createAgentError := NewAgent(
		AgentConfiguration{
			BaseSystemPrompt:          "test",
			MaximumRounds:             4,
			MaximumOutputTokens:       100,
			MaximumToolResultTokens:   2000,
			MaximumStoredMemoryTokens: 100000,
			ModelContextWindowTokens:  100000,
		},
		registeredTools,
		conversationStore,
		callModel,
		exactRuneTestTokenCounter{},
		receiveEvent,
	)
	if createAgentError != nil {
		t.Fatal(createAgentError)
	}
	return configuredAgent
}

func messageToolResultText(message model.Message) string {
	for _, contentBlock := range message.Content {
		if toolResultContentBlock, isToolResult :=
			contentBlock.(model.ToolResultContentBlock); isToolResult {
			return toolResultContentBlock.Content
		}
	}
	return ""
}
