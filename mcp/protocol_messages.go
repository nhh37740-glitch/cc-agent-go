package mcp

import (
	"encoding/json"
	"fmt"
	"os"
)

// MCPProtocolMessageTemplates 保存 messages.json 中实际发送的标准 MCP 消息。
type MCPProtocolMessageTemplates struct {
	ProtocolVersion                 string          `json:"protocolVersion"`
	InitializeRequestJSON           json.RawMessage `json:"initializeRequestJSON"`
	InitializedNotificationJSON     json.RawMessage `json:"initializedNotificationJSON"`
	GetToolListRequestJSON          json.RawMessage `json:"getToolListRequestJSON"`
	CallToolRequestJSON             json.RawMessage `json:"callToolRequestJSON"`
	CancelRequestNotificationJSON   json.RawMessage `json:"cancelRequestNotificationJSON"`
	MethodNotFoundErrorResponseJSON json.RawMessage `json:"methodNotFoundErrorResponseJSON"`
}

func loadMCPProtocolMessageTemplates(
	mcpProtocolMessagesFilePath string,
) (MCPProtocolMessageTemplates, error) {
	mcpProtocolMessagesFileJSON, readProtocolMessagesError :=
		os.ReadFile(mcpProtocolMessagesFilePath)
	if readProtocolMessagesError != nil {
		return MCPProtocolMessageTemplates{}, fmt.Errorf(
			"读取 MCP 协议消息文件失败: %w",
			readProtocolMessagesError,
		)
	}

	var mcpProtocolMessageTemplates MCPProtocolMessageTemplates
	if decodeProtocolMessagesError := json.Unmarshal(
		mcpProtocolMessagesFileJSON,
		&mcpProtocolMessageTemplates,
	); decodeProtocolMessagesError != nil {
		return MCPProtocolMessageTemplates{}, fmt.Errorf(
			"解包 MCP 协议消息文件失败: %w",
			decodeProtocolMessagesError,
		)
	}

	if mcpProtocolMessageTemplates.ProtocolVersion == "" {
		return MCPProtocolMessageTemplates{}, fmt.Errorf("MCP 协议消息文件缺少 protocolVersion")
	}

	requiredProtocolMessages := map[string]json.RawMessage{
		"initializeRequestJSON":           mcpProtocolMessageTemplates.InitializeRequestJSON,
		"initializedNotificationJSON":     mcpProtocolMessageTemplates.InitializedNotificationJSON,
		"getToolListRequestJSON":          mcpProtocolMessageTemplates.GetToolListRequestJSON,
		"callToolRequestJSON":             mcpProtocolMessageTemplates.CallToolRequestJSON,
		"cancelRequestNotificationJSON":   mcpProtocolMessageTemplates.CancelRequestNotificationJSON,
		"methodNotFoundErrorResponseJSON": mcpProtocolMessageTemplates.MethodNotFoundErrorResponseJSON,
	}
	for protocolMessageName, protocolMessageJSON := range requiredProtocolMessages {
		if _, copyProtocolMessageError := copyProtocolMessageJSON(protocolMessageJSON); copyProtocolMessageError != nil {
			return MCPProtocolMessageTemplates{}, fmt.Errorf(
				"%s 无效: %w",
				protocolMessageName,
				copyProtocolMessageError,
			)
		}
	}

	return mcpProtocolMessageTemplates, nil
}

// copyProtocolMessageJSON 每次把模板解包成一个新的 map，后续填值不会修改原模板。
func copyProtocolMessageJSON(
	protocolMessageJSON json.RawMessage,
) (map[string]any, error) {
	if len(protocolMessageJSON) == 0 {
		return nil, fmt.Errorf("消息内容为空")
	}

	var copiedProtocolMessage map[string]any
	if decodeProtocolMessageError := json.Unmarshal(
		protocolMessageJSON,
		&copiedProtocolMessage,
	); decodeProtocolMessageError != nil {
		return nil, fmt.Errorf("解包协议消息失败: %w", decodeProtocolMessageError)
	}
	if copiedProtocolMessage == nil {
		return nil, fmt.Errorf("协议消息必须是 JSON object")
	}
	return copiedProtocolMessage, nil
}
