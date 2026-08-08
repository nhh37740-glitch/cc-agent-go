package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"cc-agent-go/config"
	"cc-agent-go/harness"
	"cc-agent-go/model"
	"cc-agent-go/modeltoken"
	"cc-agent-go/service"
	"cc-agent-go/tool"
)

// arrangeHarnessPromptsOK 把 Harness prompt 全局状态设为已加载。
func arrangeHarnessPromptsOK(t *testing.T) {
	t.Helper()
	harnessPrompts = harness.Prompts{
		HarnessSystemPrompt:      "测试 Harness prompt",
		ManagedAgentSystemPrompt: "测试执行 prompt",
	}
	harnessPromptsLoadError = nil
	t.Cleanup(func() { harnessPromptsLoadError = nil })
}

func TestHandleHarnessChatStreamRejectsInvalidJSON(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/harness/chat/stream",
		strings.NewReader("{"),
	)

	handleHarnessChatStream(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	var errorResponse model.ErrorResponse
	if decodeError := json.Unmarshal(recorder.Body.Bytes(), &errorResponse); decodeError != nil {
		t.Fatalf("decode response: %v", decodeError)
	}
	if errorResponse.Code != string(service.ErrorInvalidRequest) {
		t.Fatalf("code = %q, want %q", errorResponse.Code, service.ErrorInvalidRequest)
	}
}

func TestHandleHarnessChatStreamRejectsInvalidWorkingDirectory(t *testing.T) {
	missingMessageBody, _ := json.Marshal(map[string]string{
		"workingDirectory": t.TempDir(),
		"message":          "",
	})
	testCases := []struct {
		testName string
		body     string
	}{
		{
			testName: "missing workingDirectory",
			body:     `{"workingDirectory":"","message":"你好"}`,
		},
		{
			testName: "relative workingDirectory",
			body:     `{"workingDirectory":"projects/demo","message":"你好"}`,
		},
		{
			testName: "missing message",
			body:     string(missingMessageBody),
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.testName, func(t *testing.T) {
			arrangeHarnessPromptsOK(t)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/harness/chat/stream",
				strings.NewReader(testCase.body),
			)

			handleHarnessChatStream(recorder, request)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
			}
			var errorResponse model.ErrorResponse
			if decodeError := json.Unmarshal(
				recorder.Body.Bytes(),
				&errorResponse,
			); decodeError != nil {
				t.Fatalf("decode response: %v", decodeError)
			}
			if errorResponse.Code != string(service.ErrorInvalidRequest) {
				t.Fatalf("code = %q, want %q",
					errorResponse.Code, service.ErrorInvalidRequest)
			}
		})
	}
}

func TestHarnessRoutesReturnConfigErrorWhenPromptsMissing(t *testing.T) {
	workingDirectory := t.TempDir()
	harnessPromptsLoadError = os.ErrNotExist
	t.Cleanup(func() { harnessPromptsLoadError = nil })

	chatBody, _ := json.Marshal(map[string]string{
		"workingDirectory": workingDirectory,
		"message":          "你好",
	})
	chatRecorder := httptest.NewRecorder()
	chatRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/harness/chat/stream",
		strings.NewReader(string(chatBody)),
	)
	handleHarnessChatStream(chatRecorder, chatRequest)
	if chatRecorder.Code == http.StatusOK {
		t.Fatal("prompt 缺失时 chat 路由不应返回 200")
	}

	agentsRecorder := httptest.NewRecorder()
	agentsRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/harness/agents?workingDirectory="+url.QueryEscape(workingDirectory),
		nil,
	)
	handleHarnessAgents(agentsRecorder, agentsRequest)
	if agentsRecorder.Code == http.StatusOK {
		t.Fatal("prompt 缺失时名单路由不应返回 200")
	}
	var errorResponse model.ErrorResponse
	if decodeError := json.Unmarshal(agentsRecorder.Body.Bytes(), &errorResponse); decodeError != nil {
		t.Fatalf("decode response: %v", decodeError)
	}
	if errorResponse.Code != string(service.ErrorConfig) {
		t.Fatalf("code = %q, want %q", errorResponse.Code, service.ErrorConfig)
	}
}

