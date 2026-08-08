package harness

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"cc-agent-go/agent"
	"cc-agent-go/config"
	"cc-agent-go/memory"
	"cc-agent-go/service"
	"cc-agent-go/tool"
)

// RuntimeDependencies 是创建 Runtime 时注入的全部外部依赖。
type RuntimeDependencies struct {
	ApplicationConfig          config.Config
	ConversationStore          *memory.ProjectConversationStore
	TokenCounter               agent.AgentTokenCounter
	ModelContextWindowTokens   int
	ConversationEventReceivers *service.ConversationEventReceivers
	ConversationExecutionLocks *service.ConversationExecutionLocks
	HarnessSystemPrompt        string
	ManagedAgentSystemPrompt   string
	// ManagedAgentToolRegistries 在每次启动被管理 Agent 时现取工具表，
	// 使当前全局 MCP 工具自动可见（不按 Agent 过滤）。
	ManagedAgentToolRegistries func() *tool.Registry
	// MCPLiveStatusText 由外部（main.go）提供当前运行中的 MCP Server
	// 及工具名称；nil 时表示没有 MCP 资源。
	MCPLiveStatusText func() string
}

// Runtime 是一个项目目录的 Harness 运行时：Agent 注册表、完成队列和
// Harness 自己的工具表。每个项目目录一个，由 GetOrCreateRuntime 懒创建。
type Runtime struct {
	workingDirectory string
	agentRegistry    *AgentRegistry
	completionQueue  chan ManagedAgentRecord
	dependencies     RuntimeDependencies
	harnessTools     *tool.Registry
}

var runtimesMutex sync.Mutex
var runtimesByWorkingDirectory = make(map[string]*Runtime)

// GetOrCreateRuntime 返回一个项目目录的 Runtime；首次调用时创建：
// 加载注册表 → 构建 Harness 工具表 → 启动完成队列消费 goroutine →
// 补扫重启前未收取的结果。
func GetOrCreateRuntime(
	workingDirectory string,
	dependencies RuntimeDependencies,
) (*Runtime, error) {
	runtimesMutex.Lock()
	defer runtimesMutex.Unlock()

	existingRuntime, alreadyCreated :=
		runtimesByWorkingDirectory[workingDirectory]
	if alreadyCreated {
		return existingRuntime, nil
	}

	agentRegistry, loadRegistryError := LoadAgentRegistry(
		workingDirectory,
		dependencies.ApplicationConfig.MaximumHarnessAgents,
	)
	if loadRegistryError != nil {
		return nil, loadRegistryError
	}

	createdRuntime := &Runtime{
		workingDirectory: workingDirectory,
		agentRegistry:    agentRegistry,
		completionQueue: make(
			chan ManagedAgentRecord,
			agentRegistry.MaximumAgents()*2,
		),
		dependencies: dependencies,
	}
	harnessTools, buildToolsError := createdRuntime.buildHarnessToolRegistry()
	if buildToolsError != nil {
		return nil, buildToolsError
	}
	createdRuntime.harnessTools = harnessTools

	go createdRuntime.consumeFinishedAgents()

	// 补扫：channel 是内存队列，服务重启后靠注册表的 resultCollected
	// 标志恢复——重启前已完成/失败但未收取的结果直接入队重新汇报。
	uncollectedRecords, collectError := agentRegistry.CollectFinishedResults()
	if collectError != nil {
		slog.Error("Harness 补扫未收取结果失败",
			"component", "harness",
			"working_directory", workingDirectory,
			"error", collectError)
	}
	for _, uncollectedRecord := range uncollectedRecords {
		createdRuntime.completionQueue <- uncollectedRecord
	}

	runtimesByWorkingDirectory[workingDirectory] = createdRuntime
	return createdRuntime, nil
}

// AgentRegistry 返回本运行时的 Agent 注册表（供 HTTP 名单接口使用）。
func (runtime *Runtime) AgentRegistry() *AgentRegistry {
	return runtime.agentRegistry
}

// HarnessTools 返回 Harness 自己的工具表：恰好 agent 与 memory 两个工具。
func (runtime *Runtime) HarnessTools() *tool.Registry {
	return runtime.harnessTools
}

// WorkingDirectory 返回本运行时绑定的项目目录。
func (runtime *Runtime) WorkingDirectory() string {
	return runtime.workingDirectory
}

// consumeFinishedAgents 阻塞在完成队列上（队列为空时 goroutine 休眠，
// 零 CPU），逐条把完成/失败结果汇报给 Harness 自己。单消费者天然串行，
// 会话执行锁保证和用户对话互斥。
func (runtime *Runtime) consumeFinishedAgents() {
	for finishedRecord := range runtime.completionQueue {
		runtime.reportFinishedAgent(finishedRecord)
	}
}

