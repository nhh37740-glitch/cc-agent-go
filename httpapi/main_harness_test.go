package httpapi

import (
	"cc-agent-go/config"
	"cc-agent-go/harness"
	"cc-agent-go/model"
	"cc-agent-go/modeltoken"
	"cc-agent-go/service"
	"cc-agent-go/tool"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

// arrangeHarnessPromptsOK 把 Harness prompt 全局状态设为已加载。
func arrangeHarnessPromptsOK(t *testing.T) {
	t.Helper()
	testServer.harnessPrompts = harness.Prompts{
		HarnessSystemPrompt:      "测试 Harness prompt",
		ManagedAgentSystemPrompt: "测试执行 prompt",
	}
	testServer.harnessPromptsLoadError = nil
	t.Cleanup(func() { testServer.harnessPromptsLoadError = nil })
}

func TestHandleHarnessChatStreamRejectsInvalidJSON(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/harness/chat/stream",
		strings.NewReader("{"),
	)

	testServer.handleHarnessChatStream(recorder, request)

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

			testServer.handleHarnessChatStream(recorder, request)

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
	testServer.harnessPromptsLoadError = os.ErrNotExist
	t.Cleanup(func() { testServer.harnessPromptsLoadError = nil })

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
	testServer.handleHarnessChatStream(chatRecorder, chatRequest)
	if chatRecorder.Code == http.StatusOK {
		t.Fatal("prompt 缺失时 chat 路由不应返回 200")
	}

	agentsRecorder := httptest.NewRecorder()
	agentsRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/harness/agents?workingDirectory="+url.QueryEscape(workingDirectory),
		nil,
	)
	testServer.handleHarnessAgents(agentsRecorder, agentsRequest)
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

	// 创建 Runtime 后应自动出现 4 个常驻 Agent（idle）。
	harnessRuntime, createRuntimeError := harness.GetOrCreateRuntime(
		workingDirectory,
		testServer.buildHarnessRuntimeDependencies(config.Load()),
	)
	if createRuntimeError != nil {
		t.Fatalf("GetOrCreateRuntime: %v", createRuntimeError)
	}

	initialRecorder := httptest.NewRecorder()
	initialRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/harness/agents?workingDirectory="+url.QueryEscape(workingDirectory),
		nil,
	)
	testServer.handleHarnessAgents(initialRecorder, initialRequest)
	if initialRecorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", initialRecorder.Code, http.StatusOK)
	}
	var initialResponse struct {
		Agents        []harness.ManagedAgentRecord `json:"agents"`
		MaximumAgents int                          `json:"maximumAgents"`
		RunningAgents int                          `json:"runningAgents"`
	}
	if decodeError := json.Unmarshal(initialRecorder.Body.Bytes(), &initialResponse); decodeError != nil {
		t.Fatalf("decode response: %v", decodeError)
	}
	if len(initialResponse.Agents) != len(harness.PermanentResidents) {
		t.Fatalf("初始名单应含 %d 个常驻 Agent，实际 %d 个: %+v",
			len(harness.PermanentResidents), len(initialResponse.Agents), initialResponse)
	}

	// 追加一个临时 Agent 后名单为常驻数 + 1。
	if _, _, upsertError := harnessRuntime.AgentRegistry().UpsertAgent("writer"); upsertError != nil {
		t.Fatalf("UpsertAgent: %v", upsertError)
	}

	rosterRecorder := httptest.NewRecorder()
	rosterRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/harness/agents?workingDirectory="+url.QueryEscape(workingDirectory),
		nil,
	)
	testServer.handleHarnessAgents(rosterRecorder, rosterRequest)
	var rosterResponse struct {
		Agents []harness.ManagedAgentRecord `json:"agents"`
	}
	if decodeError := json.Unmarshal(rosterRecorder.Body.Bytes(), &rosterResponse); decodeError != nil {
		t.Fatalf("decode response: %v", decodeError)
	}
	if len(rosterResponse.Agents) != len(harness.PermanentResidents)+1 {
		t.Fatalf("名单应含 %d 个 Agent（常驻 + 1 临时），实际 %d 个",
			len(harness.PermanentResidents)+1, len(rosterResponse.Agents))
	}
	temporaryNames := map[string]bool{}
	for _, agentRecord := range rosterResponse.Agents {
		temporaryNames[agentRecord.Name] = true
	}
	for _, residentDefinition := range harness.PermanentResidents {
		if !temporaryNames[residentDefinition.Name] {
			t.Fatalf("名单应包含常驻 %q", residentDefinition.Name)
		}
	}
	if !temporaryNames["writer"] {
		t.Fatalf("名单应包含临时 Agent writer")
	}
}

