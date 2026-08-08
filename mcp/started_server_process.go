package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type mcpRequestResult struct {
	mcpServerResponse MCPJSONRPCMessage
	requestError      error
}

// startedMCPServerProcess 保存一台已经启动的 MCP Server 进程及其通信数据。
type startedMCPServerProcess struct {
	mcpServerConfiguration MCPServerConfiguration
	processCommand         *exec.Cmd
	standardInput          io.WriteCloser
	standardOutput         io.ReadCloser
	standardError          io.ReadCloser
	standardInputEncoder   *json.Encoder

	standardInputMutex sync.Mutex
	nextRequestID      atomic.Uint64

	pendingRequestsMutex            sync.Mutex
	pendingRequestsByRequestID      map[string]chan mcpRequestResult
	methodNotFoundErrorResponseJSON json.RawMessage

	processStatusMutex sync.RWMutex
	processStatus      string
	processExitError   error
	stopRequested      atomic.Bool
	processFinished    chan struct{}
}

func startMCPServerProcess(
	processLifetimeContext context.Context,
	mcpServerConfiguration MCPServerConfiguration,
	methodNotFoundErrorResponseJSON json.RawMessage,
) (*startedMCPServerProcess, error) {
	processCommand := exec.CommandContext(
		processLifetimeContext,
		mcpServerConfiguration.Command,
		mcpServerConfiguration.Arguments...,
	)
	processCommand.Dir = mcpServerConfiguration.WorkingDirectory
	processCommand.Env = os.Environ()
	for environmentVariableName, environmentVariableValue := range mcpServerConfiguration.EnvironmentVariables {
		processCommand.Env = append(
			processCommand.Env,
			environmentVariableName+"="+os.ExpandEnv(environmentVariableValue),
		)
	}

	standardInput, createStandardInputError := processCommand.StdinPipe()
	if createStandardInputError != nil {
		return nil, fmt.Errorf("创建标准输入管道失败: %w", createStandardInputError)
	}
	standardOutput, createStandardOutputError := processCommand.StdoutPipe()
	if createStandardOutputError != nil {
		_ = standardInput.Close()
		return nil, fmt.Errorf("创建标准输出管道失败: %w", createStandardOutputError)
	}
	standardError, createStandardErrorError := processCommand.StderrPipe()
	if createStandardErrorError != nil {
		_ = standardInput.Close()
		_ = standardOutput.Close()
		return nil, fmt.Errorf("创建标准错误管道失败: %w", createStandardErrorError)
	}

	if startProcessError := processCommand.Start(); startProcessError != nil {
		_ = standardInput.Close()
		_ = standardOutput.Close()
		_ = standardError.Close()
		return nil, fmt.Errorf("启动命令失败: %w", startProcessError)
	}

	startedProcess := &startedMCPServerProcess{
		mcpServerConfiguration:          mcpServerConfiguration,
		processCommand:                  processCommand,
		standardInput:                   standardInput,
		standardOutput:                  standardOutput,
		standardError:                   standardError,
		standardInputEncoder:            json.NewEncoder(standardInput),
		pendingRequestsByRequestID:      make(map[string]chan mcpRequestResult),
		methodNotFoundErrorResponseJSON: methodNotFoundErrorResponseJSON,
		processStatus:                   "running",
		processFinished:                 make(chan struct{}),
	}

	go startedProcess.readMCPServerStandardOutput()
	go startedProcess.drainMCPServerStandardError()
	go startedProcess.waitForMCPServerProcessExit()

	return startedProcess, nil
}

