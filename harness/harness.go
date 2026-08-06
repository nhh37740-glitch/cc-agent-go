package harness

import (
	"fmt"
	"os"
	"strings"

	"cc-agent-go/agent"
	"cc-agent-go/config"
	"cc-agent-go/memory"
	"cc-agent-go/service"
	"cc-agent-go/tool"
)

// Prompts 保存启动时加载的两个 prompt 文件内容。
type Prompts struct {
	HarnessSystemPrompt      string
	ManagedAgentSystemPrompt string
}

// LoadPrompts 启动时加载 Harness system prompt 与被管理 Agent 统一执行
// prompt；任一缺失返回指明缺失文件的配置错误，Harness 路由不提供服务。
func LoadPrompts(
	harnessPromptFilePath string,
	managedAgentPromptFilePath string,
) (Prompts, error) {
	harnessPromptContent, readHarnessPromptError :=
		os.ReadFile(harnessPromptFilePath)
	if readHarnessPromptError != nil {
		return Prompts{}, fmt.Errorf(
			"加载 Harness system prompt 失败（%s）: %w",
			harnessPromptFilePath,
			readHarnessPromptError,
		)
	}
	managedAgentPromptContent, readManagedAgentPromptError :=
		os.ReadFile(managedAgentPromptFilePath)
	if readManagedAgentPromptError != nil {
		return Prompts{}, fmt.Errorf(
			"加载被管理 Agent 执行 prompt 失败（%s）: %w",
			managedAgentPromptFilePath,
			readManagedAgentPromptError,
		)
	}
	return Prompts{
		HarnessSystemPrompt:      strings.TrimSpace(string(harnessPromptContent)),
		ManagedAgentSystemPrompt: strings.TrimSpace(string(managedAgentPromptContent)),
	}, nil
}

// BuildRuntimeDependencies 把 prompt 与外部资源装配成 RuntimeDependencies。
// 完成队列消费 goroutine 由 GetOrCreateRuntime 在首次创建时启动。
func BuildRuntimeDependencies(
	prompts Prompts,
	applicationConfig config.Config,
	conversationStore *memory.ProjectConversationStore,
	tokenCounter agent.AgentTokenCounter,
	modelContextWindowTokens int,
	conversationEventReceivers *service.ConversationEventReceivers,
	conversationExecutionLocks *service.ConversationExecutionLocks,
	mcpLiveStatusText func() string,
	managedAgentToolRegistries func() *tool.Registry,
) RuntimeDependencies {
	return RuntimeDependencies{
		ApplicationConfig:          applicationConfig,
		ConversationStore:          conversationStore,
		TokenCounter:               tokenCounter,
		ModelContextWindowTokens:   modelContextWindowTokens,
		ConversationEventReceivers: conversationEventReceivers,
		ConversationExecutionLocks: conversationExecutionLocks,
		HarnessSystemPrompt:        prompts.HarnessSystemPrompt,
		ManagedAgentSystemPrompt:   prompts.ManagedAgentSystemPrompt,
		ManagedAgentToolRegistries: managedAgentToolRegistries,
		MCPLiveStatusText:          mcpLiveStatusText,
	}
}
