package main

import (
	"bufio"
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

func TestConfigureApplicationLoggerWritesJSONToFileAndStandardError(t *testing.T) {
	oldLogger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	logFilePath := filepath.Join(t.TempDir(), "logs", "server.jsonl")
	var standardErrorOutput bytes.Buffer

	applicationLogFile, configureLoggerError :=
		configureApplicationLogger(logFilePath, &standardErrorOutput)
	if configureLoggerError != nil {
		t.Fatalf("configure application logger: %v", configureLoggerError)
	}

	slog.Info("logger test message",
		"component", "logger_test",
		"operation", "write")
	if syncLogFileError := applicationLogFile.Sync(); syncLogFileError != nil {
		t.Fatalf("sync application log file: %v", syncLogFileError)
	}
	if closeLogFileError := applicationLogFile.Close(); closeLogFileError != nil {
		t.Fatalf("close application log file: %v", closeLogFileError)
	}

	logFileJSON, readLogFileError := os.ReadFile(logFilePath)
	if readLogFileError != nil {
		t.Fatalf("read application log file: %v", readLogFileError)
	}

	for outputName, logOutput := range map[string]string{
		"log file":       string(logFileJSON),
		"standard error": standardErrorOutput.String(),
	} {
		if !strings.Contains(logOutput, `"msg":"logger test message"`) {
			t.Fatalf("%s does not contain JSON log message: %s", outputName, logOutput)
		}
		if !strings.Contains(logOutput, `"component":"logger_test"`) {
			t.Fatalf("%s does not contain component field: %s", outputName, logOutput)
		}
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

func TestHandleConversationEventsSendsEventAndRemovesDisconnectedReceiver(
	t *testing.T,
) {
	previousConversationEventReceivers := conversationEventReceivers
	conversationEventReceivers = service.NewConversationEventReceivers()
	t.Cleanup(func() {
		conversationEventReceivers = previousConversationEventReceivers
	})

	conversationEventHTTPRoutes := http.NewServeMux()
	conversationEventHTTPRoutes.HandleFunc(
		"GET /api/conversations/{id}/events",
		handleConversationEvents,
	)
	conversationEventHTTPServer :=
		httptest.NewServer(conversationEventHTTPRoutes)
	t.Cleanup(conversationEventHTTPServer.Close)

	requestContext, cancelRequest := context.WithCancel(context.Background())
	conversationEventRequest, createRequestError := http.NewRequestWithContext(
		requestContext,
		http.MethodGet,
		conversationEventHTTPServer.URL+
			"/api/conversations/conversation-a/events",
		nil,
	)
	if createRequestError != nil {
		t.Fatalf("create conversation event request: %v", createRequestError)
	}

	conversationEventResponse, sendRequestError :=
		conversationEventHTTPServer.Client().Do(conversationEventRequest)
	if sendRequestError != nil {
		t.Fatalf("send conversation event request: %v", sendRequestError)
	}
	defer conversationEventResponse.Body.Close()

	if conversationEventResponse.StatusCode != http.StatusOK {
		t.Fatalf(
			"status = %d, want %d",
			conversationEventResponse.StatusCode,
			http.StatusOK,
		)
	}
	if contentType :=
		conversationEventResponse.Header.Get("Content-Type"); !strings.Contains(
		contentType,
		"text/event-stream",
	) {
		t.Fatalf("Content-Type = %q", contentType)
	}

	conversationEventReader :=
		bufio.NewReader(conversationEventResponse.Body)
	connectedComment, readConnectedCommentError :=
		conversationEventReader.ReadString('\n')
	if readConnectedCommentError != nil {
		t.Fatalf("read connected comment: %v", readConnectedCommentError)
	}
	if connectedComment != ": connected\n" {
		t.Fatalf("connected comment = %q", connectedComment)
	}
	if _, readEmptyLineError := conversationEventReader.ReadString('\n'); readEmptyLineError != nil {
		t.Fatalf("read connected empty line: %v", readEmptyLineError)
	}

	eventJSON := []byte(`{"type":"background_reply_started"}`)
	if receiverCount :=
		conversationEventReceivers.SendEventJSON(
			"conversation-a",
			eventJSON,
		); receiverCount != 1 {
		t.Fatalf("receiver count = %d, want 1", receiverCount)
	}

	eventLine, readEventError :=
		conversationEventReader.ReadString('\n')
	if readEventError != nil {
		t.Fatalf("read event: %v", readEventError)
	}
	if expectedEventLine :=
		"data: " + string(eventJSON) + "\n"; eventLine != expectedEventLine {
		t.Fatalf("event line = %q, want %q", eventLine, expectedEventLine)
	}

	cancelRequest()
	deadline := time.Now().Add(2 * time.Second)
	for conversationEventReceivers.ReceiverCount("conversation-a") != 0 {
		if time.Now().After(deadline) {
			t.Fatal("conversation event receiver was not removed")
		}
		time.Sleep(10 * time.Millisecond)
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
						"taskId":        "compile-check",
						"task":          "运行 go build ./...",
						"maximumRounds": 20,
					},
					map[string]any{
						"taskId":        "document-check",
						"task":          "检查 ROADMAP.md",
						"maximumRounds": 5,
					},
				},
			},
			3,
			50,
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
	if runSubAgentToolInput.SubAgentTasks[0].MaximumRounds != 20 {
		t.Fatalf(
			"first maximumRounds = %d, want 20",
			runSubAgentToolInput.SubAgentTasks[0].MaximumRounds,
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
		{
			testName: "missing maximum rounds",
			toolArguments: map[string]any{
				"subAgentTasks": []any{
					map[string]any{
						"taskId": "one",
						"task":   "work",
					},
				},
			},
			maximumParallelSubAgents: 5,
			expectedErrorText:        "maximumRounds 必须大于 0",
		},
		{
			testName: "maximum rounds above configuration",
			toolArguments: map[string]any{
				"subAgentTasks": []any{
					map[string]any{
						"taskId":        "one",
						"task":          "work",
						"maximumRounds": 51,
					},
				},
			},
			maximumParallelSubAgents: 5,
			expectedErrorText:        "当前配置最多允许 50 轮",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.testName, func(t *testing.T) {
			_, decodeToolInputError := decodeAndValidateRunSubAgentToolInput(
				testCase.toolArguments,
				testCase.maximumParallelSubAgents,
				50,
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
	t.Setenv("MAXIMUM_SUBAGENT_ROUNDS", "40")
	mainAgentToolRegistry := tool.NewRegistry()

	registerGeneralSubAgentError := registerGeneralSubAgentTool(
		mainAgentToolRegistry,
		"test-conversation",
		ignoreCompletedSubAgentResults,
	)
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
	if len(requiredTaskFields) != 3 ||
		requiredTaskFields[0] != "taskId" ||
		requiredTaskFields[1] != "task" ||
		requiredTaskFields[2] != "maximumRounds" {
		t.Fatalf(
			"required task fields = %#v, want taskId, task and maximumRounds",
			requiredTaskFields,
		)
	}
	taskProperties, taskPropertiesAreMap :=
		subAgentTaskItemSchema["properties"].(map[string]any)
	if !taskPropertiesAreMap {
		t.Fatalf(
			"task properties type = %T",
			subAgentTaskItemSchema["properties"],
		)
	}
	maximumRoundsSchema, maximumRoundsSchemaIsMap :=
		taskProperties["maximumRounds"].(map[string]any)
	if !maximumRoundsSchemaIsMap {
		t.Fatalf(
			"maximumRounds schema type = %T",
			taskProperties["maximumRounds"],
		)
	}
	if maximumRoundsSchema["minimum"] != 1 ||
		maximumRoundsSchema["maximum"] != 40 {
		t.Fatalf(
			"maximumRounds schema = %#v",
			maximumRoundsSchema,
		)
	}
}

func TestRegisteredGeneralSubAgentToolRejectsInvalidInputBeforeDeepSeekCall(
	t *testing.T,
) {
	t.Setenv("MAX_PARALLEL_SUBAGENTS", "3")
	mainAgentToolRegistry := tool.NewRegistry()
	registerGeneralSubAgentError := registerGeneralSubAgentTool(
		mainAgentToolRegistry,
		"test-conversation",
		ignoreCompletedSubAgentResults,
	)
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
	const conversationID = "nonstream-background-callback"
	conversationEventChannel :=
		conversationEventReceivers.AddReceiver(conversationID)
	defer conversationEventReceivers.RemoveReceiver(
		conversationID,
		conversationEventChannel,
	)

	httpResponseRecorder := httptest.NewRecorder()
	httpRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/chat",
		strings.NewReader(
			`{"message":"同时完成两个独立任务",`+
				`"conversationId":"`+conversationID+`"}`,
		),
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
	if chatResponse.Reply != "SubAgent 已启动，完成后会自动返回结果。" {
		t.Fatalf("reply = %q", chatResponse.Reply)
	}

	waitForBackgroundReplyCompletedEvent(
		t,
		conversationEventChannel,
		"主 Agent 已处理后台 SubAgent 结果",
	)
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
	const conversationID = "stream-background-callback"
	conversationEventChannel :=
		conversationEventReceivers.AddReceiver(conversationID)
	defer conversationEventReceivers.RemoveReceiver(
		conversationID,
		conversationEventChannel,
	)

	httpResponseRecorder := httptest.NewRecorder()
	httpRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/chat/stream",
		strings.NewReader(
			`{"message":"同时完成两个独立任务",`+
				`"conversationId":"`+conversationID+`"}`,
		),
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
		"SubAgent 已启动，完成后会自动返回结果。",
	) {
		t.Fatalf("SSE body = %s", httpResponseRecorder.Body.String())
	}

	waitForBackgroundReplyCompletedEvent(
		t,
		conversationEventChannel,
		"主 Agent 已处理后台 SubAgent 结果",
	)
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

	completedSubAgentResults := make(chan []service.SubAgentResult, 1)
	registerGeneralSubAgentError := registerGeneralSubAgentTool(
		mainAgentToolRegistry,
		"playwright-test-conversation",
		func(
			parentConversationID string,
			subAgentResults []service.SubAgentResult,
		) {
			completedSubAgentResults <- subAgentResults
		},
	)
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
	if len(runSubAgentToolOutput.Tasks) != 2 {
		t.Fatalf(
			"run_subagent task count = %d, want 2",
			len(runSubAgentToolOutput.Tasks),
		)
	}
	select {
	case returnedSubAgentResults := <-completedSubAgentResults:
		if len(returnedSubAgentResults) != 2 {
			t.Fatalf(
				"completed SubAgent result count = %d, want 2",
				len(returnedSubAgentResults),
			)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("background SubAgents did not call completion callback")
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
		Tasks: []StartedSubAgentTask{
			{
				TaskID: "successful",
				Status: "running",
			},
			{
				TaskID: "second",
				Status: "running",
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

	decodedTasks, tasksIsArray := decodedOutput["tasks"].([]any)
	if !tasksIsArray || len(decodedTasks) != 2 {
		t.Fatalf("tasks = %#v", decodedOutput["tasks"])
	}
	for taskIndex, decodedTaskValue := range decodedTasks {
		decodedTask, taskIsObject :=
			decodedTaskValue.(map[string]any)
		if !taskIsObject {
			t.Fatalf(
				"task %d type = %T",
				taskIndex,
				decodedTaskValue,
			)
		}
		for _, requiredField := range []string{
			"taskId",
			"status",
		} {
			if _, fieldExists := decodedTask[requiredField]; !fieldExists {
				t.Fatalf(
					"task %d has no %s field",
					taskIndex,
					requiredField,
				)
			}
		}
	}
}

func ignoreCompletedSubAgentResults(
	parentConversationID string,
	subAgentResults []service.SubAgentResult,
) {
}

func validSubAgentToolTask(taskID string) map[string]any {
	return map[string]any{
		"taskId":        taskID,
		"task":          "complete " + taskID,
		"maximumRounds": 20,
	}
}

type roundTripFunction func(httpRequest *http.Request) (*http.Response, error)

func (executeRoundTrip roundTripFunction) RoundTrip(
	httpRequest *http.Request,
) (*http.Response, error) {
	return executeRoundTrip(httpRequest)
}

var mainIntegrationFakeMCPToolExecutions atomic.Int32

type parallelSubAgentProviderTransport struct {
	subAgentRequestsStarted  atomic.Int32
	foregroundRequests       atomic.Int32
	callbackToolCount        atomic.Int32
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

	if mainIntegrationMessagesContainText(
		deepSeekRequest.Messages,
		"你之前启动的一批 SubAgent 已经执行完毕",
	) {
		providerTransport.callbackToolCount.Store(
			int32(len(deepSeekRequest.Tools)),
		)
		if deepSeekRequest.Stream {
			return streamingDeepSeekTextHTTPResponse(
				"主 Agent 已处理后台 SubAgent 结果",
			), nil
		}
		return regularDeepSeekTextHTTPResponse(
			"主 Agent 已处理后台 SubAgent 结果",
		), nil
	}

	providerTransport.foregroundRequests.Add(1)
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
			"主 Agent 已启动两个 SubAgent",
		), nil
	}
	return regularDeepSeekTextHTTPResponse(
		"主 Agent 已启动两个 SubAgent",
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
	originalConversationEventReceivers := conversationEventReceivers
	originalConversationExecutionLocks := conversationExecutionLocks

	registry = tool.NewRegistry()
	mainIntegrationFakeMCPToolExecutions.Store(0)
	conversationEventReceivers = service.NewConversationEventReceivers()
	conversationExecutionLocks = service.NewConversationExecutionLocks()
	registerFakeMCPToolError := registry.RegisterFunctionTool(
		"mcp_playwright__read_page",
		"fake MCP tool used by the integration test",
		map[string]any{"type": "object"},
		func(toolArguments map[string]any) (string, error) {
			mainIntegrationFakeMCPToolExecutions.Add(1)
			return "fake MCP result", nil
		},
	)
	if registerFakeMCPToolError != nil {
		t.Fatalf("register fake MCP tool: %v", registerFakeMCPToolError)
	}

	return func() {
		registry = originalRegistry
		http.DefaultClient = originalHTTPClient
		conversationEventReceivers = originalConversationEventReceivers
		conversationExecutionLocks = originalConversationExecutionLocks
	}
}

func waitForBackgroundReplyCompletedEvent(
	t *testing.T,
	conversationEventChannel chan []byte,
	expectedReplyText string,
) {
	t.Helper()

	deadline := time.After(5 * time.Second)
	for {
		select {
		case conversationEventJSON := <-conversationEventChannel:
			var backgroundReplyCompletedEvent BackgroundAgentReplyCompletedEvent
			decodeConversationEventError := json.Unmarshal(
				conversationEventJSON,
				&backgroundReplyCompletedEvent,
			)
			if decodeConversationEventError != nil {
				t.Fatalf(
					"decode background event %q: %v",
					conversationEventJSON,
					decodeConversationEventError,
				)
			}
			if backgroundReplyCompletedEvent.Type !=
				"background_reply_completed" {
				continue
			}
			if backgroundReplyCompletedEvent.Text != expectedReplyText {
				t.Fatalf(
					"background reply = %q, want %q",
					backgroundReplyCompletedEvent.Text,
					expectedReplyText,
				)
			}
			return

		case <-deadline:
			t.Fatal("background main Agent reply was not completed")
		}
	}
}

func mainIntegrationMessagesContainText(
	messages []model.Message,
	expectedText string,
) bool {
	for _, message := range messages {
		for _, messageContentBlock := range message.Content {
			textContentBlock, isTextContentBlock :=
				messageContentBlock.(model.TextContentBlock)
			if isTextContentBlock &&
				strings.Contains(textContentBlock.Text, expectedText) {
				return true
			}
		}
	}
	return false
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
	if providerTransport.foregroundRequests.Load() != 1 {
		t.Fatalf(
			"foreground main Agent request count = %d, want 1",
			providerTransport.foregroundRequests.Load(),
		)
	}
	if providerTransport.callbackToolCount.Load() != 0 {
		t.Fatalf(
			"callback tool count = %d, want 0",
			providerTransport.callbackToolCount.Load(),
		)
	}
	if mainIntegrationFakeMCPToolExecutions.Load() != 0 {
		t.Fatalf(
			"foreground MCP tool execution count = %d, want 0",
			mainIntegrationFakeMCPToolExecutions.Load(),
		)
	}

	providerTransport.toolResultMutex.Lock()
	subAgentToolNames := make(map[string]bool)
	for toolName, toolExists := range providerTransport.subAgentToolNames {
		subAgentToolNames[toolName] = toolExists
	}
	providerTransport.toolResultMutex.Unlock()
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
		"subAgentTasks": []map[string]any{
			{
				"taskId":        "first",
				"task":          "first independent task",
				"maximumRounds": 20,
			},
			{
				"taskId":        "second",
				"task":          "second independent task",
				"maximumRounds": 20,
			},
		},
	})
	return newProviderHTTPResponse(
		http.StatusOK,
		fmt.Sprintf(
			`{"content":[{"type":"tool_use","id":"run-subagents","name":"run_subagent","input":%s},{"type":"tool_use","id":"read-page","name":"mcp_playwright__read_page","input":{}}],"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":5}}`,
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
		"subAgentTasks": []map[string]any{
			{
				"taskId":        "first",
				"task":          "first independent task",
				"maximumRounds": 20,
			},
			{
				"taskId":        "second",
				"task":          "second independent task",
				"maximumRounds": 20,
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
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"read-page","name":"mcp_playwright__read_page"}}`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
		`data: {"type":"content_block_stop","index":1}`,
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
	textContentBlock, isTextContentBlock :=
		messages[0].Content[0].(model.TextContentBlock)
	if !isTextContentBlock {
		return ""
	}
	return textContentBlock.Text
}

func findMainIntegrationToolResult(messages []model.Message) string {
	for _, message := range messages {
		for _, messageContentBlock := range message.Content {
			toolResultContentBlock, isToolResultContentBlock :=
				messageContentBlock.(model.ToolResultContentBlock)
			if isToolResultContentBlock {
				return toolResultContentBlock.Content
			}
		}
	}
	return ""
}
