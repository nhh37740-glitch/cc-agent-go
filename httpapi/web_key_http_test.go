package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowserDeepSeekKeyIsolationAndRemoval(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("CC_AGENT_LOCAL_CONFIG", "missing-config-for-test.json")
	server := NewServer(Dependencies{})

	firstKey := "first-browser-test-key"
	put := httptest.NewRequest(http.MethodPut, "https://example.test/api/settings/deepseek-key",
		strings.NewReader(`{"apiKey":"`+firstKey+`"}`))
	put.Header.Set("Content-Type", "application/json")
	put.Header.Set("Origin", "https://example.test")
	putResult := httptest.NewRecorder()
	server.handleDeepSeekKey(putResult, put)
	if putResult.Code != http.StatusNoContent || len(putResult.Result().Cookies()) != 1 {
		t.Fatalf("PUT status = %d, cookies = %d", putResult.Code, len(putResult.Result().Cookies()))
	}
	if strings.Contains(putResult.Body.String(), firstKey) ||
		strings.Contains(putResult.Result().Cookies()[0].Value, firstKey) {
		t.Fatal("credential leaked in PUT response")
	}
	cookie := putResult.Result().Cookies()[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("browser credential cookie lacks isolation attributes")
	}

	firstRequest := httptest.NewRequest(http.MethodGet, "https://example.test/api/settings/deepseek-key", nil)
	firstRequest.AddCookie(cookie)
	firstStatus := httptest.NewRecorder()
	server.handleDeepSeekKey(firstStatus, firstRequest)
	if !strings.Contains(firstStatus.Body.String(), `"source":"browser"`) ||
		strings.Contains(firstStatus.Body.String(), firstKey) ||
		server.configForWebRequest(firstRequest).ApiKey != firstKey {
		t.Fatal("first browser status or model configuration is incorrect")
	}
	replacement := httptest.NewRequest(http.MethodPut, "https://example.test/api/settings/deepseek-key",
		strings.NewReader(`{"apiKey":"replacement-test-key"}`))
	replacement.Header.Set("Content-Type", "application/json")
	replacement.Header.Set("Origin", "https://example.test")
	replacement.AddCookie(cookie)
	replacementResult := httptest.NewRecorder()
	server.handleDeepSeekKey(replacementResult, replacement)
	if replacementResult.Code != http.StatusNoContent ||
		server.configForWebRequest(firstRequest).ApiKey != "replacement-test-key" {
		t.Fatal("replacement did not update this browser credential")
	}

	otherRequest := httptest.NewRequest(http.MethodGet, "https://example.test/api/settings/deepseek-key", nil)
	otherStatus := httptest.NewRecorder()
	server.handleDeepSeekKey(otherStatus, otherRequest)
	if strings.Contains(otherStatus.Body.String(), `"source":"browser"`) ||
		server.configForWebRequest(otherRequest).ApiKey != "" {
		t.Fatal("second browser inherited first browser credential")
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "https://example.test/api/settings/deepseek-key", nil)
	deleteRequest.AddCookie(cookie)
	deleteResult := httptest.NewRecorder()
	server.handleDeepSeekKey(deleteResult, deleteRequest)
	if deleteResult.Code != http.StatusNoContent ||
		server.configForWebRequest(firstRequest).ApiKey != "" {
		t.Fatal("deleted browser credential remained available")
	}
	t.Setenv("DEEPSEEK_API_KEY", "server-default-test-key")
	if server.configForWebRequest(firstRequest).ApiKey != "server-default-test-key" {
		t.Fatal("server default did not resume after browser credential removal")
	}
}

func TestBrowserDeepSeekKeyRejectsCrossOriginMutation(t *testing.T) {
	server := NewServer(Dependencies{})
	request := httptest.NewRequest(http.MethodPut, "https://example.test/api/settings/deepseek-key",
		strings.NewReader(`{"apiKey":"test-key"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://other.test")
	result := httptest.NewRecorder()
	server.handleDeepSeekKey(result, request)
	if result.Code != http.StatusForbidden || len(result.Result().Cookies()) != 0 {
		t.Fatalf("cross-origin PUT returned %d", result.Code)
	}
}

func TestBrowserDeepSeekKeyRequiresHTTPSOrLoopback(t *testing.T) {
	server := NewServer(Dependencies{})
	for _, testCase := range []struct {
		url      string
		origin   string
		wantCode int
	}{
		{"http://example.test/api/settings/deepseek-key", "http://example.test", http.StatusForbidden},
		{"http://localhost:8080/api/settings/deepseek-key", "http://localhost:8080", http.StatusNoContent},
		{"http://127.0.0.1:8080/api/settings/deepseek-key", "http://127.0.0.1:8080", http.StatusNoContent},
	} {
		request := httptest.NewRequest(http.MethodPut, testCase.url,
			strings.NewReader(`{"apiKey":"test-key"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", testCase.origin)
		result := httptest.NewRecorder()
		server.handleDeepSeekKey(result, request)
		if result.Code != testCase.wantCode {
			t.Errorf("PUT %s = %d, want %d", testCase.url, result.Code, testCase.wantCode)
		}
	}
}

func TestDeepSeekSettingsAndAssetsRegisteredOnServerRouter(t *testing.T) {
	server := NewServer(Dependencies{})
	for _, route := range []string{
		"/api/settings/deepseek-key",
		"/deepseek-key-settings.js",
		"/deepseek-key-settings.css",
	} {
		result := httptest.NewRecorder()
		server.Handler().ServeHTTP(result,
			httptest.NewRequest(http.MethodGet, "http://localhost"+route, nil))
		if result.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", route, result.Code)
		}
	}
}
