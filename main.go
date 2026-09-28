package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"cc-agent-go/config"
	"cc-agent-go/harness"
	"cc-agent-go/httpapi"
	"cc-agent-go/mcp"
	"cc-agent-go/memory"
	"cc-agent-go/modeltoken"
	"cc-agent-go/service"
	"cc-agent-go/tool"
)

const applicationLogFilePath = "logs/server.jsonl"

func loadPersonalities(dir string) (map[string]string, error) {
	result := make(map[string]string)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("读取人格目录失败: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			slog.Warn("读取人格文件失败",
				"component", "startup",
				"operation", "loadPersonalities",
				"file_name", name,
				"error", err)
			continue
		}
		agentName := strings.TrimSuffix(name, ".md")
		result[agentName] = strings.TrimSpace(string(data))
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("personalities/ 目录下没有 .md 文件")
	}
	return result, nil
}

func configureApplicationLogger(
	logFilePath string,
	standardErrorWriter io.Writer,
) (*os.File, error) {
	logDirectoryPath := filepath.Dir(logFilePath)
	if createLogDirectoryError := os.MkdirAll(logDirectoryPath, 0o755); createLogDirectoryError != nil {
		return nil, fmt.Errorf("创建日志目录失败: %w", createLogDirectoryError)
	}

	applicationLogFile, openLogFileError := os.OpenFile(
		logFilePath,
		os.O_CREATE|os.O_APPEND|os.O_WRONLY,
		0o644,
	)
	if openLogFileError != nil {
		return nil, fmt.Errorf("打开日志文件失败: %w", openLogFileError)
	}

	logOutputWriter := io.MultiWriter(standardErrorWriter, applicationLogFile)
	slog.SetDefault(slog.New(slog.NewJSONHandler(logOutputWriter, nil)))
	return applicationLogFile, nil
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	applicationLogFile, configureLoggerError :=
		configureApplicationLogger(applicationLogFilePath, os.Stderr)
	if configureLoggerError != nil {
		slog.Error("日志文件初始化失败，继续只向 stderr 输出",
			"component", "startup",
			"operation", "configureApplicationLogger",
			"error_kind", service.ErrorStorageWrite,
			"error", configureLoggerError)
	} else {
		defer applicationLogFile.Close()
	}

	applicationConfig := config.Load()
	modelTokenizerConfiguration, loadTokenizerConfigurationError :=
		config.LoadModelTokenizerConfiguration(applicationConfig.Model)
	if loadTokenizerConfigurationError != nil {
		slog.Error("模型 tokenizer 配置加载失败",
			"component", "startup",
			"operation", "config.LoadModelTokenizerConfiguration",
			"error_kind", service.ErrorConfig,
			"error", loadTokenizerConfigurationError)
		os.Exit(1)
	}
	loadedTokenCounter, createTokenCounterError :=
		modeltoken.NewHuggingFaceJSONTokenCounter(modelTokenizerConfiguration)
	if createTokenCounterError != nil {
		slog.Error("模型 tokenizer 启动失败",
			"component", "startup",
			"operation", "modeltoken.NewHuggingFaceJSONTokenCounter",
			"error_kind", service.ErrorConfig,
			"error", createTokenCounterError)
		os.Exit(1)
	}
	defer loadedTokenCounter.Close()
	modelContextWindowTokens :=
		modelTokenizerConfiguration.MaximumContextTokens

	var err error
	applicationPersonalities, err := loadPersonalities("personalities")
	if err != nil {
		slog.Error("加载人格失败",
			"component", "startup",
			"operation", "loadPersonalities",
			"error_kind", service.ErrorConfig,
			"error", err)
		os.Exit(1)
	}
	names := make([]string, 0, len(applicationPersonalities))
	for name := range applicationPersonalities {
		names = append(names, name)
	}
	sort.Strings(names)
	slog.Info("人格加载完成",
		"component", "startup",
		"operation", "loadPersonalities",
		"personality_count", len(names),
		"personality_names", strings.Join(names, ", "))

	applicationRegistry := tool.NewRegistry()
	if err := applicationRegistry.Register(tool.NewNativeCommandTool()); err != nil {
		slog.Error("工具注册失败", "component", "startup", "tool_name", "command", "error", err)
		os.Exit(1)
	}
	if err := applicationRegistry.Register(tool.NewSkillTool()); err != nil {
		slog.Error("工具注册失败", "component", "startup", "tool_name", "activate_skill", "error", err)
		os.Exit(1)
	}
	if err := applicationRegistry.Register(tool.NewCreateSkillTool()); err != nil {
		slog.Error("工具注册失败", "component", "startup", "tool_name", "create_skill", "error", err)
		os.Exit(1)
	}
	if err := applicationRegistry.Register(tool.NewFileTool()); err != nil {
		slog.Error("工具注册失败", "component", "startup", "tool_name", "create_skill", "error", err)
		os.Exit(1)
	}
	applicationMCPServerManager, err := mcp.NewMCPServerManager(
		"config/mcp_servers.json",
		"mcp/protocol/2025-11-25/messages.json",
	)
	if err != nil {
		slog.Error("MCP Server 配置加载失败",
			"component", "startup",
			"operation", "mcp.NewMCPServerManager",
			"error_kind", mcp.ErrorConfigurationInvalid,
			"error", err)
		os.Exit(1)
	}

	// Harness prompt 缺失时服务继续启动，但 Harness 路由返回配置错误。
	loadedHarnessPrompts, loadHarnessPromptsError := harness.LoadPrompts(
		"harness/system_prompt.md",
		"harness/managed_agent_prompt.md",
	)
	if loadHarnessPromptsError != nil {
		slog.Error("Harness prompt 加载失败，Harness 路由将返回配置错误",
			"component", "startup",
			"operation", "harness.LoadPrompts",
			"error_kind", service.ErrorConfig,
			"error", loadHarnessPromptsError)
	}

	applicationConversationEventReceivers := service.NewConversationEventReceivers()
	applicationConversationExecutionLocks := service.NewConversationExecutionLocks()
	applicationConversationRunRegistry := service.NewConversationRunRegistry()
	applicationConversationStore := memory.NewProjectConversationStore()
	apiServer := httpapi.NewServer(httpapi.Dependencies{
		LoadConfig:                 config.Load,
		Personalities:              applicationPersonalities,
		Registry:                   applicationRegistry,
		MCPServerManager:           applicationMCPServerManager,
		ConversationEventReceivers: applicationConversationEventReceivers,
		ConversationExecutionLocks: applicationConversationExecutionLocks,
		ConversationRunRegistry:    applicationConversationRunRegistry,
		ProjectConversationStore:   applicationConversationStore,
		TokenCounter:               loadedTokenCounter,
		ModelContextWindowTokens:   modelContextWindowTokens,
		HarnessPrompts:             loadedHarnessPrompts,
		HarnessPromptsLoadError:    loadHarnessPromptsError,
		ApplicationLogFilePath:     applicationLogFilePath,
	})

	slog.Info("cc-agent-go v15 启动",
		"component", "startup",
		"address", "http://localhost:8080",
		"maximum_parallel_subagents",
		applicationConfig.MaximumParallelSubAgents,
		"maximum_subagent_rounds",
		applicationConfig.MaximumSubAgentRounds)

	httpServer := &http.Server{Addr: ":8080", Handler: apiServer.Handler()}
	httpServerFinished := make(chan error, 1)
	go func() {
		httpServerFinished <- httpServer.ListenAndServe()
	}()

	shutdownSignal := make(chan os.Signal, 1)
	signal.Notify(shutdownSignal, os.Interrupt, syscall.SIGTERM)
	select {
	case receivedSignal := <-shutdownSignal:
		slog.Info("收到服务停止信号",
			"component", "startup",
			"operation", "main.waitForShutdown",
			"signal", receivedSignal.String())
	case listenError := <-httpServerFinished:
		if !errors.Is(listenError, http.ErrServerClosed) {
			slog.Error("HTTP 服务运行失败",
				"component", "http",
				"operation", "http.Server.ListenAndServe",
				"error_kind", service.ErrorInternal,
				"error", listenError)
		}
	}
	signal.Stop(shutdownSignal)

	if closeMCPServersError := applicationMCPServerManager.CloseAllStartedMCPServers(applicationRegistry); closeMCPServersError != nil {
		slog.Error("停止 MCP Server 失败",
			"component", "mcp_client",
			"operation", "CloseAllStartedMCPServers",
			"error_kind", mcp.ErrorProcessStopped,
			"error", closeMCPServersError)
	}

	shutdownContext, cancelHTTPShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelHTTPShutdown()
	if shutdownHTTPServerError := httpServer.Shutdown(shutdownContext); shutdownHTTPServerError != nil {
		slog.Error("HTTP 服务停止失败",
			"component", "http",
			"operation", "http.Server.Shutdown",
			"error_kind", service.ErrorInternal,
			"error", shutdownHTTPServerError)
	}
}
