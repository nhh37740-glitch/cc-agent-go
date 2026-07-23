package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cc-agent-go/config"
	"cc-agent-go/model"
	"cc-agent-go/tool"
)

type recordedDeepSeekRequest struct {
	MaxTokens int              `json:"max_tokens"`
	System    string           `json:"system"`
	Messages  []model.Message  `json:"messages"`
	Tools     []map[string]any `json:"tools"`
}

func TestRunSubAgentStartsWithOnlyTheSuppliedTaskAndReturnsFinalText(t *testing.T) {
	var receivedDeepSeekRequest recordedDeepSeekRequest
	deepSeekTestServer := httptest.NewServer(http.HandlerFunc(
		func(responseWriter http.ResponseWriter, httpRequest *http.Request) {
			decodeRequestError := json.NewDecoder(httpRequest.Body).Decode(
				&receivedDeepSeekRequest,
			)
			if decodeRequestError != nil {
				t.Errorf("decode DeepSeek request: %v", decodeRequestError)
			}
			writeDeepSeekTextResponse(t, responseWriter, "任务完成")
		},
	))
	t.Cleanup(deepSeekTestServer.Close)

	subAgentFinalText, runSubAgentError := RunSubAgent(
		"只检查 config/config.go",
		newSubAgentTestConfig(deepSeekTestServer.URL),
		tool.NewRegistry(),
	)
	if runSubAgentError != nil {
		t.Fatalf("RunSubAgent: %v", runSubAgentError)
	}
	if subAgentFinalText != "任务完成" {
		t.Fatalf("final text = %q, want %q", subAgentFinalText, "任务完成")
	}
	if receivedDeepSeekRequest.MaxTokens != subAgentMaximumOutputTokens {
		t.Fatalf(
			"max_tokens = %d, want %d",
			receivedDeepSeekRequest.MaxTokens,
			subAgentMaximumOutputTokens,
		)
	}
	if receivedDeepSeekRequest.System != generalSubAgentSystemPrompt {
		t.Fatal("DeepSeek request did not receive the SubAgent system prompt")
	}
	if len(receivedDeepSeekRequest.Messages) != 1 {
		t.Fatalf(
			"message count = %d, want 1",
			len(receivedDeepSeekRequest.Messages),
		)
	}
	if firstSubAgentTaskText(receivedDeepSeekRequest.Messages) != "只检查 config/config.go" {
		t.Fatalf(
			"first user text = %q",
			firstSubAgentTaskText(receivedDeepSeekRequest.Messages),
		)
	}
}

func TestRunSubAgentExecutesToolAndSendsToolResultToNextRound(t *testing.T) {
	var deepSeekCallCount atomic.Int32
	var secondRoundDeepSeekRequest recordedDeepSeekRequest
	deepSeekTestServer := httptest.NewServer(http.HandlerFunc(
		func(responseWriter http.ResponseWriter, httpRequest *http.Request) {
			currentDeepSeekCall := deepSeekCallCount.Add(1)
			if currentDeepSeekCall == 1 {
				writeDeepSeekToolCallResponse(
					t,
					responseWriter,
					"tool-use-1",
					"read_test_file",
				)
				return
			}

			decodeRequestError := json.NewDecoder(httpRequest.Body).Decode(
				&secondRoundDeepSeekRequest,
			)
			if decodeRequestError != nil {
				t.Errorf("decode second DeepSeek request: %v", decodeRequestError)
			}
			writeDeepSeekTextResponse(t, responseWriter, "读取完成")
		},
	))
	t.Cleanup(deepSeekTestServer.Close)

	availableSubAgentTools := tool.NewRegistry()
	registerSubAgentTestTool(
		t,
		availableSubAgentTools,
		"read_test_file",
		func(toolArguments map[string]any) (string, error) {
			return "file content", nil
		},
	)

	subAgentFinalText, runSubAgentError := RunSubAgent(
		"读取测试文件",
		newSubAgentTestConfig(deepSeekTestServer.URL),
		availableSubAgentTools,
	)
	if runSubAgentError != nil {
		t.Fatalf("RunSubAgent: %v", runSubAgentError)
	}
	if subAgentFinalText != "读取完成" {
		t.Fatalf("final text = %q, want %q", subAgentFinalText, "读取完成")
	}

	toolResultText := findToolResultText(secondRoundDeepSeekRequest.Messages)
	if toolResultText != "file content" {
		t.Fatalf("tool result = %q, want %q", toolResultText, "file content")
	}
}

