package mcp

import "encoding/json"

// MCPJSONRPCMessage 用于解包 MCP Server 写入标准输出的一条 JSON-RPC 消息。
type MCPJSONRPCMessage struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      json.RawMessage  `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *MCPJSONRPCError `json:"error,omitempty"`
}

type MCPJSONRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type MCPImplementationInformation struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}

type MCPInitializeResult struct {
	ProtocolVersion string                       `json:"protocolVersion"`
	Capabilities    map[string]json.RawMessage   `json:"capabilities"`
	ServerInfo      MCPImplementationInformation `json:"serverInfo"`
	Instructions    string                       `json:"instructions,omitempty"`
}

// MCPServerToolDefinition 是 tools/list 返回的一项工具定义。
type MCPServerToolDefinition struct {
	Name         string         `json:"name"`
	Title        string         `json:"title,omitempty"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
}

type MCPToolListResult struct {
	Tools      []MCPServerToolDefinition `json:"tools"`
	NextCursor string                    `json:"nextCursor,omitempty"`
}

type MCPToolCallResult struct {
	Content           []map[string]any `json:"content"`
	StructuredContent map[string]any   `json:"structuredContent,omitempty"`
	IsError           bool             `json:"isError,omitempty"`
}
