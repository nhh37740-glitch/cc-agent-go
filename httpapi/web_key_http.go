package httpapi

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"cc-agent-go/config"
)

func (server *Server) configForWebRequest(request *http.Request) config.Config {
	applicationConfig := server.loadConfig()
	if browserKey, found := server.browserDeepSeekKeys.Key(request); found {
		applicationConfig.ApiKey = browserKey
	}
	return applicationConfig
}

// configForRuntimeScope resolves the current key whenever a background
// Harness task starts. A removed browser key is not reused by later tasks.
func (server *Server) configForRuntimeScope(scope string) config.Config {
	applicationConfig := server.loadConfig()
	if browserKey, found := server.browserDeepSeekKeys.KeyForToken(scope); found {
		applicationConfig.ApiKey = browserKey
	}
	return applicationConfig
}

func sameOriginRequest(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return true // Non-browser clients cannot bypass SameSite browser cookie rules.
	}
	parsedOrigin, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return parsedOrigin.Host == request.Host &&
		(parsedOrigin.Scheme == "https" || parsedOrigin.Scheme == "http")
}

func safeKeyEntryContext(request *http.Request) bool {
	if request.TLS != nil {
		return true
	}
	if parsedOrigin, err := url.Parse(request.Header.Get("Origin")); err == nil &&
		parsedOrigin.Scheme == "https" && parsedOrigin.Host == request.Host {
		// TLS can terminate at the reverse proxy; browsers still send HTTPS Origin.
		return true
	}
	host := request.Host
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	return strings.EqualFold(host, "localhost") ||
		(net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback())
}

func (server *Server) handleDeepSeekKey(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.Header().Set("Content-Type", "application/json;charset=UTF-8")
	if request.Method == http.MethodGet {
		_, hasBrowserKey := server.browserDeepSeekKeys.Key(request)
		source := "none"
		if hasBrowserKey {
			source = "browser"
		} else if strings.TrimSpace(server.loadConfig().ApiKey) != "" {
			source = "server"
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"configured": source != "none", "source": source,
		})
		return
	}
	if !sameOriginRequest(request) {
		http.Error(writer, `{"error":"origin mismatch"}`, http.StatusForbidden)
		return
	}
	if request.Method == http.MethodDelete {
		server.browserDeepSeekKeys.Delete(writer, request)
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	if request.Method != http.MethodPut {
		writer.Header().Set("Allow", "GET, PUT, DELETE")
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !safeKeyEntryContext(request) {
		http.Error(writer, `{"error":"HTTPS or localhost required"}`, http.StatusForbidden)
		return
	}
	if !strings.HasPrefix(request.Header.Get("Content-Type"), "application/json") {
		http.Error(writer, `{"error":"JSON required"}`, http.StatusUnsupportedMediaType)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 8192)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input struct {
		APIKey string `json:"apiKey"`
	}
	if err := decoder.Decode(&input); err != nil {
		http.Error(writer, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		http.Error(writer, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if err := server.browserDeepSeekKeys.Put(writer, request, input.APIKey); err != nil {
		http.Error(writer, `{"error":"invalid key"}`, http.StatusBadRequest)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}
