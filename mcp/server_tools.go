package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"cc-agent-go/tool"
)

func (mcpServerManager *MCPServerManager) registerMCPServerTools(
	selectedMCPServerName string,
	returnedMCPServerTools []MCPServerToolDefinition,
	agentToolRegistry *tool.Registry,
) ([]string, error) {
	registeredAgentToolNames := make([]string, 0, len(returnedMCPServerTools))
	for _, returnedMCPServerToolDefinition := range returnedMCPServerTools {
		registeredMCPServerName := selectedMCPServerName
		registeredMCPServerToolName := returnedMCPServerToolDefinition.Name
		registeredAgentToolName := buildRegisteredAgentToolName(
			registeredMCPServerName,
			registeredMCPServerToolName,
		)
		if len(registeredAgentToolName) > 128 {
			mcpServerManager.unregisterAgentTools(agentToolRegistry, registeredAgentToolNames)
			return nil, fmt.Errorf(
				"注册 MCP 工具失败: Agent 工具名 %q 超过 128 个字符",
				registeredAgentToolName,
			)
		}

		registeredToolDescription := returnedMCPServerToolDefinition.Description
		if registeredToolDescription == "" {
			registeredToolDescription = returnedMCPServerToolDefinition.Title
		}
		if registeredToolDescription == "" {
			registeredToolDescription = registeredMCPServerToolName
		}

		executeRegisteredMCPServerTool := func(
			toolArguments map[string]any,
			_ tool.ToolExecutionEnvironment,
		) (string, error) {
			return mcpServerManager.callMCPServerTool(
				registeredMCPServerName,
				registeredMCPServerToolName,
				toolArguments,
			)
		}

		registerMCPServerToolError := agentToolRegistry.RegisterFunctionTool(
			registeredAgentToolName,
			registeredToolDescription,
			returnedMCPServerToolDefinition.InputSchema,
			executeRegisteredMCPServerTool,
		)
		if registerMCPServerToolError != nil {
			mcpServerManager.unregisterAgentTools(agentToolRegistry, registeredAgentToolNames)
			return nil, fmt.Errorf(
				"注册 MCP 工具 %q 失败: %w",
				registeredAgentToolName,
				registerMCPServerToolError,
			)
		}
		registeredAgentToolNames = append(registeredAgentToolNames, registeredAgentToolName)
	}
	return registeredAgentToolNames, nil
}

func buildRegisteredAgentToolName(mcpServerName string, mcpServerToolName string) string {
	return "mcp_" + mcpServerName + "__" + mcpServerToolName
}

func (mcpServerManager *MCPServerManager) unregisterAgentTools(
	agentToolRegistry *tool.Registry,
	registeredAgentToolNames []string,
) {
	for _, registeredAgentToolName := range registeredAgentToolNames {
		agentToolRegistry.Unregister(registeredAgentToolName)
	}
}