// reportFinishedAgent 标记已收取，然后以 InternalContinuationTaskInput
// 调用 Harness 自己；自调用失败只写日志，结果仍留在注册表供人工查看。
func (runtime *Runtime) reportFinishedAgent(finishedRecord ManagedAgentRecord) {
	if markCollectedError := runtime.agentRegistry.MarkResultCollected(
		finishedRecord.Name,
	); markCollectedError != nil {
		slog.Error("Harness 标记结果已收取失败",
			"component", "harness",
			"agent", finishedRecord.Name,
			"error", markCollectedError)
	}

	unlockHarnessConversation :=
		runtime.dependencies.ConversationExecutionLocks.LockConversation(
			HarnessConversationID,
		)
	defer unlockHarnessConversation()

	_, runHarnessError := service.RunAgentTask(
		agent.InternalContinuationTaskInput{
			ContinuationInstruction: buildContinuationInstruction(finishedRecord),
		},
		agent.AgentExecutionEnvironment{
			WorkingDirectory: runtime.workingDirectory,
			ConversationID:   HarnessConversationID,
		},
		runtime.HarnessSystemPromptWithLiveStatus(),
		runtime.dependencies.ApplicationConfig,
		runtime.harnessTools,
		runtime.dependencies.ConversationStore,
		runtime.dependencies.TokenCounter,
		runtime.dependencies.ModelContextWindowTokens,
		service.AgentRunOptions{
			StreamText: true,
			ReceiveEvent: func(receivedAgentEvent agent.AgentEvent) {
				runtime.forwardAgentEvent(
					HarnessConversationID,
					receivedAgentEvent,
				)
			},
		},
	)
	if runHarnessError != nil {
		slog.Error("Harness 自调用汇报失败",
			"component", "harness",
			"agent", finishedRecord.Name,
			"error", runHarnessError)
	}
}

// buildContinuationInstruction 把一条完成/失败记录组装成交给 Harness 的
// 自然语言汇报。
func buildContinuationInstruction(finishedRecord ManagedAgentRecord) string {
	if finishedRecord.Status == ManagedAgentStatusFailed {
		return fmt.Sprintf(
			"被管理 Agent「%s」（会话 %s）执行任务失败：\n\n%s\n\n"+
				"决定如何处理：重试、修改任务后重新派出，或用 forget 释放它。",
			finishedRecord.Name,
			finishedRecord.ConversationID,
			finishedRecord.LastError,
		)
	}
	return fmt.Sprintf(
		"被管理 Agent「%s」（会话 %s）完成了你交给它的任务。以下是它的汇报：\n\n%s",
		finishedRecord.Name,
		finishedRecord.ConversationID,
		finishedRecord.Result,
	)
}

// runManagedAgent 在后台 goroutine 中执行一个被管理 Agent 的任务：
// 事件逐 token 扇出到该 Agent 自己的会话频道；结束后状态与格式化结果
// 写回注册表，并把记录推入完成队列。
func (runtime *Runtime) runManagedAgent(
	agentName string,
	requestText string,
) {
	agentRecord, recordFound := runtime.agentRegistry.GetAgent(agentName)
	if !recordFound {
		slog.Error("被管理 Agent 启动前记录消失",
			"component", "harness",
			"agent", agentName)
		return
	}

	runResult, runAgentError := service.RunAgentTask(
		agent.HostedAgentTaskInput{Task: requestText},
		agent.AgentExecutionEnvironment{
			WorkingDirectory: runtime.workingDirectory,
			ConversationID:   agentRecord.ConversationID,
		},
		runtime.dependencies.ManagedAgentSystemPrompt,
		runtime.dependencies.ApplicationConfig,
		runtime.dependencies.ManagedAgentToolRegistries(),
		runtime.dependencies.ConversationStore,
		runtime.dependencies.TokenCounter,
		runtime.dependencies.ModelContextWindowTokens,
		service.AgentRunOptions{
			StreamText: true,
			ReceiveEvent: func(receivedAgentEvent agent.AgentEvent) {
				runtime.forwardAgentEvent(
					agentRecord.ConversationID,
					receivedAgentEvent,
				)
			},
		},
	)
	if runAgentError != nil {
		if markFailedError := runtime.agentRegistry.MarkFailed(
			agentName,
			runAgentError,
		); markFailedError != nil {
			slog.Error("被管理 Agent 失败状态写回注册表失败",
				"component", "harness",
				"agent", agentName,
				"error", markFailedError)
		}
	} else {
		if markCompletedError := runtime.agentRegistry.MarkCompleted(
			agentName,
			runResult.FinalText(),
		); markCompletedError != nil {
			slog.Error("被管理 Agent 完成状态写回注册表失败",
				"component", "harness",
				"agent", agentName,
				"error", markCompletedError)
		}
	}

	finishedRecord, finishedRecordFound :=
		runtime.agentRegistry.GetAgent(agentName)
	if !finishedRecordFound {
		return
	}
	runtime.completionQueue <- finishedRecord
}