func TestRunSubAgentSendsToolErrorToNextRound(t *testing.T) {
	var deepSeekCallCount atomic.Int32
	var secondRoundDeepSeekRequest recordedDeepSeekRequest
	deepSeekTestServer := httptest.NewServer(http.HandlerFunc(
		func(responseWriter http.ResponseWriter, httpRequest *http.Request) {
			currentDeepSeekCall := deepSeekCallCount.Add(1)
			if currentDeepSeekCall == 1 {
				writeDeepSeekToolCallResponse(
					t,
					responseWriter,
					"tool-use-1",
					"missing_tool",
				)
				return
			}

			decodeRequestError := json.NewDecoder(httpRequest.Body).Decode(
				&secondRoundDeepSeekRequest,
			)
			if decodeRequestError != nil {
				t.Errorf("decode second DeepSeek request: %v", decodeRequestError)
			}
			writeDeepSeekTextResponse(t, responseWriter, "错误已处理")
		},
	))
	t.Cleanup(deepSeekTestServer.Close)

	_, runSubAgentError := RunSubAgent(
		"调用不存在的工具",
		newSubAgentTestConfig(deepSeekTestServer.URL),
		tool.NewRegistry(),
	)
	if runSubAgentError != nil {
		t.Fatalf("RunSubAgent: %v", runSubAgentError)
	}

	toolResultText := findToolResultText(secondRoundDeepSeekRequest.Messages)
	if !strings.Contains(toolResultText, "工具执行错误: 未知工具: missing_tool") {
		t.Fatalf("tool error result = %q", toolResultText)
	}
}

func TestRunSubAgentTruncatesLongToolResult(t *testing.T) {
	var deepSeekCallCount atomic.Int32
	var secondRoundDeepSeekRequest recordedDeepSeekRequest
	deepSeekTestServer := httptest.NewServer(http.HandlerFunc(
		func(responseWriter http.ResponseWriter, httpRequest *http.Request) {
			currentDeepSeekCall := deepSeekCallCount.Add(1)
			if currentDeepSeekCall == 1 {
				writeDeepSeekToolCallResponse(
					t,
					responseWriter,
					"tool-use-1",
					"long_result_tool",
				)
				return
			}

			decodeRequestError := json.NewDecoder(httpRequest.Body).Decode(
				&secondRoundDeepSeekRequest,
			)
			if decodeRequestError != nil {
				t.Errorf("decode second DeepSeek request: %v", decodeRequestError)
			}
			writeDeepSeekTextResponse(t, responseWriter, "长结果已处理")
		},
	))
	t.Cleanup(deepSeekTestServer.Close)

	availableSubAgentTools := tool.NewRegistry()
	registerSubAgentTestTool(
		t,
		availableSubAgentTools,
		"long_result_tool",
		func(toolArguments map[string]any) (string, error) {
			return strings.Repeat("x", subAgentMaximumToolResultCharacters+25), nil
		},
	)

	_, runSubAgentError := RunSubAgent(
		"读取长结果",
		newSubAgentTestConfig(deepSeekTestServer.URL),
		availableSubAgentTools,
	)
	if runSubAgentError != nil {
		t.Fatalf("RunSubAgent: %v", runSubAgentError)
	}

	toolResultText := findToolResultText(secondRoundDeepSeekRequest.Messages)
	if !strings.Contains(toolResultText, "原始长度 8025 字符") {
		t.Fatalf("truncated tool result = %q", toolResultText)
	}
	if !strings.HasPrefix(
		toolResultText,
		strings.Repeat("x", subAgentMaximumToolResultCharacters),
	) {
		t.Fatal("truncated tool result does not preserve the first 8000 characters")
	}
}