func (startedProcess *startedMCPServerProcess) readMCPServerStandardOutput() {
	standardOutputDecoder := json.NewDecoder(startedProcess.standardOutput)
	for {
		var mcpServerMessage MCPJSONRPCMessage
		if decodeMCPServerMessageError := standardOutputDecoder.Decode(&mcpServerMessage); decodeMCPServerMessageError != nil {
			if decodeMCPServerMessageError != io.EOF {
				slog.Error("读取 MCP Server 标准输出失败",
					"component", "mcp_client",
					"operation", "readMCPServerStandardOutput",
					"mcp_server_name", startedProcess.mcpServerConfiguration.Name,
					"error_kind", ErrorProtocolResponseInvalid,
					"error", decodeMCPServerMessageError)
			}
			startedProcess.failAllPendingRequests(
				fmt.Errorf("MCP Server 标准输出已经关闭: %w", decodeMCPServerMessageError),
			)
			return
		}

		if len(mcpServerMessage.ID) != 0 && mcpServerMessage.Method == "" {
			startedProcess.deliverMCPServerResponse(mcpServerMessage)
			continue
		}
		if len(mcpServerMessage.ID) != 0 && mcpServerMessage.Method != "" {
			startedProcess.sendMethodNotFoundResponse(mcpServerMessage.ID, mcpServerMessage.Method)
			continue
		}
		if mcpServerMessage.Method != "" {
			slog.Info("收到 MCP Server notification",
				"component", "mcp_client",
				"operation", "readMCPServerStandardOutput",
				"mcp_server_name", startedProcess.mcpServerConfiguration.Name,
				"mcp_method", mcpServerMessage.Method)
		}
	}
}

func (startedProcess *startedMCPServerProcess) drainMCPServerStandardError() {
	_, copyStandardError := io.Copy(io.Discard, startedProcess.standardError)
	if copyStandardError != nil {
		slog.Warn("读取 MCP Server 标准错误失败",
			"component", "mcp_client",
			"operation", "drainMCPServerStandardError",
			"mcp_server_name", startedProcess.mcpServerConfiguration.Name,
			"error", copyStandardError)
	}
}

func (startedProcess *startedMCPServerProcess) waitForMCPServerProcessExit() {
	waitProcessError := startedProcess.processCommand.Wait()
	startedProcess.processStatusMutex.Lock()
	startedProcess.processExitError = waitProcessError
	if startedProcess.stopRequested.Load() {
		startedProcess.processStatus = "stopped"
	} else {
		startedProcess.processStatus = "failed"
	}
	startedProcess.processStatusMutex.Unlock()
	startedProcess.failAllPendingRequests(fmt.Errorf("MCP Server 进程已经退出: %w", waitProcessError))
	close(startedProcess.processFinished)
}

func (startedProcess *startedMCPServerProcess) deliverMCPServerResponse(
	mcpServerResponse MCPJSONRPCMessage,
) {
	requestID := string(mcpServerResponse.ID)
	startedProcess.pendingRequestsMutex.Lock()
	waitingRequest, requestExists := startedProcess.pendingRequestsByRequestID[requestID]
	if requestExists {
		delete(startedProcess.pendingRequestsByRequestID, requestID)
	}
	startedProcess.pendingRequestsMutex.Unlock()
	if requestExists {
		waitingRequest <- mcpRequestResult{mcpServerResponse: mcpServerResponse}
	}
}

func (startedProcess *startedMCPServerProcess) failAllPendingRequests(requestError error) {
	startedProcess.pendingRequestsMutex.Lock()
	pendingRequests := startedProcess.pendingRequestsByRequestID
	startedProcess.pendingRequestsByRequestID = make(map[string]chan mcpRequestResult)
	startedProcess.pendingRequestsMutex.Unlock()

	for _, waitingRequest := range pendingRequests {
		waitingRequest <- mcpRequestResult{requestError: requestError}
	}
}

