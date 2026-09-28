package httpapi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"cc-agent-go/mcp"
	"cc-agent-go/service"
)

type FrontendMCPServerSelectionJSON struct {
	SelectedMCPServerNames []string `json:"selectedServerNames"`
}

type MCPServerListJSON struct {
	Servers []mcp.MCPServerStatus `json:"servers"`
}

func (server *Server) handleListMCPServers(responseWriter http.ResponseWriter, httpRequest *http.Request) {
	if server.mcpServerManager == nil {
		server.writeAPIError(
			responseWriter,
			"handleListMCPServers",
			"",
			mcp.NewError(
				mcp.ErrorConfigurationInvalid,
				"handleListMCPServers",
				"",
				fmt.Errorf("MCPServerManager 尚未初始化"),
			),
		)
		return
	}

	responseWriter.Header().Set("Content-Type", "application/json;charset=UTF-8")
	if encodeServerListError := json.NewEncoder(responseWriter).Encode(MCPServerListJSON{
		Servers: server.mcpServerManager.ListConfiguredMCPServers(),
	}); encodeServerListError != nil {
		slog.Error("MCP Server 列表 JSON 写入失败",
			"component", "http",
			"operation", "handleListMCPServers",
			"error_kind", service.ErrorInternal,
			"error", encodeServerListError)
	}
}

func (server *Server) handleSelectMCPServers(responseWriter http.ResponseWriter, httpRequest *http.Request) {
	var frontendMCPServerSelectionJSON FrontendMCPServerSelectionJSON
	if decodeSelectionError := json.NewDecoder(httpRequest.Body).Decode(
		&frontendMCPServerSelectionJSON,
	); decodeSelectionError != nil {
		server.writeAPIError(
			responseWriter,
			"handleSelectMCPServers.decodeSelection",
			"",
			server.invalidRequestError(
				"handleSelectMCPServers.decodeSelection",
				decodeSelectionError,
			),
		)
		return
	}
	if server.mcpServerManager == nil {
		server.writeAPIError(
			responseWriter,
			"handleSelectMCPServers",
			"",
			mcp.NewError(
				mcp.ErrorConfigurationInvalid,
				"handleSelectMCPServers",
				"",
				fmt.Errorf("MCPServerManager 尚未初始化"),
			),
		)
		return
	}

	mcpServerStatuses, applySelectionError :=
		server.mcpServerManager.StartSelectedMCPServers(
			httpRequest.Context(),
			frontendMCPServerSelectionJSON.SelectedMCPServerNames,
			server.registry,
		)
	if applySelectionError != nil {
		server.writeAPIError(
			responseWriter,
			"handleSelectMCPServers.startSelectedMCPServers",
			"",
			applySelectionError,
		)
		return
	}

	responseWriter.Header().Set("Content-Type", "application/json;charset=UTF-8")
	if encodeServerStatusesError := json.NewEncoder(responseWriter).Encode(MCPServerListJSON{
		Servers: mcpServerStatuses,
	}); encodeServerStatusesError != nil {
		slog.Error("MCP Server 选择结果 JSON 写入失败",
			"component", "http",
			"operation", "handleSelectMCPServers",
			"error_kind", service.ErrorInternal,
			"error", encodeServerStatusesError)
	}
}

// harnessMCPLiveStatusText 生成当前运行中的 MCP Server 及工具名称的
// 自然语言实况；没有运行中的 Server 时明确说明无 MCP 资源。
func (server *Server) harnessMCPLiveStatusText() string {
	noMCPResourceText := "- 当前没有运行中的 MCP Server；" +
		"被管理 Agent 只有 command、file、activate_skill、create_skill 四个基础工具。"
	if server.mcpServerManager == nil {
		return noMCPResourceText
	}
	runningServerLines := make([]string, 0)
	for _, mcpServerStatus := range server.mcpServerManager.ListConfiguredMCPServers() {
		if mcpServerStatus.Status != "running" {
			continue
		}
		runningServerLines = append(runningServerLines, fmt.Sprintf(
			"- %s：工具 %s",
			mcpServerStatus.Name,
			strings.Join(mcpServerStatus.RegisteredAgentToolNames, "、"),
		))
	}
	if len(runningServerLines) == 0 {
		return noMCPResourceText
	}
	return strings.Join(runningServerLines, "\n")
}
