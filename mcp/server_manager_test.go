package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"cc-agent-go/tool"
)

func TestMCPServerManagerStartsRegistersCallsAndStopsServer(t *testing.T) {
	testExecutablePath, absolutePathError := filepath.Abs(os.Args[0])
	if absolutePathError != nil {
		t.Fatalf("get test executable path: %v", absolutePathError)
	}
	temporaryDirectory := t.TempDir()
	mcpServerConfigurationFilePath := filepath.Join(temporaryDirectory, "mcp_servers.json")
	mcpProtocolMessagesFilePath := filepath.Join(temporaryDirectory, "messages.json")

	mcpServerConfigurationFile := MCPServerConfigurationFile{
		MCPServers: []MCPServerConfiguration{{
			Name:                  "fake",
			DisplayName:           "Fake MCP Server",
			Transport:             "stdio",
			Command:               testExecutablePath,
			Arguments:             []string{"-test.run=TestMCPFakeServerProcess"},
			WorkingDirectory:      temporaryDirectory,
			RequestTimeoutSeconds: 5,
			EnvironmentVariables: map[string]string{
				"GO_WANT_MCP_FAKE_SERVER": "1",
			},
		}},
	}
	writeTestJSONFile(t, mcpServerConfigurationFilePath, mcpServerConfigurationFile)
	writeTestJSONFile(t, mcpProtocolMessagesFilePath, testMCPProtocolMessages())

	mcpServerManager, createManagerError := NewMCPServerManager(
		mcpServerConfigurationFilePath,
		mcpProtocolMessagesFilePath,
	)
	if createManagerError != nil {
		t.Fatalf("create MCP server manager: %v", createManagerError)
	}
	agentToolRegistry := tool.NewRegistry()
	t.Cleanup(func() {
		_ = mcpServerManager.CloseAllStartedMCPServers(agentToolRegistry)
	})

	serverStatuses, startServerError := mcpServerManager.StartSelectedMCPServers(
		context.Background(),
		[]string{"fake"},
		agentToolRegistry,
	)
	if startServerError != nil {
		t.Fatalf("start fake MCP server: %v", startServerError)
	}
	if len(serverStatuses) != 1 || serverStatuses[0].Status != "running" {
		t.Fatalf("server statuses = %#v, want one running server", serverStatuses)
	}
	if serverStatuses[0].RegisteredToolCount != 1 {
		t.Fatalf("registered tool count = %d, want 1", serverStatuses[0].RegisteredToolCount)
	}

	agentToolResult, executeToolError := agentToolRegistry.Execute(
		"mcp_fake__fake_echo",
		map[string]any{"message": "hello"},
	)
	if executeToolError != nil {
		t.Fatalf("execute registered MCP tool: %v", executeToolError)
	}
	if agentToolResult != "fake tool result" {
		t.Fatalf("tool result = %q, want %q", agentToolResult, "fake tool result")
	}

	serverStatuses, stopServerError := mcpServerManager.StartSelectedMCPServers(
		context.Background(),
		[]string{},
		agentToolRegistry,
	)
	if stopServerError != nil {
		t.Fatalf("stop fake MCP server: %v", stopServerError)
	}
	if serverStatuses[0].Status != "stopped" || serverStatuses[0].RegisteredToolCount != 0 {
		t.Fatalf("server status after stop = %#v", serverStatuses[0])
	}
	if _, executeRemovedToolError := agentToolRegistry.Execute(
		"mcp_fake__fake_echo",
		map[string]any{},
	); executeRemovedToolError == nil {
		t.Fatal("removed MCP tool is still executable")
	}
}

func TestMCPFakeServerProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_FAKE_SERVER") != "1" {
		return
	}

	standardInputScanner := bufio.NewScanner(os.Stdin)
	standardOutputEncoder := json.NewEncoder(os.Stdout)
	for standardInputScanner.Scan() {
		var clientMessage map[string]any
		if decodeClientMessageError := json.Unmarshal(
			standardInputScanner.Bytes(),
			&clientMessage,
		); decodeClientMessageError != nil {
			os.Exit(2)
		}
		clientMethod, _ := clientMessage["method"].(string)
		clientRequestID, hasRequestID := clientMessage["id"]
		if !hasRequestID {
			continue
		}

		var responseResult any
		switch clientMethod {
		case "initialize":
			responseResult = map[string]any{
				"protocolVersion": "2025-11-25",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fake", "version": "1.0.0"},
			}
		case "tools/list":
			responseResult = map[string]any{
				"tools": []map[string]any{{
					"name":        "fake_echo",
					"description": "Return a fixed test result",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"message": map[string]any{"type": "string"},
						},
					},
				}},
			}
		case "tools/call":
			responseResult = map[string]any{
				"content": []map[string]any{{
					"type": "text",
					"text": "fake tool result",
				}},
				"isError": false,
			}
		default:
			_ = standardOutputEncoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      clientRequestID,
				"error": map[string]any{
					"code":    -32601,
					"message": fmt.Sprintf("unsupported method %s", clientMethod),
				},
			})
			continue
		}

		if encodeResponseError := standardOutputEncoder.Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      clientRequestID,
			"result":  responseResult,
		}); encodeResponseError != nil {
			os.Exit(3)
		}
	}
	os.Exit(0)
}

func writeTestJSONFile(t *testing.T, filePath string, fileContent any) {
	t.Helper()
	encodedFileContent, encodeFileError := json.Marshal(fileContent)
	if encodeFileError != nil {
		t.Fatalf("encode %s: %v", filePath, encodeFileError)
	}
	if writeFileError := os.WriteFile(filePath, encodedFileContent, 0o600); writeFileError != nil {
		t.Fatalf("write %s: %v", filePath, writeFileError)
	}
}

func testMCPProtocolMessages() map[string]any {
	return map[string]any{
		"protocolVersion": "2025-11-25",
		"initializeRequestJSON": map[string]any{
			"jsonrpc": "2.0",
			"method":  "initialize",
			"params": map[string]any{
				"protocolVersion": "2025-11-25",
				"capabilities":    map[string]any{},
				"clientInfo":      map[string]any{"name": "test", "version": "1"},
			},
		},
		"initializedNotificationJSON": map[string]any{
			"jsonrpc": "2.0",
			"method":  "notifications/initialized",
		},
		"getToolListRequestJSON": map[string]any{
			"jsonrpc": "2.0",
			"method":  "tools/list",
			"params":  map[string]any{},
		},
		"callToolRequestJSON": map[string]any{
			"jsonrpc": "2.0",
			"method":  "tools/call",
			"params":  map[string]any{"name": "", "arguments": map[string]any{}},
		},
		"cancelRequestNotificationJSON": map[string]any{
			"jsonrpc": "2.0",
			"method":  "notifications/cancelled",
			"params":  map[string]any{"requestId": 0, "reason": "timeout"},
		},
		"methodNotFoundErrorResponseJSON": map[string]any{
			"jsonrpc": "2.0",
			"id":      nil,
			"error": map[string]any{
				"code":    -32601,
				"message": "Method not found",
			},
		},
	}
}
