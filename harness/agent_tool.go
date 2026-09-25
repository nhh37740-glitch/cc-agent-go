package harness

import (
	"encoding/json"
	"fmt"
	"strings"

	"cc-agent-go/tool"
)

// managedAgentTool 是 Harness 的编排工具：按名字创建/复用被管理 Agent，
// 把完整自然语言任务交给它后台独立执行，或按名字 forget。
// Go 实现是确定性的：校验参数 → upsert → 后台启动 → 立即返回；
// 没有第二个模型调用，没有自然语言解析。
type managedAgentTool struct {
	runtime *Runtime
}

func newManagedAgentTool(runtime *Runtime) *managedAgentTool {
	return &managedAgentTool{runtime: runtime}
}

func (managedAgentTool) Name() string { return "agent" }

func (toolForManagedAgents *managedAgentTool) Description() string {
	return fmt.Sprintf(`启动或管理一个有持久记忆的独立 Agent。
同一名字永远是同一个 Agent、同一份记忆。
固定 4 个常驻专项 Agent（编码员/调研员/审查员/运维员），职责见各自 residents/<slug>/AGENTS.md；
派常驻任务时 agent 参数必须用固定名，不得另起近义名。
其他名字为临时 Agent：只做一次性任务，不创建专属文档，靠最终汇报回传。
被管理 Agent 可使用 command、file、activate_skill、create_skill 和当前已连接的 MCP 工具。
Agent 数量上限 %d 个；常驻固定占名额，forget 只能移除临时 Agent。
同一个 Agent 正在运行时要等它完成，不能重复启用。`, toolForManagedAgents.runtime.agentRegistry.MaximumAgents())
}

func (managedAgentTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"agent": map[string]any{
				"type":        "string",
				"description": "Agent 名字；不存在即创建，同一个名字永远对应同一个 Agent",
			},
			"request": map[string]any{
				"type":        "string",
				"description": "交给这个 Agent 的完整自然语言内容（forget 为 true 时不需要）",
			},
			"forget": map[string]any{
				"type":        "boolean",
				"description": "为 true 时从名单移除该 Agent 并释放名额；记忆文件保留",
			},
		},
		"required": []string{"agent"},
	}
}

func (toolForManagedAgents *managedAgentTool) Execute(
	toolArguments map[string]any,
	executionEnvironment tool.ToolExecutionEnvironment,
) (string, error) {
	rawAgentName, agentNamePresent := toolArguments["agent"]
	agentName, agentNameIsString := rawAgentName.(string)
	trimmedAgentName := strings.TrimSpace(agentName)
	if !agentNamePresent || !agentNameIsString || trimmedAgentName == "" {
		return "", fmt.Errorf("缺少必填参数 agent（Agent 名字）")
	}

	forgetAgent, _ := toolArguments["forget"].(bool)
	if forgetAgent {
		if IsResidentName(trimmedAgentName) {
			return "", fmt.Errorf(
				"不能 forget 常驻 Agent %q；常驻 Agent 固定保留，只能复用",
				trimmedAgentName,
			)
		}
		forgottenRecord, forgetError :=
			toolForManagedAgents.runtime.agentRegistry.ForgetAgent(
				trimmedAgentName,
			)
		if forgetError != nil {
			return "", forgetError
		}
		forgetResult, encodeError := json.Marshal(map[string]any{
			"agent":          forgottenRecord.Name,
			"conversationId": forgottenRecord.ConversationID,
			"forgotten":      true,
			"note":           "名单记录已移除并释放名额；记忆文件保留，同名重建时记忆延续",
		})
		if encodeError != nil {
			return "", fmt.Errorf("编码 forget 结果失败: %w", encodeError)
		}
		return string(forgetResult), nil
	}

	rawRequestText, requestPresent := toolArguments["request"]
	requestText, requestIsString := rawRequestText.(string)
	trimmedRequestText := strings.TrimSpace(requestText)
	if !requestPresent || !requestIsString || trimmedRequestText == "" {
		return "", fmt.Errorf("缺少必填参数 request（交给 Agent 的完整任务内容）")
	}

	agentRecord, created, upsertError :=
		toolForManagedAgents.runtime.agentRegistry.UpsertAgent(trimmedAgentName)
	if upsertError != nil {
		return "", upsertError
	}
	if !created && agentRecord.Status == ManagedAgentStatusRunning {
		return "", fmt.Errorf(
			"Agent %q 正在运行上一个任务；等它的结果汇报给你后再派新任务，"+
				"或换一个名字，或 forget 后重建",
			trimmedAgentName,
		)
	}
	if markRunningError :=
		toolForManagedAgents.runtime.agentRegistry.MarkRunning(trimmedAgentName); markRunningError != nil {
		return "", markRunningError
	}
	if setTaskError := toolForManagedAgents.runtime.agentRegistry.SetCurrentTask(
		trimmedAgentName,
		trimmedRequestText,
	); setTaskError != nil {
		return "", setTaskError
	}

	go toolForManagedAgents.runtime.runManagedAgent(
		trimmedAgentName,
		trimmedRequestText,
	)

	launchResult, encodeError := json.Marshal(map[string]any{
		"agent":          agentRecord.Name,
		"conversationId": agentRecord.ConversationID,
		"status":         string(ManagedAgentStatusRunning),
		"created":        created,
	})
	if encodeError != nil {
		return "", fmt.Errorf("编码启动结果失败: %w", encodeError)
	}
	return string(launchResult), nil
}
