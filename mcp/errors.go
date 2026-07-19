package mcp

import "fmt"

type ErrorKind string

const (
	ErrorConfigurationInvalid    ErrorKind = "mcp_config_invalid"
	ErrorServerNotFound          ErrorKind = "mcp_server_not_found"
	ErrorServerStartFailed       ErrorKind = "mcp_server_start_failed"
	ErrorInitializeFailed        ErrorKind = "mcp_initialize_failed"
	ErrorToolsNotSupported       ErrorKind = "mcp_tools_not_supported"
	ErrorToolListFailed          ErrorKind = "mcp_tool_list_failed"
	ErrorRequestTimeout          ErrorKind = "mcp_request_timeout"
	ErrorProcessStopped          ErrorKind = "mcp_process_stopped"
	ErrorProtocolResponseInvalid ErrorKind = "mcp_protocol_response_invalid"
)

type Error struct {
	Kind          ErrorKind
	Operation     string
	MCPServerName string
	Err           error
}

func NewError(
	errorKind ErrorKind,
	operation string,
	mcpServerName string,
	err error,
) *Error {
	return &Error{
		Kind:          errorKind,
		Operation:     operation,
		MCPServerName: mcpServerName,
		Err:           err,
	}
}

func (mcpError *Error) Error() string {
	if mcpError == nil {
		return ""
	}
	if mcpError.Err == nil {
		return fmt.Sprintf("%s: %s", mcpError.Operation, mcpError.Kind)
	}
	return fmt.Sprintf("%s: %v", mcpError.Operation, mcpError.Err)
}

func (mcpError *Error) Unwrap() error {
	if mcpError == nil {
		return nil
	}
	return mcpError.Err
}
