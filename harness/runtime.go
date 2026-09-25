package harness

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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

	if ensureWorkspaceError := EnsureHarnessWorkspace(workingDirectory); ensureWorkspaceError != nil {
		return nil, ensureWorkspaceError
	}

	agentRegistry, loadRegistryError := LoadAgentRegistry(
		workingDirectory,
		dependencies.ApplicationConfig.MaximumHarnessAgents,
	)
	if loadRegistryError != nil {
		return nil, loadRegistryError
	}
	if ensureResidentsError := agentRegistry.EnsurePermanentResidents(); ensureResidentsError != nil {
		return nil, ensureResidentsError
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

// DrainPendingReports 处理完成队列中积压的汇报。
// 调用方必须已经持有 harness 会话执行锁（用户对话场景），
// 这样 Agent 的失败/完成汇报能抢在用户消息前被主管理看到。
// 锁由调用方持有并负责解锁。
func (runtime *Runtime) DrainPendingReports() {
	for {
		select {
		case finishedRecord := <-runtime.completionQueue:
			runtime.reportFinishedAgentLocked(finishedRecord)
		default:
			return
		}
	}
}

// WorkingDirectory 返回本运行时绑定的项目目录。
func (runtime *Runtime) WorkingDirectory() string {
	return runtime.workingDirectory
}

// consumeFinishedAgents 阻塞在完成队列上（队列为空时 goroutine 休眠，
// 零 CPU），逐条把完成/失败结果汇报给 Harness 自己。
// 汇报需要 harness 会话执行锁；若用户对话正持有锁（TryLock 失败），
// 把记录放回队列稍后重试，不死等——避免主管理对话等 Agent、Agent 汇报等锁的闭环。
func (runtime *Runtime) consumeFinishedAgents() {
	for finishedRecord := range runtime.completionQueue {
		unlockHarnessConversation, locked :=
			runtime.dependencies.ConversationExecutionLocks.TryLockConversation(
				HarnessConversationID,
			)
		if !locked {
			// 用户对话正持有 harness 锁：放回队列，等对话结束后再试。
			runtime.completionQueue <- finishedRecord
			time.Sleep(heartbeatRetryInterval)
			continue
		}
		runtime.reportFinishedAgentLocked(finishedRecord)
		unlockHarnessConversation()
	}
}

// reportFinishedAgentLocked 标记已收取，然后以 InternalContinuationTaskInput
// 调用 Harness 自己；自调用失败只写日志，结果仍留在注册表供人工查看。
// 调用前必须已经持有 harness 会话执行锁，且由调用方负责解锁。
func (runtime *Runtime) reportFinishedAgentLocked(
	finishedRecord ManagedAgentRecord,
) {
	if markCollectedError := runtime.agentRegistry.MarkResultCollected(
		finishedRecord.Name,
	); markCollectedError != nil {
		slog.Error("Harness 标记结果已收取失败",
			"component", "harness",
			"agent", finishedRecord.Name,
			"error", markCollectedError)
	}

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
			StreamText:             true,
			KeepRecentMemoryTokens: managedAgentKeepRecentMemoryTokens,
			// 对齐 pi 的 reserveTokens 与压缩触发（与主管理自调用一致）。
			MaximumOutputTokens:     managedAgentReserveOutputTokens,
			MaximumToolResultTokens: 2000,
			MaximumStoredMemoryTokens: runtime.dependencies.ModelContextWindowTokens -
				managedAgentReserveOutputTokens,
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
	kindText := string(finishedRecord.Kind)
	if kindText == "" {
		kindText = string(ClassifyAgentName(finishedRecord.Name))
	}
	reportBody := strings.TrimSpace(finishedRecord.Result)
	if reportBody == "" {
		reportBody = strings.TrimSpace(finishedRecord.LastError)
	}
	if reportBody == "" {
		reportBody = "（无正文汇报；请按失败处理并决定是否重试）"
	}
	if finishedRecord.Status == ManagedAgentStatusFailed {
		forgetHint := "修改任务后重新派出该 Agent"
		if finishedRecord.Kind != AgentKindResident {
			forgetHint += "，或 forget 临时 Agent 释放名额"
		}
		return fmt.Sprintf(
			"被管理 Agent「%s」（类型 %s，会话 %s）执行失败，必须据此决策：\n\n%s\n\n"+
				"可选：重试、%s。不要忽略这条失败汇报。",
			finishedRecord.Name,
			kindText,
			finishedRecord.ConversationID,
			reportBody,
			forgetHint,
		)
	}
	return fmt.Sprintf(
		"被管理 Agent「%s」（类型 %s，会话 %s）完成任务。汇报如下：\n\n%s\n\n"+
			"若结论稳定，请写入 shared/memory.md 或 shared/docs/。",
		finishedRecord.Name,
		kindText,
		finishedRecord.ConversationID,
		reportBody,
	)
}

// agentExecutionTrail 收集被管理 Agent 本次执行的进度上下文，
// 供失败汇报携带，避免主管理只收到失败却看不到 Agent 干了什么。
type agentExecutionTrail struct {
	// textParts 是已产出的文本增量，限量保留尾部。
	textParts []string
	// toolEvents 是工具开始/成功/失败事件，按顺序记录。
	toolEvents []string
	// 剩余文本与事件总预算（rune 数），超限丢更早的。
	maximumTotalRunes int
	currentTotalRunes int
}

func newAgentExecutionTrail() *agentExecutionTrail {
	return &agentExecutionTrail{
		maximumTotalRunes: 4000,
	}
}

func (executionTrail *agentExecutionTrail) recordEvent(
	receivedAgentEvent agent.AgentEvent,
) {
	switch concreteAgentEvent := receivedAgentEvent.(type) {
	case agent.AgentRoundStartedEvent:
		executionTrail.recordText(
			fmt.Sprintf("\n[第 %d 轮开始]", concreteAgentEvent.Round),
		)
	case agent.AgentTextDeltaEvent:
		executionTrail.recordText(concreteAgentEvent.Text)
	case agent.AgentToolStartedEvent:
		executionTrail.recordText(
			fmt.Sprintf("\n[调用工具 %s]", concreteAgentEvent.ToolName),
		)
	case agent.AgentToolSucceededEvent:
		executionTrail.recordText(
			fmt.Sprintf("\n[工具 %s 成功]", concreteAgentEvent.ToolName),
		)
	case agent.AgentToolFailedEvent:
		executionTrail.recordText(
			fmt.Sprintf("\n[工具 %s 失败: %v]",
				concreteAgentEvent.ToolName,
				concreteAgentEvent.Cause),
		)
	case agent.AgentCurrentRunCompactedEvent:
		executionTrail.recordText(
			fmt.Sprintf("\n[上下文已压缩 %d→%d 条]",
				concreteAgentEvent.MessagesBefore,
				concreteAgentEvent.MessagesAfter),
		)
	}
}

func (executionTrail *agentExecutionTrail) recordText(textToRecord string) {
	if textToRecord == "" {
		return
	}
	textRunes := []rune(textToRecord)
	executionTrail.textParts = append(executionTrail.textParts, textToRecord)
	executionTrail.currentTotalRunes += len(textRunes)
	executionTrail.trimToBudget()
}

// trimToBudget 从头部丢弃最旧的文本，直到总量不超过预算。
func (executionTrail *agentExecutionTrail) trimToBudget() {
	for executionTrail.currentTotalRunes > executionTrail.maximumTotalRunes &&
		len(executionTrail.textParts) > 1 {
		droppedText := executionTrail.textParts[0]
		executionTrail.textParts = executionTrail.textParts[1:]
		executionTrail.currentTotalRunes -= len([]rune(droppedText))
	}
}

func (executionTrail *agentExecutionTrail) text() string {
	return strings.Join(executionTrail.textParts, "")
}

func (executionTrail *agentExecutionTrail) isEmpty() bool {
	return strings.TrimSpace(executionTrail.text()) == ""
}

// 被管理 Agent 的上下文限制对齐 pi（docs/compaction.md）的参数：
//
//	reserveTokens = 16384    为 LLM 响应预留的空间（单次输出上限）
//	keepRecentTokens = 20000 压缩时保留的最近原文（KeepRecentMemoryTokens 默认值）
//	压缩触发 = contextWindow - reserveTokens（会话记忆压缩阈值）
const managedAgentReserveOutputTokens = 16384
const managedAgentKeepRecentMemoryTokens = 20000

// heartbeatRetryInterval 是完成队列消费者拿不到 harness 锁时的重试间隔。
const heartbeatRetryInterval = 200 * time.Millisecond

// runManagedAgent 在后台 goroutine 中执行一个被管理 Agent 的任务：
// 事件逐 token 扇出到该 Agent 自己的会话频道；同时收集执行轨迹供失败汇报；
// 结束后状态与格式化结果写回注册表，并把记录推入完成队列。
// 无论 RunAgentTask 以何种方式结束（正常返回、错误、panic、挂死），
// 都会确保产生一条完成/失败记录入队，主管理必能收到回调。
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

	// defer 兜底：任何异常路径都保证写失败并入队。
	enqueued := false
	defer func() {
		if enqueued {
			return
		}
		if recoveredPanic := recover(); recoveredPanic != nil {
			slog.Error("被管理 Agent 执行 panic，已转为失败汇报",
				"component", "harness",
				"agent", agentName,
				"panic", recoveredPanic)
			if markError := runtime.agentRegistry.MarkFailedWithReport(
				agentName,
				fmt.Errorf("被管理 Agent 执行 panic: %v", recoveredPanic),
				"Status: 失败（panic）\nPanic: "+fmt.Sprintf("%v", recoveredPanic),
			); markError != nil {
				slog.Error("panic 后写失败状态失败",
					"component", "harness",
					"agent", agentName,
					"error", markError)
			}
		} else if !enqueued {
			// 正常返回但未入队（记录消失等）也兜底标记失败。
			if markError := runtime.agentRegistry.MarkFailedWithReport(
				agentName,
				fmt.Errorf("被管理 Agent 执行结束但未产生汇报记录"),
				"Status: 失败（未产生汇报记录）",
			); markError != nil {
				slog.Error("兜底写失败状态失败",
					"component", "harness",
					"agent", agentName,
					"error", markError)
			}
		}
		// 无论 panic 与否，最后都尝试入队一条记录。
		runtime.enqueueFinishedAgent(agentName)
	}()

	managedSystemPrompt := runtime.buildManagedAgentSystemPrompt(agentRecord)
	availableTools := runtime.dependencies.ManagedAgentToolRegistries()
	if availableTools == nil {
		availableTools = tool.NewRegistry()
	}
	executionTrail := newAgentExecutionTrail()
	runResult, runAgentError := service.RunAgentTask(
		agent.HostedAgentTaskInput{Task: requestText},
		agent.AgentExecutionEnvironment{
			WorkingDirectory: runtime.workingDirectory,
			ConversationID:   agentRecord.ConversationID,
		},
		managedSystemPrompt,
		runtime.dependencies.ApplicationConfig,
		availableTools,
		runtime.dependencies.ConversationStore,
		runtime.dependencies.TokenCounter,
		runtime.dependencies.ModelContextWindowTokens,
		service.AgentRunOptions{
			StreamText:             true,
			KeepRecentMemoryTokens: managedAgentKeepRecentMemoryTokens,
			// 对齐 pi 的 reserveTokens：单次输出上限 = 预留响应空间。
			MaximumOutputTokens: managedAgentReserveOutputTokens,
			// 工具结果上限对齐 pi 风格：截断到 2000 字符级别，
			// 避免大输出（bash/file）每轮全量重发导致 token 暴涨。
			MaximumToolResultTokens: 2000,
			// 压缩触发对齐 pi：contextWindow - reserveTokens。
			MaximumStoredMemoryTokens: runtime.dependencies.ModelContextWindowTokens -
				managedAgentReserveOutputTokens,
			ReceiveEvent: func(receivedAgentEvent agent.AgentEvent) {
				executionTrail.recordEvent(receivedAgentEvent)
				runtime.forwardAgentEvent(
					agentRecord.ConversationID,
					receivedAgentEvent,
				)
			},
			// 每轮模型调用前刷新心跳，主管理据此判断 Agent 仍在工作。
			RoundHeartbeat: func() {
				if heartbeatError := runtime.agentRegistry.HeartbeatAgent(
					agentName,
				); heartbeatError != nil {
					slog.Warn("被管理 Agent 心跳写回失败",
						"component", "harness",
						"agent", agentName,
						"error", heartbeatError)
				}
			},
		},
	)
	finalReport := buildManagedAgentFinalReport(
		agentRecord,
		requestText,
		runResult,
		runAgentError,
		executionTrail,
	)
	if runAgentError != nil || looksLikeFailedManagedReport(finalReport) {
		markError := runAgentError
		if markError == nil {
			markError = fmt.Errorf("Agent 未产出有效汇报")
		} else {
			markError = fmt.Errorf("%v\n\n%s", runAgentError, finalReport)
		}
		if markFailedError := runtime.agentRegistry.MarkFailedWithReport(
			agentName,
			markError,
			finalReport,
		); markFailedError != nil {
			slog.Error("被管理 Agent 失败状态写回注册表失败",
				"component", "harness",
				"agent", agentName,
				"error", markFailedError)
		}
	} else {
		if markCompletedError := runtime.agentRegistry.MarkCompleted(
			agentName,
			finalReport,
		); markCompletedError != nil {
			slog.Error("被管理 Agent 完成状态写回注册表失败",
				"component", "harness",
				"agent", agentName,
				"error", markCompletedError)
		}
	}

	runtime.enqueueFinishedAgent(agentName)
	enqueued = true
}