func (startedProcess *startedMCPServerProcess) sendMCPRequestAndWaitForResponse(
	requestContext context.Context,
	mcpRequestJSON map[string]any,
) (uint64, MCPJSONRPCMessage, error) {
	requestID := startedProcess.nextRequestID.Add(1)
	mcpRequestJSON["id"] = requestID
	waitingRequest := make(chan mcpRequestResult, 1)
	requestIDKey := strconv.FormatUint(requestID, 10)

	startedProcess.pendingRequestsMutex.Lock()
	startedProcess.pendingRequestsByRequestID[requestIDKey] = waitingRequest
	startedProcess.pendingRequestsMutex.Unlock()

	if writeRequestError := startedProcess.writeMCPJSON(mcpRequestJSON); writeRequestError != nil {
		startedProcess.removePendingRequest(requestIDKey)
		return requestID, MCPJSONRPCMessage{}, writeRequestError
	}

	select {
	case requestResult := <-waitingRequest:
		return requestID, requestResult.mcpServerResponse, requestResult.requestError
	case <-requestContext.Done():
		startedProcess.removePendingRequest(requestIDKey)
		return requestID, MCPJSONRPCMessage{}, requestContext.Err()
	case <-startedProcess.processFinished:
		startedProcess.removePendingRequest(requestIDKey)
		return requestID, MCPJSONRPCMessage{}, fmt.Errorf("MCP Server 进程已经退出")
	}
}

func (startedProcess *startedMCPServerProcess) sendMCPNotification(
	mcpNotificationJSON map[string]any,
) error {
	delete(mcpNotificationJSON, "id")
	return startedProcess.writeMCPJSON(mcpNotificationJSON)
}

func (startedProcess *startedMCPServerProcess) writeMCPJSON(mcpJSON map[string]any) error {
	startedProcess.standardInputMutex.Lock()
	defer startedProcess.standardInputMutex.Unlock()
	if encodeMCPJSONError := startedProcess.standardInputEncoder.Encode(mcpJSON); encodeMCPJSONError != nil {
		return fmt.Errorf("写入 MCP Server 标准输入失败: %w", encodeMCPJSONError)
	}
	return nil
}

func (startedProcess *startedMCPServerProcess) removePendingRequest(requestID string) {
	startedProcess.pendingRequestsMutex.Lock()
	delete(startedProcess.pendingRequestsByRequestID, requestID)
	startedProcess.pendingRequestsMutex.Unlock()
}

func (startedProcess *startedMCPServerProcess) sendMethodNotFoundResponse(
	serverRequestID json.RawMessage,
	serverMethod string,
) {
	methodNotFoundResponse, copyResponseError := copyProtocolMessageJSON(
		startedProcess.methodNotFoundErrorResponseJSON,
	)
	if copyResponseError != nil {
		return
	}
	var copiedServerRequestID any
	if decodeRequestIDError := json.Unmarshal(serverRequestID, &copiedServerRequestID); decodeRequestIDError != nil {
		return
	}
	methodNotFoundResponse["id"] = copiedServerRequestID
	if writeResponseError := startedProcess.writeMCPJSON(methodNotFoundResponse); writeResponseError != nil {
		slog.Warn("回复不支持的 MCP Server 请求失败",
			"component", "mcp_client",
			"operation", "sendMethodNotFoundResponse",
			"mcp_server_name", startedProcess.mcpServerConfiguration.Name,
			"mcp_method", serverMethod,
			"error", writeResponseError)
	}
}

func (startedProcess *startedMCPServerProcess) status() (string, error) {
	startedProcess.processStatusMutex.RLock()
	defer startedProcess.processStatusMutex.RUnlock()
	return startedProcess.processStatus, startedProcess.processExitError
}

func (startedProcess *startedMCPServerProcess) stop() error {
	startedProcess.stopRequested.Store(true)
	startedProcess.processStatusMutex.Lock()
	if startedProcess.processStatus == "running" {
		startedProcess.processStatus = "stopping"
	}
	startedProcess.processStatusMutex.Unlock()

	_ = startedProcess.standardInput.Close()
	select {
	case <-startedProcess.processFinished:
		return nil
	case <-time.After(3 * time.Second):
	}

	if startedProcess.processCommand.Process != nil {
		if killProcessError := startedProcess.processCommand.Process.Kill(); killProcessError != nil {
			return fmt.Errorf("结束 MCP Server 进程失败: %w", killProcessError)
		}
	}
	select {
	case <-startedProcess.processFinished:
		return nil
	case <-time.After(3 * time.Second):
		return fmt.Errorf("等待 MCP Server 进程退出超时")
	}
}
