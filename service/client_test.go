package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cc-agent-go/config"
)

func TestChatClassifiesProviderAuthErrorAndRedactsAPIKey(t *testing.T) {
	const apiKey = "v11-test-secret-credential"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key ` + apiKey + `"}}`))
	}))
	t.Cleanup(server.Close)

	_, err := Chat(context.Background(), nil, "", config.Config{
		ApiKey:      apiKey,
		ApiEndpoint: server.URL,
		Model:       "test-model",
	}, nil, 16)
	if err == nil {
		t.Fatal("Chat returned nil error")
	}

	var appErr *AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("error type = %T, want *AppError", err)
	}
	if appErr.Kind != ErrorProviderAuth {
		t.Fatalf("kind = %q, want %q", appErr.Kind, ErrorProviderAuth)
	}
	if appErr.ProviderStatus != http.StatusUnauthorized {
		t.Fatalf("provider status = %d, want %d", appErr.ProviderStatus, http.StatusUnauthorized)
	}
	if strings.Contains(err.Error(), apiKey) {
		t.Fatal("error contains API key")
	}
}

func TestChatClassifiesInvalidProviderResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not-json"))
	}))
	t.Cleanup(server.Close)

	_, err := Chat(context.Background(), nil, "", config.Config{
		ApiKey:      "test-key",
		ApiEndpoint: server.URL,
		Model:       "test-model",
	}, nil, 16)
	if err == nil {
		t.Fatal("Chat returned nil error")
	}

	var appErr *AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("error type = %T, want *AppError", err)
	}
	if appErr.Kind != ErrorProviderResponseInvalid {
		t.Fatalf("kind = %q, want %q", appErr.Kind, ErrorProviderResponseInvalid)
	}
}
