package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"cc-agent-go/tool"
)

type MCPServerStatus struct {
	Name                     string   `json:"name"`
	DisplayName              string   `json:"displayName"`
	Status                   string   `json:"status"`
	RegisteredToolCount      int      `json:"registeredToolCount"`
	RegisteredAgentToolNames []string `json:"registeredAgentToolNames"`
}

// MCPServerManager 保存 MCP Server 配置、协议消息、已启动进程和注册工具名称。
type MCPServerManager struct {
	mcpServerConfigurationsByName map[string]MCPServerConfiguration
	mcpProtocolMessageTemplates   MCPProtocolMessageTemplates

	startedMCPServerProcessesMutex          sync.RWMutex
	startedMCPServerProcessesByName         map[string]*startedMCPServerProcess
	registeredAgentToolNamesByMCPServerName map[string][]string

	processLifetimeContext context.Context
	cancelProcessLifetime  context.CancelFunc
}

func NewMCPServerManager(
	mcpServerConfigurationFilePath string,
	mcpProtocolMessagesFilePath string,
) (*MCPServerManager, error) {
	mcpServerConfigurationsByName, loadConfigurationsError :=
		loadMCPServerConfigurations(mcpServerConfigurationFilePath)
	if loadConfigurationsError != nil {
		return nil, NewError(
			ErrorConfigurationInvalid,
			"NewMCPServerManager.loadMCPServerConfigurations",
			"",
			loadConfigurationsError,
		)
	}

	mcpProtocolMessageTemplates, loadProtocolMessagesError :=
		loadMCPProtocolMessageTemplates(mcpProtocolMessagesFilePath)
	if loadProtocolMessagesError != nil {
		return nil, NewError(
			ErrorConfigurationInvalid,
			"NewMCPServerManager.loadMCPProtocolMessageTemplates",
			"",
			loadProtocolMessagesError,
		)
	}

	processLifetimeContext, cancelProcessLifetime := context.WithCancel(context.Background())
	return &MCPServerManager{
		mcpServerConfigurationsByName:           mcpServerConfigurationsByName,
		mcpProtocolMessageTemplates:             mcpProtocolMessageTemplates,
		startedMCPServerProcessesByName:         make(map[string]*startedMCPServerProcess),
		registeredAgentToolNamesByMCPServerName: make(map[string][]string),
		processLifetimeContext:                  processLifetimeContext,
		cancelProcessLifetime:                   cancelProcessLifetime,
	}, nil
}

func (mcpServerManager *MCPServerManager) ListConfiguredMCPServers() []MCPServerStatus {
	mcpServerNames := make([]string, 0, len(mcpServerManager.mcpServerConfigurationsByName))
	for mcpServerName := range mcpServerManager.mcpServerConfigurationsByName {
		mcpServerNames = append(mcpServerNames, mcpServerName)
	}
	sort.Strings(mcpServerNames)

	mcpServerManager.startedMCPServerProcessesMutex.RLock()
	defer mcpServerManager.startedMCPServerProcessesMutex.RUnlock()

	mcpServerStatuses := make([]MCPServerStatus, 0, len(mcpServerNames))
	for _, mcpServerName := range mcpServerNames {
		mcpServerConfiguration := mcpServerManager.mcpServerConfigurationsByName[mcpServerName]
		mcpServerStatus := MCPServerStatus{
			Name:                     mcpServerName,
			DisplayName:              mcpServerConfiguration.DisplayName,
			Status:                   "stopped",
			RegisteredAgentToolNames: make([]string, 0),
		}
		if startedProcess, processExists :=
			mcpServerManager.startedMCPServerProcessesByName[mcpServerName]; processExists {
			mcpServerStatus.Status, _ = startedProcess.status()
		}
		registeredAgentToolNames := append(
			make([]string, 0),
			mcpServerManager.registeredAgentToolNamesByMCPServerName[mcpServerName]...,
		)
		sort.Strings(registeredAgentToolNames)
		mcpServerStatus.RegisteredAgentToolNames = registeredAgentToolNames
		mcpServerStatus.RegisteredToolCount = len(registeredAgentToolNames)
		mcpServerStatuses = append(mcpServerStatuses, mcpServerStatus)
	}
	return mcpServerStatuses
}