// enqueueFinishedAgent 把指定 Agent 的最新记录推入完成队列。
// 若记录不存在则只写日志（此时 defer 已兜底标记失败）。
func (runtime *Runtime) enqueueFinishedAgent(agentName string) {
	finishedRecord, finishedRecordFound :=
		runtime.agentRegistry.GetAgent(agentName)
	if !finishedRecordFound {
		slog.Error("完成队列入队时 Agent 记录消失",
			"component", "harness",
			"agent", agentName)
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

// buildManagedAgentSystemPrompt 为被管理 Agent 组装 system prompt：
// 统一执行 prompt + 角色身份说明（常驻读专属 AGENTS.md，临时只说明是一次性）。
func (runtime *Runtime) buildManagedAgentSystemPrompt(
	agentRecord ManagedAgentRecord,
) string {
	basePrompt := strings.TrimSpace(runtime.dependencies.ManagedAgentSystemPrompt)
	if agentRecord.Kind == AgentKindResident {
		if residentDefinition, found := ResidentByName(agentRecord.Name); found {
			identityPath, pathError := ResidentDirectoryPath(
				runtime.workingDirectory,
				residentDefinition.Slug,
			)
			if pathError == nil {
				identityPath = filepath.Join(identityPath, "AGENTS.md")
			}
			relativeIdentityPath, relError := RelativeHarnessPath(
				runtime.workingDirectory,
				identityPath,
			)
			if relError == nil {
				identityPath = relativeIdentityPath
			}
			sharedDirectoryPath, _ := SharedDirectoryPath(runtime.workingDirectory)
			relativeSharedPath, _ := RelativeHarnessPath(
				runtime.workingDirectory,
				sharedDirectoryPath,
			)
			basePrompt += fmt.Sprintf(
				"\n\n# 你的身份\n你是常驻专项 Agent「%s」。\n"+
					"请先读取你的身份规则：%s（用 file 或 command 读取）。\n"+
					"共享规则与文档在：%s/AGENTS.md、%s/memory.md、%s/docs/。\n"+
					"你的专属文档目录：%s/docs/。\n"+
					"禁止读取其他常驻 Agent 的专属 docs 与其他会话文件。",
				agentRecord.Name,
				identityPath,
				relativeSharedPath,
				relativeSharedPath,
				relativeSharedPath,
				relativeSharedPath,
			)
		}
	} else {
		basePrompt += fmt.Sprintf(
			"\n\n# 你的身份\n你是临时 Agent「%s」（一次性任务）。\n"+
				"你没有专属长期文档目录；不要创建或依赖 residents 下的任何目录。\n"+
				"你的最终汇报是回传主管理的唯一通道，必须完整。",
			agentRecord.Name,
		)
	}
	return basePrompt
}

// buildManagedAgentFinalReport 把 Agent 的 Run 结果或错误整理为最终汇报正文。
// 无论成功失败都必须有正文，禁止空结果。
// 失败且无正文时，强制附加本次执行轨迹（产出文本与工具事件），
// 让主管理知道 Agent 干了什么、卡在哪里，而不是只收到一个失败。
func buildManagedAgentFinalReport(
	agentRecord ManagedAgentRecord,
	requestText string,
	runResult agent.AgentRunResult,
	runAgentError error,
	executionTrail *agentExecutionTrail,
) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Agent: %s\n", agentRecord.Name)
	if runAgentError != nil {
		fmt.Fprintf(&builder, "Status: 失败\n")
		fmt.Fprintf(&builder, "Error: %v\n", runAgentError)
	} else if runResult != nil {
		trimmedFinalText := strings.TrimSpace(runResult.FinalText())
		if trimmedFinalText == "" {
			fmt.Fprintf(&builder, "Status: 失败（无正文）\n")
			fmt.Fprintf(&builder, "Reason: 执行结束但未产出任何汇报正文\n")
		} else {
			fmt.Fprintf(&builder, "Status: 完成\n")
			fmt.Fprintf(&builder, "Report:\n%s\n", trimmedFinalText)
		}
	} else {
		fmt.Fprintf(&builder, "Status: 失败（无结果对象）\n")
	}
	fmt.Fprintf(&builder, "Task 原文: %s\n", strings.TrimSpace(requestText))
	// 失败时强制带执行轨迹：即使 Agent 空结果/报错，也把已产生的进度给主管理。
	if executionTrail != nil && !executionTrail.isEmpty() {
		fmt.Fprintf(&builder, "\n== 本次执行进度上下文（截至失败/结束时的轨迹） ==\n%s\n",
			strings.TrimSpace(executionTrail.text()))
	}
	return builder.String()
}

// looksLikeFailedManagedReport 根据汇总正文判断是否应按失败处理。
func looksLikeFailedManagedReport(reportText string) bool {
	return strings.Contains(reportText, "Status: 失败")
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
			kindText := string(agentRecord.Kind)
			if kindText == "" {
				kindText = string(ClassifyAgentName(agentRecord.Name))
			}
			rosterText += fmt.Sprintf(
				"- %s（%s）：状态 %s，已完成任务 %d 个，会话 %s",
				agentRecord.Name,
				kindText,
				agentRecord.Status,
				agentRecord.TaskCount,
				agentRecord.ConversationID,
			)
			if agentRecord.CurrentTask != "" {
				rosterText += fmt.Sprintf("，当前任务：%s", truncateTaskText(agentRecord.CurrentTask))
			}
			if agentRecord.Status == ManagedAgentStatusRunning {
				rosterText += "，" + heartbeatStatusText(agentRecord.LastHeartbeatAt)
			}
			rosterText += "\n"
		}
	}
	mcpLiveStatusText := "- 当前没有运行中的 MCP Server；" +
		"被管理 Agent 只有 command、file、activate_skill、create_skill 四个基础工具。"
	if runtime.dependencies.MCPLiveStatusText != nil {
		mcpLiveStatusText = runtime.dependencies.MCPLiveStatusText()
	}
	remainingTemporarySlots := runtime.agentRegistry.MaximumAgents() -
		runtime.agentRegistry.TemporaryAgentCount()
	if remainingTemporarySlots < 0 {
		remainingTemporarySlots = 0
	}
	return fmt.Sprintf(
		"%s\n\n---\n当前 Agent 资源实况（每次调用时刷新）：\n"+
			"固定常驻 4 个（编码员/调研员/审查员/运维员，不计入容量上限）；\n"+
			"临时 Agent 容量上限 %d 个；当前临时 %d 个，还可创建临时 %d 个；运行中 %d 个。\n"+
			"运行中 Agent 若显示「心跳正常」说明它仍在每轮工作，不要重复启用同名 Agent，也不要新建职责相同的 Agent；\n"+
			"若显示「心跳超时」才说明可能卡死，可决定是否重试。\n"+
			"名单（常驻必须优先复用，禁止另起近义名；临时只做一次性任务）：\n%s\n"+
			"当前 MCP 资源实况（每次调用时刷新）：\n%s\n\n"+
			"当前应用实况（每次调用时刷新）：\n%s",
		runtime.dependencies.HarnessSystemPrompt,
		runtime.agentRegistry.MaximumAgents(),
		runtime.agentRegistry.TemporaryAgentCount(),
		remainingTemporarySlots,
		runtime.agentRegistry.RunningAgentCount(),
		rosterText,
		mcpLiveStatusText,
		runtime.appsLiveStatusText(),
	)
}