func TestRunSubAgentReturnsAgentLimitAfterTwelveRounds(t *testing.T) {
	var deepSeekCallCount atomic.Int32
	deepSeekTestServer := httptest.NewServer(http.HandlerFunc(
		func(responseWriter http.ResponseWriter, httpRequest *http.Request) {
			currentDeepSeekCall := deepSeekCallCount.Add(1)
			writeDeepSeekToolCallResponse(
				t,
				responseWriter,
				fmt.Sprintf("tool-use-%d", currentDeepSeekCall),
				"continue_tool",
			)
		},
	))
	t.Cleanup(deepSeekTestServer.Close)

	availableSubAgentTools := tool.NewRegistry()
	registerSubAgentTestTool(
		t,
		availableSubAgentTools,
		"continue_tool",
		func(toolArguments map[string]any) (string, error) {
			return "continue", nil
		},
	)

	_, runSubAgentError := RunSubAgent(
		"持续调用工具",
		newSubAgentTestConfig(deepSeekTestServer.URL),
		availableSubAgentTools,
	)
	if runSubAgentError == nil {
		t.Fatal("RunSubAgent returned nil error")
	}

	var applicationError *AppError
	if !errors.As(runSubAgentError, &applicationError) {
		t.Fatalf("error type = %T, want *AppError", runSubAgentError)
	}
	if applicationError.Kind != ErrorAgentLimit {
		t.Fatalf(
			"error kind = %q, want %q",
			applicationError.Kind,
			ErrorAgentLimit,
		)
	}
	if deepSeekCallCount.Load() != subAgentMaximumRounds {
		t.Fatalf(
			"DeepSeek call count = %d, want %d",
			deepSeekCallCount.Load(),
			subAgentMaximumRounds,
		)
	}
}

func TestRunSubAgentsInParallelStartsTogetherAndPreservesInputOrder(t *testing.T) {
	var startedRequestCount atomic.Int32
	allRequestsStarted := make(chan struct{})
	secondTaskResponseWritten := make(chan struct{})
	var closeAllRequestsStarted sync.Once
	var closeSecondTaskResponseWritten sync.Once

	deepSeekTestServer := httptest.NewServer(http.HandlerFunc(
		func(responseWriter http.ResponseWriter, httpRequest *http.Request) {
			var receivedDeepSeekRequest recordedDeepSeekRequest
			decodeRequestError := json.NewDecoder(httpRequest.Body).Decode(
				&receivedDeepSeekRequest,
			)
			if decodeRequestError != nil {
				t.Errorf("decode DeepSeek request: %v", decodeRequestError)
				return
			}

			if startedRequestCount.Add(1) == 3 {
				closeAllRequestsStarted.Do(func() {
					close(allRequestsStarted)
				})
			}

			select {
			case <-allRequestsStarted:
			case <-time.After(2 * time.Second):
				t.Error("three SubAgent requests did not start concurrently")
				return
			}

			taskText := firstSubAgentTaskText(receivedDeepSeekRequest.Messages)
			switch taskText {
			case "first task":
				select {
				case <-secondTaskResponseWritten:
				case <-time.After(2 * time.Second):
					t.Error("second task did not finish before first task")
					return
				}
				writeDeepSeekTextResponse(t, responseWriter, "first result")
			case "second task":
				writeDeepSeekTextResponse(t, responseWriter, "second result")
				closeSecondTaskResponseWritten.Do(func() {
					close(secondTaskResponseWritten)
				})
			case "third task":
				writeDeepSeekTextResponse(t, responseWriter, "third result")
			default:
				t.Errorf("unexpected task: %q", taskText)
			}
		},
	))
	t.Cleanup(deepSeekTestServer.Close)

	subAgentResults := RunSubAgentsInParallel(
		[]SubAgentTask{
			{TaskID: "first", Task: "first task"},
			{TaskID: "second", Task: "second task"},
			{TaskID: "third", Task: "third task"},
		},
		newSubAgentTestConfig(deepSeekTestServer.URL),
		tool.NewRegistry(),
	)

	if startedRequestCount.Load() != 3 {
		t.Fatalf("started request count = %d, want 3", startedRequestCount.Load())
	}
	expectedTaskIDs := []string{"first", "second", "third"}
	expectedResults := []string{"first result", "second result", "third result"}
	for resultIndex, subAgentResult := range subAgentResults {
		if subAgentResult.TaskID != expectedTaskIDs[resultIndex] {
			t.Fatalf(
				"result %d taskId = %q, want %q",
				resultIndex,
				subAgentResult.TaskID,
				expectedTaskIDs[resultIndex],
			)
		}
		if subAgentResult.Status != subAgentStatusCompleted {
			t.Fatalf(
				"result %d status = %q, want %q",
				resultIndex,
				subAgentResult.Status,
				subAgentStatusCompleted,
			)
		}
		if subAgentResult.Result != expectedResults[resultIndex] {
			t.Fatalf(
				"result %d text = %q, want %q",
				resultIndex,
				subAgentResult.Result,
				expectedResults[resultIndex],
			)
		}
	}
}