func TestHandleHarnessAgentsReturnsRoster(t *testing.T) {
	arrangeHarnessPromptsOK(t)
	workingDirectory := t.TempDir()

	// 空名单。
	emptyRecorder := httptest.NewRecorder()
	emptyRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/harness/agents?workingDirectory="+url.QueryEscape(workingDirectory),
		nil,
	)
	handleHarnessAgents(emptyRecorder, emptyRequest)
	if emptyRecorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", emptyRecorder.Code, http.StatusOK)
	}
	var emptyResponse struct {
		Agents        []harness.ManagedAgentRecord `json:"agents"`
		MaximumAgents int                          `json:"maximumAgents"`
		RunningAgents int                          `json:"runningAgents"`
	}
	if decodeError := json.Unmarshal(emptyRecorder.Body.Bytes(), &emptyResponse); decodeError != nil {
		t.Fatalf("decode response: %v", decodeError)
	}
	if len(emptyResponse.Agents) != 0 || emptyResponse.MaximumAgents != 11 {
		t.Fatalf("空名单响应 = %+v", emptyResponse)
	}

	// 通过同一个目录的 Runtime upsert 两个 Agent 后名单可见。
	harnessRuntime, createRuntimeError := harness.GetOrCreateRuntime(
		workingDirectory,
		buildHarnessRuntimeDependencies(config.Load()),
	)
	if createRuntimeError != nil {
		t.Fatalf("GetOrCreateRuntime: %v", createRuntimeError)
	}
	if _, _, upsertError := harnessRuntime.AgentRegistry().UpsertAgent("coder"); upsertError != nil {
		t.Fatalf("UpsertAgent: %v", upsertError)
	}
	if _, _, upsertError := harnessRuntime.AgentRegistry().UpsertAgent("writer"); upsertError != nil {
		t.Fatalf("UpsertAgent: %v", upsertError)
	}

	rosterRecorder := httptest.NewRecorder()
	rosterRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/harness/agents?workingDirectory="+url.QueryEscape(workingDirectory),
		nil,
	)
	handleHarnessAgents(rosterRecorder, rosterRequest)
	var rosterResponse struct {
		Agents []harness.ManagedAgentRecord `json:"agents"`
	}
	if decodeError := json.Unmarshal(rosterRecorder.Body.Bytes(), &rosterResponse); decodeError != nil {
		t.Fatalf("decode response: %v", decodeError)
	}
	if len(rosterResponse.Agents) != 2 ||
		rosterResponse.Agents[0].Name != "coder" ||
		rosterResponse.Agents[1].Name != "writer" {
		t.Fatalf("名单响应 = %+v", rosterResponse.Agents)
	}
}

func TestHandleHarnessAgentMemoryReturnsSession(t *testing.T) {
	arrangeHarnessPromptsOK(t)
	workingDirectory := t.TempDir()

	harnessRuntime, createRuntimeError := harness.GetOrCreateRuntime(
		workingDirectory,
		buildHarnessRuntimeDependencies(config.Load()),
	)
	if createRuntimeError != nil {
		t.Fatalf("GetOrCreateRuntime: %v", createRuntimeError)
	}
	if _, _, upsertError := harnessRuntime.AgentRegistry().UpsertAgent("coder"); upsertError != nil {
		t.Fatalf("UpsertAgent: %v", upsertError)
	}
	seededSession := &model.SessionJson{
		ConversationId: "harness-agent-coder",
		Title:          "coder 的记忆",
		Messages: []model.Message{
			{
				Role: "user",
				Content: []model.MessageContentBlock{
					model.TextContentBlock{Text: "你是编码 Agent"},
				},
			},
		},
	}
	if saveError := projectConversationStore.SaveConversation(
		workingDirectory,
		seededSession,
	); saveError != nil {
		t.Fatalf("SaveConversation: %v", saveError)
	}

	harnessMux := http.NewServeMux()
	harnessMux.HandleFunc(
		"GET /api/harness/agents/{name}/memory",
		handleHarnessAgentMemory,
	)
	testServer := httptest.NewServer(harnessMux)
	defer testServer.Close()

	memoryHTTPResponse, getMemoryError := http.Get(
		testServer.URL + "/api/harness/agents/coder/memory?workingDirectory=" +
			url.QueryEscape(workingDirectory),
	)
	if getMemoryError != nil {
		t.Fatalf("GET memory: %v", getMemoryError)
	}
	defer memoryHTTPResponse.Body.Close()
	if memoryHTTPResponse.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d",
			memoryHTTPResponse.StatusCode, http.StatusOK)
	}
	var memoryResponse model.SessionJson
	if decodeError := json.NewDecoder(memoryHTTPResponse.Body).Decode(&memoryResponse); decodeError != nil {
		t.Fatalf("decode response: %v", decodeError)
	}
	if memoryResponse.Title != "coder 的记忆" ||
		len(memoryResponse.Messages) != 1 {
		t.Fatalf("记忆响应 = %+v", memoryResponse)
	}

	unknownHTTPResponse, getUnknownError := http.Get(
		testServer.URL + "/api/harness/agents/ghost/memory?workingDirectory=" +
			url.QueryEscape(workingDirectory),
	)
	if getUnknownError != nil {
		t.Fatalf("GET unknown memory: %v", getUnknownError)
	}
	defer unknownHTTPResponse.Body.Close()
	if unknownHTTPResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("未知 Agent status = %d, want %d",
			unknownHTTPResponse.StatusCode, http.StatusBadRequest)
	}
}