// forwardAgentEvent 把一个 Agent 事件编码为 JSON 并扇出到指定会话频道。
func (runtime *Runtime) forwardAgentEvent(
	conversationID string,
	receivedAgentEvent agent.AgentEvent,
) {
	eventJSONFields := AgentEventJSONFields(receivedAgentEvent)
	if eventJSONFields == nil {
		return
	}
	encodedAgentEvent, encodeError := json.Marshal(eventJSONFields)
	if encodeError != nil {
		return
	}
	runtime.dependencies.ConversationEventReceivers.SendEventJSON(
		conversationID,
		encodedAgentEvent,
	)
}

// HarnessSystemPromptWithLiveStatus 在 Harness system prompt 后追加三段
// 实况：Agent 资源（池容量上限、已创建、运行中、还可创建和名单明细）、
// 当前运行中的 MCP Server 及工具名称、当前已创建的应用。
// Harness 据此自行决定启用哪个名字、给谁派任务、何时 forget、
// 以及引导哪个 Agent 使用哪个 MCP 工具。
func (runtime *Runtime) HarnessSystemPromptWithLiveStatus() string {
	agentRecords := runtime.agentRegistry.ListAgents()
	rosterText := "- 当前没有已创建的 Agent\n"
	if len(agentRecords) > 0 {
		rosterText = ""
		for _, agentRecord := range agentRecords {
			rosterText += fmt.Sprintf(
				"- %s：状态 %s，已完成任务 %d 个，会话 %s\n",
				agentRecord.Name,
				agentRecord.Status,
				agentRecord.TaskCount,
				agentRecord.ConversationID,
			)
		}
	}
	mcpLiveStatusText := "- 当前没有运行中的 MCP Server；" +
		"被管理 Agent 只有 bash、activate_skill、create_skill 三个基础工具。"
	if runtime.dependencies.MCPLiveStatusText != nil {
		mcpLiveStatusText = runtime.dependencies.MCPLiveStatusText()
	}
	return fmt.Sprintf(
		"%s\n\n---\n当前 Agent 资源实况（每次调用时刷新）：\n"+
			"池容量上限 %d 个；已创建 %d 个，运行中 %d 个，还可创建 %d 个。\n"+
			"名单：\n%s\n"+
			"当前 MCP 资源实况（每次调用时刷新）：\n%s\n\n"+
			"当前应用实况（每次调用时刷新）：\n%s",
		runtime.dependencies.HarnessSystemPrompt,
		runtime.agentRegistry.MaximumAgents(),
		runtime.agentRegistry.AgentCount(),
		runtime.agentRegistry.RunningAgentCount(),
		runtime.agentRegistry.MaximumAgents()-runtime.agentRegistry.AgentCount(),
		rosterText,
		mcpLiveStatusText,
		runtime.appsLiveStatusText(),
	)
}

// appsLiveStatusText 扫描 <workingDirectory>/.cc-agent/apps/ 下含 app.json
// 的子目录，生成当前应用的自然语言实况。
func (runtime *Runtime) appsLiveStatusText() string {
	noAppsText := "- 当前没有已创建的应用"
	appsDirectoryPath, buildAppsPathError :=
		AppsDirectoryPath(runtime.workingDirectory)
	if buildAppsPathError != nil {
		return noAppsText
	}
	appEntries, readAppsDirectoryError := os.ReadDir(appsDirectoryPath)
	if readAppsDirectoryError != nil {
		return noAppsText
	}
	appLines := make([]string, 0, len(appEntries))
	for _, appEntry := range appEntries {
		if !appEntry.IsDir() {
			continue
		}
		appConfigurationPath := filepath.Join(
			appsDirectoryPath,
			appEntry.Name(),
			appConfigurationFileName,
		)
		if _, statAppConfigurationError := os.Stat(appConfigurationPath); statAppConfigurationError != nil {
			continue
		}
		appLines = append(appLines, fmt.Sprintf(
			"- %s（页面地址 /apps/%s/）",
			appEntry.Name(),
			appEntry.Name(),
		))
	}
	if len(appLines) == 0 {
		return noAppsText
	}
	return strings.Join(appLines, "\n")
}

// buildHarnessToolRegistry 构建 Harness 自己的工具表：
// 恰好 agent 与 memory 两个工具。
func (runtime *Runtime) buildHarnessToolRegistry() (*tool.Registry, error) {
	harnessToolRegistry := tool.NewRegistry()
	if registerAgentToolError := harnessToolRegistry.Register(
		newManagedAgentTool(runtime),
	); registerAgentToolError != nil {
		return nil, fmt.Errorf("注册 agent 工具失败: %w", registerAgentToolError)
	}
	if registerMemoryToolError := harnessToolRegistry.Register(
		newHarnessMemoryTool(runtime),
	); registerMemoryToolError != nil {
		return nil, fmt.Errorf("注册 memory 工具失败: %w", registerMemoryToolError)
	}
	return harnessToolRegistry, nil
}