func (mcpServerManager *MCPServerManager) StartSelectedMCPServers(
	requestContext context.Context,
	selectedMCPServerNames []string,
	agentToolRegistry *tool.Registry,
) ([]MCPServerStatus, error) {
	if agentToolRegistry == nil {
		return mcpServerManager.ListConfiguredMCPServers(), NewError(
			ErrorConfigurationInvalid,
			"StartSelectedMCPServers",
			"",
			fmt.Errorf("Agent 工具注册表不能为空"),
		)
	}

	selectedMCPServerNamesSet := make(map[string]struct{}, len(selectedMCPServerNames))
	for _, selectedMCPServerName := range selectedMCPServerNames {
		if _, configurationExists :=
			mcpServerManager.mcpServerConfigurationsByName[selectedMCPServerName]; !configurationExists {
			return mcpServerManager.ListConfiguredMCPServers(), NewError(
				ErrorServerNotFound,
				"StartSelectedMCPServers",
				selectedMCPServerName,
				fmt.Errorf("没有名称为 %q 的 MCP Server 配置", selectedMCPServerName),
			)
		}
		selectedMCPServerNamesSet[selectedMCPServerName] = struct{}{}
	}

	sortedSelectedMCPServerNames := make([]string, 0, len(selectedMCPServerNamesSet))
	for selectedMCPServerName := range selectedMCPServerNamesSet {
		sortedSelectedMCPServerNames = append(sortedSelectedMCPServerNames, selectedMCPServerName)
	}
	sort.Strings(sortedSelectedMCPServerNames)

	newlyStartedMCPServerNames := make([]string, 0, len(sortedSelectedMCPServerNames))
	for _, selectedMCPServerName := range sortedSelectedMCPServerNames {
		if mcpServerManager.isMCPServerProcessRunning(selectedMCPServerName) {
			continue
		}
		_ = mcpServerManager.stopMCPServer(selectedMCPServerName, agentToolRegistry)
		if startServerError := mcpServerManager.startMCPServerAndLoadTools(
			requestContext,
			selectedMCPServerName,
			agentToolRegistry,
		); startServerError != nil {
			for _, newlyStartedMCPServerName := range newlyStartedMCPServerNames {
				_ = mcpServerManager.stopMCPServer(newlyStartedMCPServerName, agentToolRegistry)
			}
			return mcpServerManager.ListConfiguredMCPServers(), startServerError
		}
		newlyStartedMCPServerNames = append(newlyStartedMCPServerNames, selectedMCPServerName)
	}

	for configuredMCPServerName := range mcpServerManager.mcpServerConfigurationsByName {
		if _, selected := selectedMCPServerNamesSet[configuredMCPServerName]; selected {
			continue
		}
		if stopServerError := mcpServerManager.stopMCPServer(
			configuredMCPServerName,
			agentToolRegistry,
		); stopServerError != nil {
			return mcpServerManager.ListConfiguredMCPServers(), stopServerError
		}
	}

	return mcpServerManager.ListConfiguredMCPServers(), nil
}

func (mcpServerManager *MCPServerManager) startMCPServerAndLoadTools(
	requestContext context.Context,
	selectedMCPServerName string,
	agentToolRegistry *tool.Registry,
) error {
	mcpServerConfiguration :=
		mcpServerManager.mcpServerConfigurationsByName[selectedMCPServerName]
	startedProcess, startProcessError := startMCPServerProcess(
		mcpServerManager.processLifetimeContext,
		mcpServerConfiguration,
		mcpServerManager.mcpProtocolMessageTemplates.MethodNotFoundErrorResponseJSON,
	)
	if startProcessError != nil {
		return NewError(
			ErrorServerStartFailed,
			"startMCPServerProcess",
			selectedMCPServerName,
			startProcessError,
		)
	}

	initializationResult, initializeServerError :=
		mcpServerManager.initializeMCPServer(requestContext, startedProcess)
	if initializeServerError != nil {
		_ = startedProcess.stop()
		return initializeServerError
	}
	if _, toolsSupported := initializationResult.Capabilities["tools"]; !toolsSupported {
		_ = startedProcess.stop()
		return NewError(
			ErrorToolsNotSupported,
			"initializeMCPServer.capabilities",
			selectedMCPServerName,
			fmt.Errorf("MCP Server 没有返回 tools 能力"),
		)
	}

	returnedMCPServerTools, getToolListError :=
		mcpServerManager.getMCPServerToolList(requestContext, startedProcess)
	if getToolListError != nil {
		_ = startedProcess.stop()
		return getToolListError
	}

	registeredAgentToolNames, registerToolsError :=
		mcpServerManager.registerMCPServerTools(
			selectedMCPServerName,
			returnedMCPServerTools,
			agentToolRegistry,
		)
	if registerToolsError != nil {
		_ = startedProcess.stop()
		return registerToolsError
	}

	mcpServerManager.startedMCPServerProcessesMutex.Lock()
	mcpServerManager.startedMCPServerProcessesByName[selectedMCPServerName] = startedProcess
	mcpServerManager.registeredAgentToolNamesByMCPServerName[selectedMCPServerName] =
		registeredAgentToolNames
	mcpServerManager.startedMCPServerProcessesMutex.Unlock()

	slog.Info("MCP Server 启动并注册工具完成",
		"component", "mcp_client",
		"operation", "startMCPServerAndLoadTools",
		"mcp_server_name", selectedMCPServerName,
		"registered_tool_count", len(registeredAgentToolNames))
	return nil
}