func TestRunSubAgentsInParallelKeepsSuccessfulResultWhenAnotherTaskFails(
	t *testing.T,
) {
	deepSeekTestServer := httptest.NewServer(http.HandlerFunc(
		func(responseWriter http.ResponseWriter, httpRequest *http.Request) {
			var receivedDeepSeekRequest recordedDeepSeekRequest
			decodeRequestError := json.NewDecoder(httpRequest.Body).Decode(
				&receivedDeepSeekRequest,
			)
			if decodeRequestError != nil {
				t.Errorf("decode DeepSeek request: %v", decodeRequestError)
				return
			}

			if firstSubAgentTaskText(receivedDeepSeekRequest.Messages) == "failed task" {
				responseWriter.WriteHeader(http.StatusBadGateway)
				_, _ = responseWriter.Write([]byte(`{"error":"provider failed"}`))
				return
			}
			writeDeepSeekTextResponse(t, responseWriter, "successful result")
		},
	))
	t.Cleanup(deepSeekTestServer.Close)

	subAgentResults := RunSubAgentsInParallel(
		[]SubAgentTask{
			{TaskID: "failed", Task: "failed task"},
			{TaskID: "successful", Task: "successful task"},
		},
		newSubAgentTestConfig(deepSeekTestServer.URL),
		tool.NewRegistry(),
	)

	if subAgentResults[0].Status != subAgentStatusFailed {
		t.Fatalf(
			"failed task status = %q, want %q",
			subAgentResults[0].Status,
			subAgentStatusFailed,
		)
	}
	if subAgentResults[0].Error == "" {
		t.Fatal("failed task error is empty")
	}
	if subAgentResults[1].Status != subAgentStatusCompleted {
		t.Fatalf(
			"successful task status = %q, want %q",
			subAgentResults[1].Status,
			subAgentStatusCompleted,
		)
	}
	if subAgentResults[1].Result != "successful result" {
		t.Fatalf(
			"successful task result = %q",
			subAgentResults[1].Result,
		)
	}
}

func newSubAgentTestConfig(apiEndpoint string) config.Config {
	return config.Config{
		ApiKey:                   "test-key",
		ApiEndpoint:              apiEndpoint,
		Model:                    "test-model",
		MaximumParallelSubAgents: 5,
	}
}

func registerSubAgentTestTool(
	t *testing.T,
	subAgentToolRegistry *tool.Registry,
	toolName string,
	executeFunction tool.FunctionToolExecuteFunction,
) {
	t.Helper()

	registerToolError := subAgentToolRegistry.RegisterFunctionTool(
		toolName,
		toolName+" description",
		map[string]any{"type": "object"},
		executeFunction,
	)
	if registerToolError != nil {
		t.Fatalf("register %s: %v", toolName, registerToolError)
	}
}

func writeDeepSeekTextResponse(
	t *testing.T,
	responseWriter http.ResponseWriter,
	responseText string,
) {
	t.Helper()

	responseWriter.Header().Set("Content-Type", "application/json")
	encodeResponseError := json.NewEncoder(responseWriter).Encode(map[string]any{
		"content": []map[string]any{{
			"type": "text",
			"text": responseText,
		}},
		"stop_reason": "end_turn",
		"usage": map[string]int{
			"input_tokens":  10,
			"output_tokens": 5,
		},
	})
	if encodeResponseError != nil {
		t.Errorf("encode DeepSeek text response: %v", encodeResponseError)
	}
}

func writeDeepSeekToolCallResponse(
	t *testing.T,
	responseWriter http.ResponseWriter,
	toolUseID string,
	toolName string,
) {
	t.Helper()

	responseWriter.Header().Set("Content-Type", "application/json")
	encodeResponseError := json.NewEncoder(responseWriter).Encode(map[string]any{
		"content": []map[string]any{{
			"type":  "tool_use",
			"id":    toolUseID,
			"name":  toolName,
			"input": map[string]any{},
		}},
		"stop_reason": "tool_use",
		"usage": map[string]int{
			"input_tokens":  10,
			"output_tokens": 5,
		},
	})
	if encodeResponseError != nil {
		t.Errorf("encode DeepSeek tool response: %v", encodeResponseError)
	}
}

func firstSubAgentTaskText(messages []model.Message) string {
	if len(messages) == 0 || len(messages[0].Content) == 0 {
		return ""
	}
	return messages[0].Content[0].Text
}

func findToolResultText(messages []model.Message) string {
	for _, message := range messages {
		for _, contentBlock := range message.Content {
			if contentBlock.Type == "tool_result" {
				return contentBlock.Content
			}
		}
	}
	return ""
}