func TestManagedAgentToolRegistriesComposition(t *testing.T) {
	for _, baseTool := range []tool.Tool{
		tool.NewBashTool(),
		tool.NewSkillTool(),
		tool.NewCreateSkillTool(),
	} {
		if registerError := registry.Register(baseTool); registerError != nil {
			t.Fatalf("注册基础工具失败: %v", registerError)
		}
	}
	if registerMCPError := registry.RegisterFunctionTool(
		"mcp_playwright__browser_navigate",
		"假 MCP 工具",
		map[string]any{"type": "object", "properties": map[string]any{}},
		func(toolArguments map[string]any,
			executionEnvironment tool.ToolExecutionEnvironment) (string, error) {
			return "ok", nil
		},
	); registerMCPError != nil {
		t.Fatalf("注册假 MCP 工具失败: %v", registerMCPError)
	}
	t.Cleanup(func() {
		registry.Unregister("mcp_playwright__browser_navigate")
	})

	managedAgentTools := registry.CopyExcludingTools(generalSubAgentToolName)
	registeredNames := make(map[string]bool)
	for _, toolDefinition := range managedAgentTools.GetDefinitions() {
		registeredNames[toolDefinition["name"].(string)] = true
	}
	for _, expectedName := range []string{
		"bash", "activate_skill", "create_skill", "mcp_playwright__browser_navigate",
	} {
		if !registeredNames[expectedName] {
			t.Fatalf("被管理 Agent 工具表应包含 %q，实际: %v",
				expectedName, registeredNames)
		}
	}
	if registeredNames[generalSubAgentToolName] {
		t.Fatalf("被管理 Agent 工具表不应包含 %q", generalSubAgentToolName)
	}
}

// TestHandleHarnessChatStreamIntegration 用真实 DeepSeek 走一遍
// Harness 对话 SSE：首帧 conversation_id、中间 Agent 事件、末尾 done。
func TestHandleHarnessChatStreamIntegration(t *testing.T) {
	if os.Getenv("RUN_HARNESS_DEEPSEEK_INTEGRATION") != "1" {
		t.Skip("set RUN_HARNESS_DEEPSEEK_INTEGRATION=1 to call DeepSeek")
	}
	applicationConfig := config.Load()
	if strings.TrimSpace(applicationConfig.ApiKey) == "" {
		t.Skip("DEEPSEEK_API_KEY 未配置")
	}
	tokenizerConfiguration, loadTokenizerError :=
		config.LoadModelTokenizerConfiguration(applicationConfig.Model)
	if loadTokenizerError != nil {
		t.Fatalf("加载 tokenizer 配置失败: %v", loadTokenizerError)
	}
	tokenCounter, createTokenCounterError :=
		modeltoken.NewHuggingFaceJSONTokenCounter(tokenizerConfiguration)
	if createTokenCounterError != nil {
		t.Fatalf("创建 token 计数器失败: %v", createTokenCounterError)
	}
	defer tokenCounter.Close()
	configuredTokenCounter = tokenCounter
	configuredModelContextWindowTokens =
		tokenizerConfiguration.MaximumContextTokens

	loadedHarnessPrompts, loadPromptsError := harness.LoadPrompts(
		"harness/system_prompt.md",
		"harness/managed_agent_prompt.md",
	)
	if loadPromptsError != nil {
		t.Fatalf("加载 Harness prompt 失败: %v", loadPromptsError)
	}
	harnessPrompts = loadedHarnessPrompts
	harnessPromptsLoadError = nil

	integrationBody, _ := json.Marshal(map[string]string{
		"workingDirectory": t.TempDir(),
		"message":          "不要调用任何工具，直接用一句话回复：HARNESS_OK",
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/harness/chat/stream",
		strings.NewReader(string(integrationBody)),
	)

	handleHarnessChatStream(recorder, request)

	streamBody := recorder.Body.String()
	for _, expectedFrame := range []string{
		`"type":"conversation_id"`,
		`"conversationId":"harness"`,
		`"type":"round_started"`,
		`"type":"done"`,
	} {
		if !strings.Contains(streamBody, expectedFrame) {
			t.Fatalf("SSE 流应包含 %s，实际:\n%s", expectedFrame, streamBody)
		}
	}
}