func (mcpServerManager *MCPServerManager) initializeMCPServer(
	requestContext context.Context,
	startedProcess *startedMCPServerProcess,
) (MCPInitializeResult, error) {
	initializeRequestJSON, copyInitializeRequestError := copyProtocolMessageJSON(
		mcpServerManager.mcpProtocolMessageTemplates.InitializeRequestJSON,
	)
	if copyInitializeRequestError != nil {
		return MCPInitializeResult{}, NewError(
			ErrorInitializeFailed,
			"initializeMCPServer.copyInitializeRequestJSON",
			startedProcess.mcpServerConfiguration.Name,
			copyInitializeRequestError,
		)
	}

	initializeRequestContext, cancelInitializeRequest :=
		mcpServerManager.newMCPRequestContext(requestContext, startedProcess)
	defer cancelInitializeRequest()
	requestID, initializeResponse, initializeRequestError :=
		startedProcess.sendMCPRequestAndWaitForResponse(
			initializeRequestContext,
			initializeRequestJSON,
		)
	if initializeRequestError != nil {
		mcpServerManager.sendCancellationNotification(startedProcess, requestID, initializeRequestError)
		return MCPInitializeResult{}, mcpServerManager.classifyMCPRequestError(
			"initializeMCPServer.initialize",
			startedProcess.mcpServerConfiguration.Name,
			initializeRequestError,
			ErrorInitializeFailed,
		)
	}
	if initializeResponse.Error != nil {
		return MCPInitializeResult{}, NewError(
			ErrorInitializeFailed,
			"initializeMCPServer.initialize",
			startedProcess.mcpServerConfiguration.Name,
			fmt.Errorf(
				"MCP Server 返回 JSON-RPC error %d: %s",
				initializeResponse.Error.Code,
				initializeResponse.Error.Message,
			),
		)
	}

	var initializationResult MCPInitializeResult
	if decodeInitializeResultError := json.Unmarshal(
		initializeResponse.Result,
		&initializationResult,
	); decodeInitializeResultError != nil {
		return MCPInitializeResult{}, NewError(
			ErrorProtocolResponseInvalid,
			"initializeMCPServer.decodeInitializeResult",
			startedProcess.mcpServerConfiguration.Name,
			decodeInitializeResultError,
		)
	}
	if initializationResult.ProtocolVersion !=
		mcpServerManager.mcpProtocolMessageTemplates.ProtocolVersion {
		return MCPInitializeResult{}, NewError(
			ErrorInitializeFailed,
			"initializeMCPServer.protocolVersion",
			startedProcess.mcpServerConfiguration.Name,
			fmt.Errorf(
				"MCP Server 返回协议版本 %q，当前客户端只支持 %q",
				initializationResult.ProtocolVersion,
				mcpServerManager.mcpProtocolMessageTemplates.ProtocolVersion,
			),
		)
	}

	initializedNotificationJSON, copyInitializedNotificationError :=
		copyProtocolMessageJSON(
			mcpServerManager.mcpProtocolMessageTemplates.InitializedNotificationJSON,
		)
	if copyInitializedNotificationError != nil {
		return MCPInitializeResult{}, NewError(
			ErrorInitializeFailed,
			"initializeMCPServer.copyInitializedNotificationJSON",
			startedProcess.mcpServerConfiguration.Name,
			copyInitializedNotificationError,
		)
	}
	if sendInitializedNotificationError := startedProcess.sendMCPNotification(
		initializedNotificationJSON,
	); sendInitializedNotificationError != nil {
		return MCPInitializeResult{}, NewError(
			ErrorInitializeFailed,
			"initializeMCPServer.sendInitializedNotification",
			startedProcess.mcpServerConfiguration.Name,
			sendInitializedNotificationError,
		)
	}

	return initializationResult, nil
}