func TestHandleHarnessAgentMemoryReturnsSession(t *testing.T) {
	arrangeHarnessPromptsOK(t)
	workingDirectory := t.TempDir()

	harnessRuntime, createRuntimeError := harness.GetOrCreateRuntime(
		workingDirectory,
		testServer.buildHarnessRuntimeDependencies(config.Load()),
	)
	if createRuntimeError != nil {
		t.Fatalf("GetOrCreateRuntime: %v", createRuntimeError)
	}
	if _, _, upsertError := harnessRuntime.AgentRegistry().UpsertAgent("writer"); upsertError != nil {
		t.Fatalf("UpsertAgent: %v", upsertError)
	}
	seededSession := &model.SessionJson{
		ConversationId: "harness-agent-writer",
		Title:          "writer 的记忆",
		Messages: []model.Message{
			{
				Role: "user",
				Content: []model.MessageContentBlock{
					model.TextContentBlock{Text: "你是写作 Agent"},
				},
			},
		},
	}
	if saveError := testServer.projectConversationStore.SaveConversation(
		workingDirectory,
		seededSession,
	); saveError != nil {
		t.Fatalf("SaveConversation: %v", saveError)
	}

	harnessMux := http.NewServeMux()
	harnessMux.HandleFunc(
		"GET /api/harness/agents/{name}/memory",
		testServer.handleHarnessAgentMemory,
	)
	testServer := httptest.NewServer(harnessMux)
	defer testServer.Close()

	memoryHTTPResponse, getMemoryError := http.Get(
		testServer.URL + "/api/harness/agents/writer/memory?workingDirectory=" +
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
	if memoryResponse.Title != "writer 的记忆" ||
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
		tool.NewNativeCommandTool(),
		tool.NewSkillTool(),
		tool.NewCreateSkillTool(),
	} {
		if registerError := testServer.registry.Register(baseTool); registerError != nil {
			t.Fatalf("注册基础工具失败: %v", registerError)
		}
	}
	if registerMCPError := testServer.registry.RegisterFunctionTool(
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
		testServer.registry.Unregister("mcp_playwright__browser_navigate")
	})

	managedAgentTools := testServer.registry.CopyExcludingTools(generalSubAgentToolName)
	registeredNames := make(map[string]bool)
	for _, toolDefinition := range managedAgentTools.GetDefinitions() {
		registeredNames[toolDefinition["name"].(string)] = true
	}
	for _, expectedName := range []string{
		"command", "activate_skill", "create_skill", "mcp_playwright__browser_navigate",
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
	testServer.tokenCounter = tokenCounter
	testServer.modelContextWindowTokens =
		tokenizerConfiguration.MaximumContextTokens

	loadedHarnessPrompts, loadPromptsError := harness.LoadPrompts(
		"harness/system_prompt.md",
		"harness/managed_agent_prompt.md",
	)
	if loadPromptsError != nil {
		t.Fatalf("加载 Harness prompt 失败: %v", loadPromptsError)
	}
	testServer.harnessPrompts = loadedHarnessPrompts
	testServer.harnessPromptsLoadError = nil

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

	testServer.handleHarnessChatStream(recorder, request)

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

// TestHandleHarnessAgentHeartbeat 固定远程保活接口：
// 合法心跳刷新时间并返回 heartbeat=ok；未知 Agent 返回 400。
func TestHandleHarnessAgentHeartbeat(t *testing.T) {
	arrangeHarnessPromptsOK(t)
	workingDirectory := t.TempDir()

	harnessRuntime, createRuntimeError := harness.GetOrCreateRuntime(
		workingDirectory,
		testServer.buildHarnessRuntimeDependencies(config.Load()),
	)
	if createRuntimeError != nil {
		t.Fatalf("GetOrCreateRuntime: %v", createRuntimeError)
	}
	if _, _, upsertError := harnessRuntime.AgentRegistry().UpsertAgent("writer"); upsertError != nil {
		t.Fatalf("UpsertAgent: %v", upsertError)
	}
	beforeRecord, _ := harnessRuntime.AgentRegistry().GetAgent("writer")

	heartbeatMux := http.NewServeMux()
	heartbeatMux.HandleFunc(
		"POST /api/harness/agents/{name}/heartbeat",
		testServer.handleHarnessAgentHeartbeat,
	)
	testServer := httptest.NewServer(heartbeatMux)
	defer testServer.Close()

	heartbeatHTTPResponse, heartbeatError := http.Post(
		testServer.URL+"/api/harness/agents/writer/heartbeat?workingDirectory="+
			url.QueryEscape(workingDirectory),
		"application/json",
		nil,
	)
	if heartbeatError != nil {
		t.Fatalf("POST heartbeat: %v", heartbeatError)
	}
	defer heartbeatHTTPResponse.Body.Close()
	if heartbeatHTTPResponse.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d",
			heartbeatHTTPResponse.StatusCode, http.StatusOK)
	}
	var heartbeatResponse map[string]any
	if decodeError := json.NewDecoder(heartbeatHTTPResponse.Body).Decode(&heartbeatResponse); decodeError != nil {
		t.Fatalf("decode response: %v", decodeError)
	}
	if heartbeatResponse["heartbeat"] != "ok" {
		t.Fatalf("心跳响应 = %+v", heartbeatResponse)
	}

	afterRecord, _ := harnessRuntime.AgentRegistry().GetAgent("writer")
	if afterRecord.LastHeartbeatAt < beforeRecord.LastHeartbeatAt {
		t.Fatalf("心跳后最后心跳时间应更新：before=%v after=%v",
			beforeRecord.LastHeartbeatAt, afterRecord.LastHeartbeatAt)
	}

	unknownHTTPResponse, unknownError := http.Post(
		testServer.URL+"/api/harness/agents/ghost/heartbeat?workingDirectory="+
			url.QueryEscape(workingDirectory),
		"application/json",
		nil,
	)
	if unknownError != nil {
		t.Fatalf("POST unknown heartbeat: %v", unknownError)
	}
	defer unknownHTTPResponse.Body.Close()
	if unknownHTTPResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("未知 Agent status = %d, want %d",
			unknownHTTPResponse.StatusCode, http.StatusBadRequest)
	}
}
