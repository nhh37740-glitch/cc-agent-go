package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

const defaultMCPRequestTimeoutSeconds = 60

var validMCPServerName = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// MCPServerConfigurationFile 对应 config/mcp_servers.json 的完整内容。
type MCPServerConfigurationFile struct {
	MCPServers []MCPServerConfiguration `json:"mcpServers"`
}

// MCPServerConfiguration 保存启动一台 MCP Server 所需的配置。
type MCPServerConfiguration struct {
	Name                  string            `json:"name"`
	DisplayName           string            `json:"displayName"`
	Transport             string            `json:"transport"`
	Command               string            `json:"command"`
	Arguments             []string          `json:"arguments"`
	WorkingDirectory      string            `json:"workingDirectory"`
	RequestTimeoutSeconds int               `json:"requestTimeoutSeconds"`
	EnvironmentVariables  map[string]string `json:"environmentVariables"`
}

func loadMCPServerConfigurations(
	mcpServerConfigurationFilePath string,
) (map[string]MCPServerConfiguration, error) {
	mcpServerConfigurationFileJSON, readConfigurationError :=
		os.ReadFile(mcpServerConfigurationFilePath)
	if readConfigurationError != nil {
		return nil, fmt.Errorf("读取 MCP Server 配置文件失败: %w", readConfigurationError)
	}

	var mcpServerConfigurationFile MCPServerConfigurationFile
	if decodeConfigurationError := json.Unmarshal(
		mcpServerConfigurationFileJSON,
		&mcpServerConfigurationFile,
	); decodeConfigurationError != nil {
		return nil, fmt.Errorf("解包 MCP Server 配置文件失败: %w", decodeConfigurationError)
	}

	mcpServerConfigurationsByName := make(
		map[string]MCPServerConfiguration,
		len(mcpServerConfigurationFile.MCPServers),
	)
	for configurationPosition, mcpServerConfiguration := range mcpServerConfigurationFile.MCPServers {
		if validationError := validateMCPServerConfiguration(
			configurationPosition,
			&mcpServerConfiguration,
		); validationError != nil {
			return nil, validationError
		}
		if _, nameAlreadyExists := mcpServerConfigurationsByName[mcpServerConfiguration.Name]; nameAlreadyExists {
			return nil, fmt.Errorf(
				"MCP Server 配置包含重复名称 %q",
				mcpServerConfiguration.Name,
			)
		}
		mcpServerConfigurationsByName[mcpServerConfiguration.Name] = mcpServerConfiguration
	}

	return mcpServerConfigurationsByName, nil
}

func validateMCPServerConfiguration(
	configurationPosition int,
	mcpServerConfiguration *MCPServerConfiguration,
) error {
	mcpServerConfiguration.Name = strings.TrimSpace(mcpServerConfiguration.Name)
	mcpServerConfiguration.DisplayName = strings.TrimSpace(mcpServerConfiguration.DisplayName)
	mcpServerConfiguration.Transport = strings.TrimSpace(mcpServerConfiguration.Transport)
	mcpServerConfiguration.Command = strings.TrimSpace(mcpServerConfiguration.Command)
	mcpServerConfiguration.WorkingDirectory = strings.TrimSpace(mcpServerConfiguration.WorkingDirectory)

	if mcpServerConfiguration.Name == "" {
		return fmt.Errorf("第 %d 条 MCP Server 配置缺少 name", configurationPosition+1)
	}
	if !validMCPServerName.MatchString(mcpServerConfiguration.Name) {
		return fmt.Errorf(
			"MCP Server 名称 %q 只能包含字母、数字、下划线、点和短横线",
			mcpServerConfiguration.Name,
		)
	}
	if mcpServerConfiguration.DisplayName == "" {
		mcpServerConfiguration.DisplayName = mcpServerConfiguration.Name
	}
	if mcpServerConfiguration.Transport != "stdio" {
		return fmt.Errorf(
			"MCP Server %q 的 transport 必须是 stdio",
			mcpServerConfiguration.Name,
		)
	}
	if mcpServerConfiguration.Command == "" {
		return fmt.Errorf("MCP Server %q 缺少 command", mcpServerConfiguration.Name)
	}
	if mcpServerConfiguration.WorkingDirectory == "" {
		mcpServerConfiguration.WorkingDirectory = "."
	}
	if mcpServerConfiguration.RequestTimeoutSeconds <= 0 {
		mcpServerConfiguration.RequestTimeoutSeconds = defaultMCPRequestTimeoutSeconds
	}
	return nil
}