// heartbeatStatusText 根据最后心跳时间生成对主管理的可读状态。
func heartbeatStatusText(lastHeartbeatAt float64) string {
	if lastHeartbeatAt <= 0 {
		return "心跳：未知"
	}
	ageSeconds := currentEpochSeconds() - lastHeartbeatAt
	if ageSeconds < heartbeatStaleAfterSeconds {
		return fmt.Sprintf("心跳：正常（%.0f 秒前）", ageSeconds)
	}
	return fmt.Sprintf("心跳：超时（%.0f 秒前，疑似卡死）", ageSeconds)
}

// truncateTaskText 限制当前任务摘要长度，避免实况注入过长。
func truncateTaskText(taskText string) string {
	const maximumTaskTextLength = 120
	trimmedTaskText := strings.TrimSpace(taskText)
	if len([]rune(trimmedTaskText)) <= maximumTaskTextLength {
		return trimmedTaskText
	}
	return string([]rune(trimmedTaskText)[:maximumTaskTextLength]) + "…"
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
// agent、memory 与 docs 三个工具。
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
	if registerDocsToolError := harnessToolRegistry.Register(
		newHarnessDocsTool(runtime),
	); registerDocsToolError != nil {
		return nil, fmt.Errorf("注册 docs 工具失败: %w", registerDocsToolError)
	}
	return harnessToolRegistry, nil
}