func (mcpServerManager *MCPServerManager) getMCPServerToolList(
	requestContext context.Context,
	startedProcess *startedMCPServerProcess,
) ([]MCPServerToolDefinition, error) {
	allReturnedMCPServerTools := make([]MCPServerToolDefinition, 0)
	nextCursor := ""
	usedCursors := make(map[string]struct{})
	for toolListPage := 0; toolListPage < 100; toolListPage++ {
		getToolListRequestJSON, copyToolListRequestError := copyProtocolMessageJSON(
			mcpServerManager.mcpProtocolMessageTemplates.GetToolListRequestJSON,
		)
		if copyToolListRequestError != nil {
			return nil, NewError(
				ErrorToolListFailed,
				"getMCPServerToolList.copyGetToolListRequestJSON",
				startedProcess.mcpServerConfiguration.Name,
				copyToolListRequestError,
			)
		}
		if nextCursor != "" {
			requestParameters, parametersError := getMCPRequestParameters(getToolListRequestJSON)
			if parametersError != nil {
				return nil, NewError(
					ErrorToolListFailed,
					"getMCPServerToolList.getRequestParameters",
					startedProcess.mcpServerConfiguration.Name,
					parametersError,
				)
			}
			requestParameters["cursor"] = nextCursor
		}

		toolListRequestContext, cancelToolListRequest :=
			mcpServerManager.newMCPRequestContext(requestContext, startedProcess)
		requestID, toolListResponse, toolListRequestError :=
			startedProcess.sendMCPRequestAndWaitForResponse(
				toolListRequestContext,
				getToolListRequestJSON,
			)
		cancelToolListRequest()
		if toolListRequestError != nil {
			mcpServerManager.sendCancellationNotification(startedProcess, requestID, toolListRequestError)
			return nil, mcpServerManager.classifyMCPRequestError(
				"getMCPServerToolList.toolsList",
				startedProcess.mcpServerConfiguration.Name,
				toolListRequestError,
				ErrorToolListFailed,
			)
		}
		if toolListResponse.Error != nil {
			return nil, NewError(
				ErrorToolListFailed,
				"getMCPServerToolList.toolsList",
				startedProcess.mcpServerConfiguration.Name,
				fmt.Errorf(
					"MCP Server 返回 JSON-RPC error %d: %s",
					toolListResponse.Error.Code,
					toolListResponse.Error.Message,
				),
			)
		}

		var toolListResult MCPToolListResult
		if decodeToolListResultError := json.Unmarshal(
			toolListResponse.Result,
			&toolListResult,
		); decodeToolListResultError != nil {
			return nil, NewError(
				ErrorProtocolResponseInvalid,
				"getMCPServerToolList.decodeToolListResult",
				startedProcess.mcpServerConfiguration.Name,
				decodeToolListResultError,
			)
		}
		for _, returnedToolDefinition := range toolListResult.Tools {
			if returnedToolDefinition.Name == "" || returnedToolDefinition.InputSchema == nil {
				return nil, NewError(
					ErrorProtocolResponseInvalid,
					"getMCPServerToolList.validateToolDefinition",
					startedProcess.mcpServerConfiguration.Name,
					fmt.Errorf("tools/list 返回了缺少 name 或 inputSchema 的工具"),
				)
			}
		}
		allReturnedMCPServerTools = append(allReturnedMCPServerTools, toolListResult.Tools...)
		if toolListResult.NextCursor == "" {
			return allReturnedMCPServerTools, nil
		}
		if _, cursorAlreadyUsed := usedCursors[toolListResult.NextCursor]; cursorAlreadyUsed {
			return nil, NewError(
				ErrorProtocolResponseInvalid,
				"getMCPServerToolList.pagination",
				startedProcess.mcpServerConfiguration.Name,
				fmt.Errorf("tools/list 重复返回 cursor %q", toolListResult.NextCursor),
			)
		}
		usedCursors[toolListResult.NextCursor] = struct{}{}
		nextCursor = toolListResult.NextCursor
	}

	return nil, NewError(
		ErrorToolListFailed,
		"getMCPServerToolList.pagination",
		startedProcess.mcpServerConfiguration.Name,
		fmt.Errorf("tools/list 超过 100 页"),
	)
}

func (mcpServerManager *MCPServerManager) newMCPRequestContext(
	parentContext context.Context,
	startedProcess *startedMCPServerProcess,
) (context.Context, context.CancelFunc) {
	if parentContext == nil {
		parentContext = context.Background()
	}
	return context.WithTimeout(
		parentContext,
		time.Duration(startedProcess.mcpServerConfiguration.RequestTimeoutSeconds)*time.Second,
	)
}

