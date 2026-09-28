package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cc-agent-go/config"
	"cc-agent-go/memory"
	"cc-agent-go/service"
	"cc-agent-go/tool"
)

var testServer *Server

func TestMain(testingProcess *testing.M) {
	currentDirectory, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "get test working directory:", err)
		os.Exit(1)
	}
	repositoryRootCandidates := []string{
		currentDirectory,
		filepath.Join(currentDirectory, ".."),
	}
	repositoryRootFound := false
	for _, candidateDirectory := range repositoryRootCandidates {
		if _, statError := os.Stat(filepath.Join(candidateDirectory, "go.mod")); statError != nil {
			continue
		}
		if chdirError := os.Chdir(candidateDirectory); chdirError != nil {
			fmt.Fprintln(os.Stderr, "change to repository root:", chdirError)
			os.Exit(1)
		}
		repositoryRootFound = true
		break
	}
	if !repositoryRootFound {
		fmt.Fprintln(os.Stderr, "repository root with go.mod was not found")
		os.Exit(1)
	}
	isolatedConfigDirectory, err := os.MkdirTemp("", "cc-agent-go-httpapi-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create isolated test config directory:", err)
		os.Exit(1)
	}
	if err := os.Setenv("CC_AGENT_LOCAL_CONFIG", filepath.Join(isolatedConfigDirectory, "missing-config.json")); err != nil {
		fmt.Fprintln(os.Stderr, "isolate local config path:", err)
		_ = os.RemoveAll(isolatedConfigDirectory)
		os.Exit(1)
	}
	testServer = NewServer(Dependencies{})
	exitCode := testingProcess.Run()
	_ = os.RemoveAll(isolatedConfigDirectory)
	os.Exit(exitCode)
}

func TestNewServerInjectsStateAndKeepsDefaultsIsolated(t *testing.T) {
	registry := tool.NewRegistry()
	eventReceivers := service.NewConversationEventReceivers()
	executionLocks := service.NewConversationExecutionLocks()
	runRegistry := service.NewConversationRunRegistry()
	conversationStore := memory.NewProjectConversationStore()
	configLoadCalls := 0
	server := NewServer(Dependencies{
		LoadConfig: func() config.Config {
			configLoadCalls++
			return config.Config{Model: "injected-model"}
		},
		Registry:                   registry,
		ConversationEventReceivers: eventReceivers,
		ConversationExecutionLocks: executionLocks,
		ConversationRunRegistry:    runRegistry,
		ProjectConversationStore:   conversationStore,
	})
	if server.registry != registry || server.conversationEventReceivers != eventReceivers ||
		server.conversationExecutionLocks != executionLocks ||
		server.conversationRunRegistry != runRegistry ||
		server.projectConversationStore != conversationStore {
		t.Fatal("server did not retain injected dependencies")
	}
	if got := server.loadConfig().Model; got != "injected-model" || configLoadCalls != 1 {
		t.Fatalf("injected config loader returned %q after %d calls", got, configLoadCalls)
	}
	otherServer := NewServer(Dependencies{})
	if otherServer.registry == server.registry ||
		otherServer.conversationEventReceivers == server.conversationEventReceivers ||
		otherServer.conversationExecutionLocks == server.conversationExecutionLocks ||
		otherServer.conversationRunRegistry == server.conversationRunRegistry ||
		otherServer.projectConversationStore == server.projectConversationStore {
		t.Fatal("default dependencies are shared between server instances")
	}
}

func TestServerHandlerPreservesLegacyPagesAndChatRoutes(t *testing.T) {
	server := NewServer(Dependencies{LoadConfig: func() config.Config { return config.Config{} }})
	handler := server.Handler()
	for _, pagePath := range []string{"/", "/harness", "/council"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, pagePath, nil))
		if response.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, want %d", pagePath, response.Code, http.StatusOK)
		}
	}
	invalidChat := httptest.NewRecorder()
	handler.ServeHTTP(invalidChat, httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader("{")))
	if invalidChat.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/chat status = %d, want %d", invalidChat.Code, http.StatusBadRequest)
	}
	invalidStream := httptest.NewRecorder()
	handler.ServeHTTP(invalidStream, httptest.NewRequest(http.MethodPost, "/api/chat/stream", strings.NewReader("{")))
	if invalidStream.Code != http.StatusOK || !strings.Contains(invalidStream.Body.String(), `"type":"error"`) {
		t.Fatalf("POST /api/chat/stream response = HTTP %d %q", invalidStream.Code, invalidStream.Body.String())
	}
	unknown := httptest.NewRecorder()
	handler.ServeHTTP(unknown, httptest.NewRequest(http.MethodGet, "/not-a-route", nil))
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown route status = %d, want %d", unknown.Code, http.StatusNotFound)
	}
}

func TestServerHandlerReturnsNotFoundForUnknownNestedPage(t *testing.T) {
	server := NewServer(Dependencies{LoadConfig: func() config.Config { return config.Config{} }})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/unregistered/nested/page", nil))

	if response.Code != http.StatusNotFound {
		t.Fatalf("GET /unregistered/nested/page status = %d, want %d", response.Code, http.StatusNotFound)
	}
}
