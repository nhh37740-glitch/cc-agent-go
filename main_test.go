package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cc-agent-go/model"
	"cc-agent-go/service"
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