func (mcpServerManager *MCPServerManager) sendCancellationNotification(
	startedProcess *startedMCPServerProcess,
	requestID uint64,
	requestError error,
) {
	if !errors.Is(requestError, context.DeadlineExceeded) &&
		!errors.Is(requestError, context.Canceled) {
		return
	}
	cancelRequestNotificationJSON, copyNotificationError := copyProtocolMessageJSON(
		mcpServerManager.mcpProtocolMessageTemplates.CancelRequestNotificationJSON,
	)
	if copyNotificationError != nil {
		return
	}
	requestParameters, parametersError := getMCPRequestParameters(cancelRequestNotificationJSON)
	if parametersError != nil {
		return
	}
	requestParameters["requestId"] = requestID
	requestParameters["reason"] = requestError.Error()
	_ = startedProcess.sendMCPNotification(cancelRequestNotificationJSON)
}

func (mcpServerManager *MCPServerManager) classifyMCPRequestError(
	operation string,
	mcpServerName string,
	requestError error,
	fallbackErrorKind ErrorKind,
) error {
	if errors.Is(requestError, context.DeadlineExceeded) {
		return NewError(ErrorRequestTimeout, operation, mcpServerName, requestError)
	}
	if errors.Is(requestError, context.Canceled) {
		return NewError(ErrorRequestTimeout, operation, mcpServerName, requestError)
	}
	return NewError(fallbackErrorKind, operation, mcpServerName, requestError)
}

func getMCPRequestParameters(mcpRequestJSON map[string]any) (map[string]any, error) {
	requestParameters, parametersAreObject := mcpRequestJSON["params"].(map[string]any)
	if !parametersAreObject {
		return nil, fmt.Errorf("MCP 请求的 params 必须是 JSON object")
	}
	return requestParameters, nil
}

func (mcpServerManager *MCPServerManager) isMCPServerProcessRunning(
	mcpServerName string,
) bool {
	mcpServerManager.startedMCPServerProcessesMutex.RLock()
	startedProcess := mcpServerManager.startedMCPServerProcessesByName[mcpServerName]
	mcpServerManager.startedMCPServerProcessesMutex.RUnlock()
	if startedProcess == nil {
		return false
	}
	processStatus, _ := startedProcess.status()
	return processStatus == "running"
}

func (mcpServerManager *MCPServerManager) stopMCPServer(
	mcpServerName string,
	agentToolRegistry *tool.Registry,
) error {
	mcpServerManager.startedMCPServerProcessesMutex.Lock()
	startedProcess := mcpServerManager.startedMCPServerProcessesByName[mcpServerName]
	registeredAgentToolNames :=
		mcpServerManager.registeredAgentToolNamesByMCPServerName[mcpServerName]
	delete(mcpServerManager.startedMCPServerProcessesByName, mcpServerName)
	delete(mcpServerManager.registeredAgentToolNamesByMCPServerName, mcpServerName)
	mcpServerManager.startedMCPServerProcessesMutex.Unlock()

	for _, registeredAgentToolName := range registeredAgentToolNames {
		agentToolRegistry.Unregister(registeredAgentToolName)
	}
	if startedProcess == nil {
		return nil
	}
	if stopProcessError := startedProcess.stop(); stopProcessError != nil {
		return NewError(
			ErrorProcessStopped,
			"stopMCPServer",
			mcpServerName,
			stopProcessError,
		)
	}
	return nil
}

func (mcpServerManager *MCPServerManager) CloseAllStartedMCPServers(
	agentToolRegistry *tool.Registry,
) error {
	mcpServerManager.startedMCPServerProcessesMutex.RLock()
	startedMCPServerNames := make(
		[]string,
		0,
		len(mcpServerManager.startedMCPServerProcessesByName),
	)
	for startedMCPServerName := range mcpServerManager.startedMCPServerProcessesByName {
		startedMCPServerNames = append(startedMCPServerNames, startedMCPServerName)
	}
	mcpServerManager.startedMCPServerProcessesMutex.RUnlock()

	var closeErrors []error
	for _, startedMCPServerName := range startedMCPServerNames {
		if closeServerError := mcpServerManager.stopMCPServer(
			startedMCPServerName,
			agentToolRegistry,
		); closeServerError != nil {
			closeErrors = append(closeErrors, closeServerError)
		}
	}
	mcpServerManager.cancelProcessLifetime()
	return errors.Join(closeErrors...)
}