func (mcpServerManager *MCPServerManager) callMCPServerTool(
	registeredMCPServerName string,
	registeredMCPServerToolName string,
	toolArguments map[string]any,
) (string, error) {
	mcpServerManager.startedMCPServerProcessesMutex.RLock()
	startedProcess :=
		mcpServerManager.startedMCPServerProcessesByName[registeredMCPServerName]
	mcpServerManager.startedMCPServerProcessesMutex.RUnlock()
	if startedProcess == nil {
		return "", NewError(
			ErrorProcessStopped,
			"callMCPServerTool",
			registeredMCPServerName,
			fmt.Errorf("MCP Server 没有运行"),
		)
	}
	processStatus, processExitError := startedProcess.status()
	if processStatus != "running" {
		return "", NewError(
			ErrorProcessStopped,
			"callMCPServerTool",
			registeredMCPServerName,
			fmt.Errorf("MCP Server 状态为 %s: %v", processStatus, processExitError),
		)
	}

	callToolRequestJSON, copyCallToolRequestError := copyProtocolMessageJSON(
		mcpServerManager.mcpProtocolMessageTemplates.CallToolRequestJSON,
	)
	if copyCallToolRequestError != nil {
		return "", NewError(
			ErrorProtocolResponseInvalid,
			"callMCPServerTool.copyCallToolRequestJSON",
			registeredMCPServerName,
			copyCallToolRequestError,
		)
	}
	requestParameters, parametersError := getMCPRequestParameters(callToolRequestJSON)
	if parametersError != nil {
		return "", NewError(
			ErrorProtocolResponseInvalid,
			"callMCPServerTool.getRequestParameters",
			registeredMCPServerName,
			parametersError,
		)
	}
	requestParameters["name"] = registeredMCPServerToolName
	requestParameters["arguments"] = toolArguments

	toolCallRequestContext, cancelToolCallRequest :=
		mcpServerManager.newMCPRequestContext(context.Background(), startedProcess)
	defer cancelToolCallRequest()
	requestID, toolCallResponse, toolCallRequestError :=
		startedProcess.sendMCPRequestAndWaitForResponse(
			toolCallRequestContext,
			callToolRequestJSON,
		)
	if toolCallRequestError != nil {
		mcpServerManager.sendCancellationNotification(startedProcess, requestID, toolCallRequestError)
		return "", mcpServerManager.classifyMCPRequestError(
			"callMCPServerTool.toolsCall",
			registeredMCPServerName,
			toolCallRequestError,
			ErrorProcessStopped,
		)
	}
	if toolCallResponse.Error != nil {
		return "", fmt.Errorf(
			"MCP tools/call 返回 JSON-RPC error %d: %s",
			toolCallResponse.Error.Code,
			toolCallResponse.Error.Message,
		)
	}

	var toolCallResult MCPToolCallResult
	if decodeToolCallResultError := json.Unmarshal(
		toolCallResponse.Result,
		&toolCallResult,
	); decodeToolCallResultError != nil {
		return "", NewError(
			ErrorProtocolResponseInvalid,
			"callMCPServerTool.decodeToolCallResult",
			registeredMCPServerName,
			decodeToolCallResultError,
		)
	}

	agentToolResult, convertToolResultError := convertMCPToolResultToAgentText(toolCallResult)
	if convertToolResultError != nil {
		return "", convertToolResultError
	}
	if toolCallResult.IsError {
		return "", fmt.Errorf("MCP 工具执行错误: %s", agentToolResult)
	}
	return agentToolResult, nil
}

func convertMCPToolResultToAgentText(toolCallResult MCPToolCallResult) (string, error) {
	textResults := make([]string, 0, len(toolCallResult.Content))
	nonTextResults := make([]map[string]any, 0)
	for _, contentBlock := range toolCallResult.Content {
		contentType, _ := contentBlock["type"].(string)
		contentText, _ := contentBlock["text"].(string)
		if contentType == "text" && contentText != "" {
			textResults = append(textResults, contentText)
			continue
		}
		nonTextResults = append(nonTextResults, contentBlock)
	}
	if len(textResults) > 0 {
		return strings.Join(textResults, "\n"), nil
	}
	if toolCallResult.StructuredContent != nil {
		structuredContentJSON, encodeStructuredContentError :=
			json.Marshal(toolCallResult.StructuredContent)
		if encodeStructuredContentError != nil {
			return "", fmt.Errorf("整理 MCP structuredContent 失败: %w", encodeStructuredContentError)
		}
		return string(structuredContentJSON), nil
	}
	if len(nonTextResults) > 0 {
		nonTextContentJSON, encodeNonTextContentError := json.Marshal(nonTextResults)
		if encodeNonTextContentError != nil {
			return "", fmt.Errorf("整理 MCP 非文字结果失败: %w", encodeNonTextContentError)
		}
		return string(nonTextContentJSON), nil
	}
	return "MCP 工具执行完成，没有返回内容。", nil
}
