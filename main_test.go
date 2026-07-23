package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cc-agent-go/mcp"
	"cc-agent-go/model"
	"cc-agent-go/service"
	"cc-agent-go/tool"
)

func TestHandleChatInvalidJSON(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader("{"))

	handleChat(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	var resp model.ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != string(service.ErrorInvalidRequest) {
		t.Fatalf("code = %q, want %q", resp.Code, service.ErrorInvalidRequest)
	}
}

func TestHandleChatMissingAPIKeyDoesNotLogUserMessage(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Chdir(t.TempDir())

	var logs bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	const userMessage = "V11_PRIVATE_TEST_MESSAGE"
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/chat",
		strings.NewReader(`{"message":"`+userMessage+`"}`))

	handleChat(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	var resp model.ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != string(service.ErrorConfig) {
		t.Fatalf("code = %q, want %q", resp.Code, service.ErrorConfig)
	}
	if strings.Contains(logs.String(), userMessage) {
		t.Fatal("structured log contains the user message")
	}
	for lineNumber, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if line == "" {
			continue
		}
		var value map[string]any
		if err := json.Unmarshal([]byte(line), &value); err != nil {
			t.Fatalf("log line %d is not JSON: %v", lineNumber+1, err)
		}
	}
}

func TestHandleChatStreamMissingAPIKey(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Chdir(t.TempDir())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/chat/stream",
		strings.NewReader(`{"message":"test"}`))

	handleChatStream(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"type":"error"`) {
		t.Fatalf("SSE response has no error event: %s", body)
	}
	if !strings.Contains(body, `"code":"config_error"`) {
		t.Fatalf("SSE response has no config_error: %s", body)
	}
}

func TestPublicErrorPreservesProviderStatus(t *testing.T) {
	err := service.NewAppError(service.ErrorProviderAuth,
		"service.Chat.provider", http.StatusUnauthorized, errors.New("failed"))

	status, resp := publicError(err)

	if status != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", status, http.StatusBadGateway)
	}
	if resp.Code != string(service.ErrorProviderAuth) {
		t.Fatalf("code = %q, want %q", resp.Code, service.ErrorProviderAuth)
	}
	if resp.ProviderStatus != http.StatusUnauthorized {
		t.Fatalf("providerStatus = %d, want %d", resp.ProviderStatus, http.StatusUnauthorized)
	}
}

func TestHandleSelectMCPServersRejectsInvalidJSON(t *testing.T) {
	httpResponseRecorder := httptest.NewRecorder()
	httpRequest := httptest.NewRequest(
		http.MethodPut,
		"/api/mcp/servers",
		strings.NewReader("{"),
	)

	handleSelectMCPServers(httpResponseRecorder, httpRequest)

	if httpResponseRecorder.Code != http.StatusBadRequest {
		t.Fatalf(
			"status = %d, want %d",
			httpResponseRecorder.Code,
			http.StatusBadRequest,
		)
	}
	var publicErrorResponse model.ErrorResponse
	if decodeErrorResponseError := json.Unmarshal(
		httpResponseRecorder.Body.Bytes(),
		&publicErrorResponse,
	); decodeErrorResponseError != nil {
		t.Fatalf("decode response: %v", decodeErrorResponseError)
	}
	if publicErrorResponse.Code != string(service.ErrorInvalidRequest) {
		t.Fatalf(
			"code = %q, want %q",
			publicErrorResponse.Code,
			service.ErrorInvalidRequest,
		)
	}
}

func TestPublicErrorMapsMCPRequestTimeout(t *testing.T) {
	mcpRequestTimeoutError := mcp.NewError(
		mcp.ErrorRequestTimeout,
		"callMCPServerTool",
		"playwright",
		errors.New("timeout"),
	)

	httpStatus, publicErrorResponse := publicError(mcpRequestTimeoutError)

	if httpStatus != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d", httpStatus, http.StatusGatewayTimeout)
	}
	if publicErrorResponse.Code != string(mcp.ErrorRequestTimeout) {
		t.Fatalf(
			"code = %q, want %q",
			publicErrorResponse.Code,
			mcp.ErrorRequestTimeout,
		)
	}
}

func TestDecodeAndValidateRunSubAgentToolInputAcceptsValidTasks(t *testing.T) {
	runSubAgentToolInput, decodeToolInputError :=
		decodeAndValidateRunSubAgentToolInput(
			map[string]any{
				"subAgentTasks": []any{
					map[string]any{
						"taskId": "compile-check",
						"task":   "运行 go build ./...",
					},
					map[string]any{
						"taskId": "document-check",
						"task":   "检查 ROADMAP.md",
					},
				},
			},
			3,
		)
	if decodeToolInputError != nil {
		t.Fatalf("decode valid run_subagent input: %v", decodeToolInputError)
	}
	if len(runSubAgentToolInput.SubAgentTasks) != 2 {
		t.Fatalf(
			"task count = %d, want 2",
			len(runSubAgentToolInput.SubAgentTasks),
		)
	}
	if runSubAgentToolInput.SubAgentTasks[0].TaskID != "compile-check" {
		t.Fatalf(
			"first taskId = %q",
			runSubAgentToolInput.SubAgentTasks[0].TaskID,
		)
	}
	if runSubAgentToolInput.SubAgentTasks[0].Task != "运行 go build ./..." {
		t.Fatalf(
			"first task = %q",
			runSubAgentToolInput.SubAgentTasks[0].Task,
		)
	}
}

func TestDecodeAndValidateRunSubAgentToolInputRejectsInvalidTasks(t *testing.T) {
	testCases := []struct {
		testName                 string
		toolArguments            map[string]any
		maximumParallelSubAgents int
		expectedErrorText        string
	}{
		{
			testName:                 "missing task array",
			toolArguments:            map[string]any{},
			maximumParallelSubAgents: 5,
			expectedErrorText:        "至少需要 1 个任务",
		},
		{
			testName: "non array task value",
			toolArguments: map[string]any{
				"subAgentTasks": "not-an-array",
			},
			maximumParallelSubAgents: 5,
			expectedErrorText:        "无法解包",
		},
		{
			testName: "too many tasks for current configuration",
			toolArguments: map[string]any{
				"subAgentTasks": []any{
					validSubAgentToolTask("one"),
					validSubAgentToolTask("two"),
					validSubAgentToolTask("three"),
				},
			},
			maximumParallelSubAgents: 2,
			expectedErrorText:        "当前配置最多允许 2 个",
		},
		{
			testName: "empty task id",
			toolArguments: map[string]any{
				"subAgentTasks": []any{
					map[string]any{"taskId": " ", "task": "work"},
				},
			},
			maximumParallelSubAgents: 5,
			expectedErrorText:        "缺少非空 taskId",
		},
		{
			testName: "empty task",
			toolArguments: map[string]any{
				"subAgentTasks": []any{
					map[string]any{"taskId": "one", "task": " "},
				},
			},
			maximumParallelSubAgents: 5,
			expectedErrorText:        "缺少非空 task",
		},
		{
			testName: "non string task",
			toolArguments: map[string]any{
				"subAgentTasks": []any{
					map[string]any{"taskId": "one", "task": 123},
				},
			},
			maximumParallelSubAgents: 5,
			expectedErrorText:        "无法解包",
		},
		{
			testName: "duplicate task id",
			toolArguments: map[string]any{
				"subAgentTasks": []any{
					validSubAgentToolTask("same"),
					validSubAgentToolTask("same"),
				},
			},
			maximumParallelSubAgents: 5,
			expectedErrorText:        "taskId \"same\" 重复",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.testName, func(t *testing.T) {
			_, decodeToolInputError := decodeAndValidateRunSubAgentToolInput(
				testCase.toolArguments,
				testCase.maximumParallelSubAgents,
			)
			if decodeToolInputError == nil {
				t.Fatal("decode invalid run_subagent input returned nil error")
			}
			if !strings.Contains(
				decodeToolInputError.Error(),
				testCase.expectedErrorText,
			) {
				t.Fatalf(
					"error = %q, want text %q",
					decodeToolInputError,
					testCase.expectedErrorText,
				)
			}
		})
	}
}

func TestRegisterGeneralSubAgentToolAddsFixedJSONSchema(t *testing.T) {
	mainAgentToolRegistry := tool.NewRegistry()

	registerGeneralSubAgentError :=
		registerGeneralSubAgentTool(mainAgentToolRegistry)
	if registerGeneralSubAgentError != nil {
		t.Fatalf("registerGeneralSubAgentTool: %v", registerGeneralSubAgentError)
	}

	registeredToolDefinitions := mainAgentToolRegistry.GetDefinitions()
	if len(registeredToolDefinitions) != 1 {
		t.Fatalf(
			"definition count = %d, want 1",
			len(registeredToolDefinitions),
		)
	}
	runSubAgentToolDefinition := registeredToolDefinitions[0]
	if runSubAgentToolDefinition["name"] != generalSubAgentToolName {
		t.Fatalf(
			"tool name = %v, want %q",
			runSubAgentToolDefinition["name"],
			generalSubAgentToolName,
		)
	}

	runSubAgentInputSchema, inputSchemaIsMap :=
		runSubAgentToolDefinition["input_schema"].(map[string]any)
	if !inputSchemaIsMap {
		t.Fatalf(
			"input_schema type = %T",
			runSubAgentToolDefinition["input_schema"],
		)
	}
	inputProperties, propertiesIsMap :=
		runSubAgentInputSchema["properties"].(map[string]any)
	if !propertiesIsMap {
		t.Fatalf(
			"properties type = %T",
			runSubAgentInputSchema["properties"],
		)
	}
	subAgentTasksSchema, taskSchemaIsMap :=
		inputProperties["subAgentTasks"].(map[string]any)
	if !taskSchemaIsMap {
		t.Fatalf(
			"subAgentTasks schema type = %T",
			inputProperties["subAgentTasks"],
		)
	}
	if subAgentTasksSchema["minItems"] != 1 {
		t.Fatalf("minItems = %v, want 1", subAgentTasksSchema["minItems"])
	}
	if subAgentTasksSchema["maxItems"] != 5 {
		t.Fatalf("maxItems = %v, want 5", subAgentTasksSchema["maxItems"])
	}
	subAgentTaskItemSchema, taskItemSchemaIsMap :=
		subAgentTasksSchema["items"].(map[string]any)
	if !taskItemSchemaIsMap {
		t.Fatalf("task item schema type = %T", subAgentTasksSchema["items"])
	}
	requiredTaskFields, requiredFieldsAreStrings :=
		subAgentTaskItemSchema["required"].([]string)
	if !requiredFieldsAreStrings {
		t.Fatalf(
			"required task fields type = %T",
			subAgentTaskItemSchema["required"],
		)
	}
	if len(requiredTaskFields) != 2 ||
		requiredTaskFields[0] != "taskId" ||
		requiredTaskFields[1] != "task" {
		t.Fatalf(
			"required task fields = %#v, want taskId and task",
			requiredTaskFields,
		)
	}
}

func TestRegisteredGeneralSubAgentToolRejectsInvalidInputBeforeDeepSeekCall(
	t *testing.T,
) {
	t.Setenv("MAX_PARALLEL_SUBAGENTS", "3")
	mainAgentToolRegistry := tool.NewRegistry()
	registerGeneralSubAgentError :=
		registerGeneralSubAgentTool(mainAgentToolRegistry)
	if registerGeneralSubAgentError != nil {
		t.Fatalf("registerGeneralSubAgentTool: %v", registerGeneralSubAgentError)
	}

	_, executeRunSubAgentToolError := mainAgentToolRegistry.Execute(
		generalSubAgentToolName,
		map[string]any{
			"subAgentTasks": []any{
				validSubAgentToolTask("one"),
				validSubAgentToolTask("two"),
				validSubAgentToolTask("three"),
				validSubAgentToolTask("four"),
			},
		},
	)
	if executeRunSubAgentToolError == nil {
		t.Fatal("registered run_subagent accepted more than two tasks")
	}
	if !strings.Contains(
		executeRunSubAgentToolError.Error(),
		"当前配置最多允许 3 个",
	) {
		t.Fatalf("error = %q", executeRunSubAgentToolError)
	}
}

func TestHandleChatReceivesParallelSubAgentJSONToolResult(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-key")
	t.Setenv("MAX_PARALLEL_SUBAGENTS", "3")
	t.Chdir(t.TempDir())

	restoreMainIntegrationTestGlobals := configureMainIntegrationTestGlobals(t)
	t.Cleanup(restoreMainIntegrationTestGlobals)

	providerTransport := newParallelSubAgentProviderTransport()
	http.DefaultClient = &http.Client{Transport: providerTransport}

	httpResponseRecorder := httptest.NewRecorder()
	httpRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/chat",
		strings.NewReader(`{"message":"同时完成两个独立任务"}`),
	)

	handleChat(httpResponseRecorder, httpRequest)

	if httpResponseRecorder.Code != http.StatusOK {
		t.Fatalf(
			"status = %d, body = %s",
			httpResponseRecorder.Code,
			httpResponseRecorder.Body.String(),
		)
	}
	var chatResponse model.ChatResponse
	decodeChatResponseError := json.Unmarshal(
		httpResponseRecorder.Body.Bytes(),
		&chatResponse,
	)
	if decodeChatResponseError != nil {
		t.Fatalf("decode chat response: %v", decodeChatResponseError)
	}
	if chatResponse.Reply != "主 Agent 已收到两个 SubAgent 结果" {
		t.Fatalf("reply = %q", chatResponse.Reply)
	}

	assertParallelSubAgentToolResult(t, providerTransport)
	assertOnlyMainAgentSessionWasSaved(t)
}

func TestHandleChatStreamReceivesParallelSubAgentJSONToolResult(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-key")
	t.Setenv("MAX_PARALLEL_SUBAGENTS", "3")
	t.Chdir(t.TempDir())

	restoreMainIntegrationTestGlobals := configureMainIntegrationTestGlobals(t)
	t.Cleanup(restoreMainIntegrationTestGlobals)

	providerTransport := newParallelSubAgentProviderTransport()
	http.DefaultClient = &http.Client{Transport: providerTransport}

	httpResponseRecorder := httptest.NewRecorder()
	httpRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/chat/stream",
		strings.NewReader(`{"message":"同时完成两个独立任务"}`),
	)

	handleChatStream(httpResponseRecorder, httpRequest)

	if httpResponseRecorder.Code != http.StatusOK {
		t.Fatalf(
			"status = %d, body = %s",
			httpResponseRecorder.Code,
			httpResponseRecorder.Body.String(),
		)
	}
	if !strings.Contains(
		httpResponseRecorder.Body.String(),
		"主 Agent 已收到两个 SubAgent 结果",
	) {
		t.Fatalf("SSE body = %s", httpResponseRecorder.Body.String())
	}

	assertParallelSubAgentToolResult(t, providerTransport)
	assertOnlyMainAgentSessionWasSaved(t)
}

func TestRunSubAgentUsesToolsFromStartedPlaywrightMCPServer(t *testing.T) {
	if os.Getenv("RUN_PLAYWRIGHT_MCP_INTEGRATION") != "1" {
		t.Skip("set RUN_PLAYWRIGHT_MCP_INTEGRATION=1 to start Playwright MCP")
	}
	t.Setenv("DEEPSEEK_API_KEY", "test-key")
	t.Setenv("MAX_PARALLEL_SUBAGENTS", "3")

	mainAgentToolRegistry := tool.NewRegistry()
	playwrightMCPServerManager, createMCPServerManagerError :=
		mcp.NewMCPServerManager(
			"config/mcp_servers.json",
			"mcp/protocol/2025-11-25/messages.json",
		)
	if createMCPServerManagerError != nil {
		t.Fatalf(
			"create Playwright MCP Server manager: %v",
			createMCPServerManagerError,
		)
	}
	t.Cleanup(func() {
		closeMCPServersError :=
			playwrightMCPServerManager.CloseAllStartedMCPServers(
				mainAgentToolRegistry,
			)
		if closeMCPServersError != nil {
			t.Errorf("close Playwright MCP Server: %v", closeMCPServersError)
		}
	})

	startMCPContext, cancelStartMCP :=
		context.WithTimeout(context.Background(), 90*time.Second)
	defer cancelStartMCP()
	playwrightMCPServerStatuses, startMCPServerError :=
		playwrightMCPServerManager.StartSelectedMCPServers(
			startMCPContext,
			[]string{"playwright"},
			mainAgentToolRegistry,
		)
	if startMCPServerError != nil {
		t.Fatalf("start Playwright MCP Server: %v", startMCPServerError)
	}
	if len(playwrightMCPServerStatuses) != 1 ||
		playwrightMCPServerStatuses[0].RegisteredToolCount == 0 {
		t.Fatalf(
			"Playwright MCP status = %#v",
			playwrightMCPServerStatuses,
		)
	}

	registerGeneralSubAgentError :=
		registerGeneralSubAgentTool(mainAgentToolRegistry)
	if registerGeneralSubAgentError != nil {
		t.Fatalf("register general SubAgent tool: %v", registerGeneralSubAgentError)
	}

	originalHTTPClient := http.DefaultClient
	providerTransport := newParallelSubAgentProviderTransport()
	http.DefaultClient = &http.Client{Transport: providerTransport}
	t.Cleanup(func() {
		http.DefaultClient = originalHTTPClient
	})

	runSubAgentToolResultJSON, executeRunSubAgentToolError :=
		mainAgentToolRegistry.Execute(
			generalSubAgentToolName,
			map[string]any{
				"subAgentTasks": []any{
					validSubAgentToolTask("first"),
					validSubAgentToolTask("second"),
				},
			},
		)
	if executeRunSubAgentToolError != nil {
		t.Fatalf("execute run_subagent: %v", executeRunSubAgentToolError)
	}
	var runSubAgentToolOutput RunSubAgentToolOutput
	decodeToolOutputError := json.Unmarshal(
		[]byte(runSubAgentToolResultJSON),
		&runSubAgentToolOutput,
	)
	if decodeToolOutputError != nil {
		t.Fatalf("decode run_subagent output: %v", decodeToolOutputError)
	}
	if len(runSubAgentToolOutput.Results) != 2 {
		t.Fatalf(
			"run_subagent result count = %d, want 2",
			len(runSubAgentToolOutput.Results),
		)
	}

	providerTransport.toolResultMutex.Lock()
	defer providerTransport.toolResultMutex.Unlock()
	foundPlaywrightMCPTool := false
	for subAgentToolName := range providerTransport.subAgentToolNames {
		if strings.HasPrefix(subAgentToolName, "mcp_playwright__") {
			foundPlaywrightMCPTool = true
		}
	}
	if !foundPlaywrightMCPTool {
		t.Fatalf(
			"SubAgent tool names do not contain Playwright MCP: %#v",
			providerTransport.subAgentToolNames,
		)
	}
	if providerTransport.subAgentToolNames[generalSubAgentToolName] {
		t.Fatal("SubAgent tool definitions contain run_subagent")
	}
}

func TestRunSubAgentToolOutputUsesFixedJSONFields(t *testing.T) {
	runSubAgentToolOutput := RunSubAgentToolOutput{
		MaximumParallelSubAgents: 3,
		Results: []service.SubAgentResult{
			{
				TaskID: "successful",
				Status: "completed",
				Result: "done",
				Error:  "",
			},
			{
				TaskID: "failed",
				Status: "failed",
				Result: "",
				Error:  "network_timeout",
			},
		},
	}

	runSubAgentToolOutputJSON, encodeOutputError :=
		json.Marshal(runSubAgentToolOutput)
	if encodeOutputError != nil {
		t.Fatalf("encode RunSubAgentToolOutput: %v", encodeOutputError)
	}

	var decodedOutput map[string]any
	decodeOutputError := json.Unmarshal(
		runSubAgentToolOutputJSON,
		&decodedOutput,
	)
	if decodeOutputError != nil {
		t.Fatalf("decode RunSubAgentToolOutput: %v", decodeOutputError)
	}
	if decodedOutput["maximumParallelSubAgents"] != float64(3) {
		t.Fatalf(
			"maximumParallelSubAgents = %v, want 3",
			decodedOutput["maximumParallelSubAgents"],
		)
	}

	decodedResults, resultsIsArray := decodedOutput["results"].([]any)
	if !resultsIsArray || len(decodedResults) != 2 {
		t.Fatalf("results = %#v", decodedOutput["results"])
	}
	for resultIndex, decodedResultValue := range decodedResults {
		decodedResult, resultIsObject :=
			decodedResultValue.(map[string]any)
		if !resultIsObject {
			t.Fatalf(
				"result %d type = %T",
				resultIndex,
				decodedResultValue,
			)
		}
		for _, requiredField := range []string{
			"taskId",
			"status",
			"result",
			"error",
		} {
			if _, fieldExists := decodedResult[requiredField]; !fieldExists {
				t.Fatalf(
					"result %d has no %s field",
					resultIndex,
					requiredField,
				)
			}
		}
	}
}

func validSubAgentToolTask(taskID string) map[string]any {
	return map[string]any{
		"taskId": taskID,
		"task":   "complete " + taskID,
	}
}

type roundTripFunction func(httpRequest *http.Request) (*http.Response, error)

func (executeRoundTrip roundTripFunction) RoundTrip(
	httpRequest *http.Request,
) (*http.Response, error) {
	return executeRoundTrip(httpRequest)
}

type parallelSubAgentProviderTransport struct {
	subAgentRequestsStarted  atomic.Int32
	allSubAgentRequestsReady chan struct{}
	closeAllRequestsReady    sync.Once
	toolResultMutex          sync.Mutex
	mainAgentToolResultJSON  string
	subAgentToolNames        map[string]bool
}

func newParallelSubAgentProviderTransport() *parallelSubAgentProviderTransport {
	return &parallelSubAgentProviderTransport{
		allSubAgentRequestsReady: make(chan struct{}),
		subAgentToolNames:        make(map[string]bool),
	}
}

func (providerTransport *parallelSubAgentProviderTransport) RoundTrip(
	httpRequest *http.Request,
) (*http.Response, error) {
	deepSeekRequestJSON, readRequestError := io.ReadAll(httpRequest.Body)
	if readRequestError != nil {
		return nil, fmt.Errorf("read DeepSeek request: %w", readRequestError)
	}

	var deepSeekRequest recordedMainIntegrationDeepSeekRequest
	decodeRequestError := json.Unmarshal(
		deepSeekRequestJSON,
		&deepSeekRequest,
	)
	if decodeRequestError != nil {
		return nil, fmt.Errorf("decode DeepSeek request: %w", decodeRequestError)
	}

	if strings.Contains(deepSeekRequest.System, "临时通用 SubAgent") {
		providerTransport.recordSubAgentToolNames(deepSeekRequest.Tools)
		if providerTransport.subAgentRequestsStarted.Add(1) == 2 {
			providerTransport.closeAllRequestsReady.Do(func() {
				close(providerTransport.allSubAgentRequestsReady)
			})
		}

		select {
		case <-providerTransport.allSubAgentRequestsReady:
		case <-time.After(2 * time.Second):
			return nil, fmt.Errorf("two SubAgent requests did not start concurrently")
		}

		subAgentTask := firstMainIntegrationUserText(deepSeekRequest.Messages)
		return regularDeepSeekTextHTTPResponse("result: " + subAgentTask), nil
	}

	mainAgentToolResultJSON := findMainIntegrationToolResult(
		deepSeekRequest.Messages,
	)
	if mainAgentToolResultJSON == "" {
		if deepSeekRequest.Stream {
			return streamingDeepSeekToolCallHTTPResponse(), nil
		}
		return regularDeepSeekToolCallHTTPResponse(), nil
	}

	providerTransport.toolResultMutex.Lock()
	providerTransport.mainAgentToolResultJSON = mainAgentToolResultJSON
	providerTransport.toolResultMutex.Unlock()

	if deepSeekRequest.Stream {
		return streamingDeepSeekTextHTTPResponse(
			"主 Agent 已收到两个 SubAgent 结果",
		), nil
	}
	return regularDeepSeekTextHTTPResponse(
		"主 Agent 已收到两个 SubAgent 结果",
	), nil
}

func (providerTransport *parallelSubAgentProviderTransport) recordSubAgentToolNames(
	subAgentToolDefinitions []map[string]any,
) {
	providerTransport.toolResultMutex.Lock()
	defer providerTransport.toolResultMutex.Unlock()

	for _, subAgentToolDefinition := range subAgentToolDefinitions {
		toolName, toolNameIsString := subAgentToolDefinition["name"].(string)
		if toolNameIsString {
			providerTransport.subAgentToolNames[toolName] = true
		}
	}
}

type recordedMainIntegrationDeepSeekRequest struct {
	System   string           `json:"system"`
	Messages []model.Message  `json:"messages"`
	Tools    []map[string]any `json:"tools"`
	Stream   bool             `json:"stream"`
}

func configureMainIntegrationTestGlobals(t *testing.T) func() {
	t.Helper()

	originalRegistry := registry
	originalHTTPClient := http.DefaultClient

	registry = tool.NewRegistry()
	registerFakeMCPToolError := registry.RegisterFunctionTool(
		"mcp_playwright__read_page",
		"fake MCP tool used by the integration test",
		map[string]any{"type": "object"},
		func(toolArguments map[string]any) (string, error) {
			return "fake MCP result", nil
		},
	)
	if registerFakeMCPToolError != nil {
		t.Fatalf("register fake MCP tool: %v", registerFakeMCPToolError)
	}
	registerGeneralSubAgentError := registerGeneralSubAgentTool(registry)
	if registerGeneralSubAgentError != nil {
		t.Fatalf("register general SubAgent tool: %v", registerGeneralSubAgentError)
	}

	return func() {
		registry = originalRegistry
		http.DefaultClient = originalHTTPClient
	}
}

func assertParallelSubAgentToolResult(
	t *testing.T,
	providerTransport *parallelSubAgentProviderTransport,
) {
	t.Helper()

	if providerTransport.subAgentRequestsStarted.Load() != 2 {
		t.Fatalf(
			"SubAgent request count = %d, want 2",
			providerTransport.subAgentRequestsStarted.Load(),
		)
	}

	providerTransport.toolResultMutex.Lock()
	mainAgentToolResultJSON := providerTransport.mainAgentToolResultJSON
	subAgentToolNames := make(map[string]bool)
	for toolName, toolExists := range providerTransport.subAgentToolNames {
		subAgentToolNames[toolName] = toolExists
	}
	providerTransport.toolResultMutex.Unlock()

	var runSubAgentToolOutput RunSubAgentToolOutput
	decodeToolResultError := json.Unmarshal(
		[]byte(mainAgentToolResultJSON),
		&runSubAgentToolOutput,
	)
	if decodeToolResultError != nil {
		t.Fatalf(
			"decode run_subagent tool result %q: %v",
			mainAgentToolResultJSON,
			decodeToolResultError,
		)
	}
	if runSubAgentToolOutput.MaximumParallelSubAgents != 3 {
		t.Fatalf(
			"maximumParallelSubAgents = %d, want 3",
			runSubAgentToolOutput.MaximumParallelSubAgents,
		)
	}
	if len(runSubAgentToolOutput.Results) != 2 {
		t.Fatalf(
			"result count = %d, want 2",
			len(runSubAgentToolOutput.Results),
		)
	}
	for resultIndex, subAgentResult := range runSubAgentToolOutput.Results {
		if subAgentResult.TaskID == "" ||
			subAgentResult.Status != "completed" ||
			subAgentResult.Result == "" ||
			subAgentResult.Error != "" {
			t.Fatalf(
				"result %d = %#v",
				resultIndex,
				subAgentResult,
			)
		}
	}
	if !subAgentToolNames["mcp_playwright__read_page"] {
		t.Fatal("SubAgent tool definitions do not contain the registered MCP tool")
	}
	if subAgentToolNames[generalSubAgentToolName] {
		t.Fatal("SubAgent tool definitions contain run_subagent")
	}
}

func assertOnlyMainAgentSessionWasSaved(t *testing.T) {
	t.Helper()

	sessionFilePaths, readSessionDirectoryError := filepath.Glob(
		filepath.Join("data", "sessions", "*.json"),
	)
	if readSessionDirectoryError != nil {
		t.Fatalf("read session directory: %v", readSessionDirectoryError)
	}
	if len(sessionFilePaths) != 1 {
		t.Fatalf(
			"session file count = %d, want only the main Agent session",
			len(sessionFilePaths),
		)
	}
	if _, readSessionError := os.ReadFile(sessionFilePaths[0]); readSessionError != nil {
		t.Fatalf("read main Agent session: %v", readSessionError)
	}
}

func regularDeepSeekToolCallHTTPResponse() *http.Response {
	toolArgumentsJSON, _ := json.Marshal(map[string]any{
		"subAgentTasks": []map[string]string{
			{
				"taskId": "first",
				"task":   "first independent task",
			},
			{
				"taskId": "second",
				"task":   "second independent task",
			},
		},
	})
	return newProviderHTTPResponse(
		http.StatusOK,
		fmt.Sprintf(
			`{"content":[{"type":"tool_use","id":"run-subagents","name":"run_subagent","input":%s}],"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":5}}`,
			toolArgumentsJSON,
		),
		"application/json",
	)
}

func regularDeepSeekTextHTTPResponse(responseText string) *http.Response {
	responseTextJSON, _ := json.Marshal(responseText)
	return newProviderHTTPResponse(
		http.StatusOK,
		fmt.Sprintf(
			`{"content":[{"type":"text","text":%s}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`,
			responseTextJSON,
		),
		"application/json",
	)
}

func streamingDeepSeekToolCallHTTPResponse() *http.Response {
	toolArgumentsJSON, _ := json.Marshal(map[string]any{
		"subAgentTasks": []map[string]string{
			{
				"taskId": "first",
				"task":   "first independent task",
			},
			{
				"taskId": "second",
				"task":   "second independent task",
			},
		},
	})
	partialToolArgumentsJSON, _ := json.Marshal(string(toolArgumentsJSON))
	streamBody := strings.Join([]string{
		`data: {"type":"message_start","message":{"usage":{"input_tokens":10}}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"run-subagents","name":"run_subagent"}}`,
		fmt.Sprintf(
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":%s}}`,
			partialToolArgumentsJSON,
		),
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"message_delta","usage":{"output_tokens":5}}`,
		`data: {"type":"message_stop"}`,
		"",
	}, "\n\n")
	return newProviderHTTPResponse(
		http.StatusOK,
		streamBody,
		"text/event-stream",
	)
}

func streamingDeepSeekTextHTTPResponse(responseText string) *http.Response {
	responseTextJSON, _ := json.Marshal(responseText)
	streamBody := strings.Join([]string{
		`data: {"type":"message_start","message":{"usage":{"input_tokens":10}}}`,
		fmt.Sprintf(
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%s}}`,
			responseTextJSON,
		),
		`data: {"type":"message_delta","usage":{"output_tokens":5}}`,
		`data: {"type":"message_stop"}`,
		"",
	}, "\n\n")
	return newProviderHTTPResponse(
		http.StatusOK,
		streamBody,
		"text/event-stream",
	)
}

func newProviderHTTPResponse(
	statusCode int,
	responseBody string,
	contentType string,
) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header: http.Header{
			"Content-Type": []string{contentType},
		},
		Body: io.NopCloser(strings.NewReader(responseBody)),
	}
}

func firstMainIntegrationUserText(messages []model.Message) string {
	if len(messages) == 0 || len(messages[0].Content) == 0 {
		return ""
	}
	return messages[0].Content[0].Text
}

func findMainIntegrationToolResult(messages []model.Message) string {
	for _, message := range messages {
		for _, contentBlock := range message.Content {
			if contentBlock.Type == "tool_result" {
				return contentBlock.Content
			}
		}
	}
	return ""
}
