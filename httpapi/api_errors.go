package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"cc-agent-go/mcp"
	"cc-agent-go/model"
	"cc-agent-go/service"
)

func (server *Server) publicError(err error) (int, model.ErrorResponse) {
	status := http.StatusInternalServerError
	resp := model.ErrorResponse{
		Code:    string(service.ErrorInternal),
		Message: "服务内部错误。",
	}

	var appErr *service.AppError
	if errors.As(err, &appErr) {
		resp.Code = string(appErr.Kind)
		resp.ProviderStatus = appErr.ProviderStatus
		switch appErr.Kind {
		case service.ErrorInvalidRequest:
			status = http.StatusBadRequest
			resp.Message = "请求参数无效。"
		case service.ErrorConfig:
			status = http.StatusServiceUnavailable
			resp.Message = "服务端未配置 DEEPSEEK_API_KEY。"
		case service.ErrorProviderAuth:
			status = http.StatusBadGateway
			resp.Message = "DeepSeek API 鉴权失败，请检查服务端 DEEPSEEK_API_KEY。"
		case service.ErrorProviderRateLimit:
			status = http.StatusServiceUnavailable
			resp.Message = "DeepSeek 请求过于频繁，请稍后重试。"
		case service.ErrorProvider, service.ErrorProviderResponseInvalid:
			status = http.StatusBadGateway
			resp.Message = "DeepSeek 服务返回异常。"
		case service.ErrorNetwork:
			status = http.StatusBadGateway
			resp.Message = "无法连接 DeepSeek 服务。"
		case service.ErrorNetworkTimeout:
			status = http.StatusGatewayTimeout
			resp.Message = "连接 DeepSeek 服务超时。"
		case service.ErrorStorageRead:
			status = http.StatusInternalServerError
			resp.Message = "读取会话数据失败。"
		case service.ErrorStorageWrite:
			status = http.StatusInternalServerError
			resp.Message = "写入会话数据失败。"
		case service.ErrorAgentLimit:
			status = http.StatusInternalServerError
			resp.Message = "Agent 达到最大工具调用轮数。"
		}
		return status, resp
	}

	var mcpErr *mcp.Error
	if !errors.As(err, &mcpErr) {
		return status, resp
	}
	resp.Code = string(mcpErr.Kind)
	switch mcpErr.Kind {
	case mcp.ErrorServerNotFound:
		status = http.StatusBadRequest
		resp.Message = "选择的 MCP Server 不存在。"
	case mcp.ErrorConfigurationInvalid:
		status = http.StatusInternalServerError
		resp.Message = "MCP 配置文件无效。"
	case mcp.ErrorRequestTimeout:
		status = http.StatusGatewayTimeout
		resp.Message = "MCP Server 请求超时。"
	case mcp.ErrorServerStartFailed:
		status = http.StatusBadGateway
		resp.Message = "MCP Server 进程启动失败。"
	case mcp.ErrorInitializeFailed:
		status = http.StatusBadGateway
		resp.Message = "MCP Server 初始化失败。"
	case mcp.ErrorToolsNotSupported:
		status = http.StatusBadGateway
		resp.Message = "MCP Server 没有提供工具。"
	case mcp.ErrorToolListFailed:
		status = http.StatusBadGateway
		resp.Message = "读取 MCP Server 工具列表失败。"
	case mcp.ErrorProcessStopped:
		status = http.StatusBadGateway
		resp.Message = "MCP Server 进程已经停止。"
	case mcp.ErrorProtocolResponseInvalid:
		status = http.StatusBadGateway
		resp.Message = "MCP Server 返回了无效数据。"
	}
	return status, resp
}

func (server *Server) logAPIError(operation string, conversationID string, err error, resp model.ErrorResponse) {
	args := []any{
		"component", "http",
		"operation", operation,
		"error_kind", resp.Code,
		"error", err,
	}
	if conversationID != "" {
		args = append(args, "conversation_id", conversationID)
	}
	if resp.ProviderStatus != 0 {
		args = append(args, "provider_status", resp.ProviderStatus)
	}
	slog.Error("请求处理失败", args...)
}

func (server *Server) writeAPIError(w http.ResponseWriter, operation string, conversationID string, err error) {
	status, resp := server.publicError(err)
	server.logAPIError(operation, conversationID, err, resp)
	w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	w.WriteHeader(status)
	if encodeErr := json.NewEncoder(w).Encode(resp); encodeErr != nil {
		slog.Error("错误响应 JSON 写入失败",
			"component", "http",
			"operation", operation,
			"error_kind", service.ErrorInternal,
			"error", encodeErr)
	}
}

func (server *Server) writeSSEError(w http.ResponseWriter, flusher http.Flusher, operation string,
	conversationID string, err error) {
	_, resp := server.publicError(err)
	server.logAPIError(operation, conversationID, err, resp)
	frame := map[string]any{
		"type":    "error",
		"code":    resp.Code,
		"message": resp.Message,
	}
	if resp.ProviderStatus != 0 {
		frame["providerStatus"] = resp.ProviderStatus
	}
	data, marshalErr := json.Marshal(frame)
	if marshalErr != nil {
		slog.Error("SSE 错误事件序列化失败",
			"component", "http",
			"operation", operation,
			"error_kind", service.ErrorInternal,
			"error", marshalErr)
		return
	}
	if _, writeErr := fmt.Fprintf(w, "data: %s\n\n", data); writeErr != nil {
		slog.Error("SSE 错误事件写入失败",
			"component", "http",
			"operation", operation,
			"error_kind", service.ErrorInternal,
			"error", writeErr)
		return
	}
	flusher.Flush()
}

func (server *Server) invalidRequestError(operation string, err error) *service.AppError {
	return service.NewAppError(service.ErrorInvalidRequest, operation, 0, err)
}
