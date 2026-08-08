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
同一个名字永远是同一个 Agent、同一份记忆：第一次调用写清角色与职责，之后写当前任务。
Agent 在后台独立执行，工具立即返回 running；完成后结果会自动汇报给你。
被管理 Agent 可使用 bash、activate_skill、create_skill 和当前已连接的 MCP 工具。
Agent 数量上限 %d 个；名额满时可以用 forget 释放不再需要的 Agent，或复用现有 Agent。
forget 为 true 时从名单移除该 Agent 并释放名额；它的记忆文件保留，同名重建时记忆延续。`,
		toolForManagedAgents.runtime.agentRegistry.MaximumAgents())
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
