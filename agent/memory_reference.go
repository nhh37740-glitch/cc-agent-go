package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cc-agent-go/memory"
	"cc-agent-go/model"
)

type AgentMemoryReference struct {
	WorkingDirectory         string
	RelativeSessionFilePath  string
	RelativeProjectRulesPath string
	ProjectRulesPresent      bool
	AllowedReadingCommands   []string
}

func buildAgentMemoryReference(
	executionEnvironment AgentExecutionEnvironment,
	conversationStore *memory.ProjectConversationStore,
) (AgentMemoryReference, string, error) {
	sessionFilePath, buildSessionFilePathError :=
		conversationStore.SessionFilePath(
			executionEnvironment.WorkingDirectory,
			executionEnvironment.ConversationID,
		)
	if buildSessionFilePathError != nil {
		return AgentMemoryReference{}, "", buildSessionFilePathError
	}
	relativeSessionFilePath, buildRelativeSessionPathError := filepath.Rel(
		executionEnvironment.WorkingDirectory,
		sessionFilePath,
	)
	if buildRelativeSessionPathError != nil {
		return AgentMemoryReference{}, "", buildRelativeSessionPathError
	}
	projectRulesPath := filepath.Join(executionEnvironment.WorkingDirectory, "AGENTS.md")
	projectRulesContent, readProjectRulesError := os.ReadFile(projectRulesPath)
	if readProjectRulesError != nil && !os.IsNotExist(readProjectRulesError) {
		return AgentMemoryReference{}, "", fmt.Errorf(
			"读取项目 AGENTS.md 失败: %w",
			readProjectRulesError,
		)
	}
	return AgentMemoryReference{
		WorkingDirectory:         executionEnvironment.WorkingDirectory,
		RelativeSessionFilePath:  filepath.ToSlash(relativeSessionFilePath),
		RelativeProjectRulesPath: "AGENTS.md",
		ProjectRulesPresent:      readProjectRulesError == nil,
		AllowedReadingCommands:   []string{"rg", "head", "tail", "cat"},
	}, strings.TrimSpace(string(projectRulesContent)), nil
}

func (memoryReference AgentMemoryReference) SystemInstruction() string {
	projectRulesInstruction := "项目规则文件：" + memoryReference.RelativeProjectRulesPath
	if !memoryReference.ProjectRulesPresent {
		projectRulesInstruction += "（工作目录根不存在 AGENTS.md，本请求未附带项目规则；" +
			"如需项目规则，先用工具查看工作目录结构确认）"
	}
	return fmt.Sprintf(
		"本次工作目录：%s\n本次会话编号：%s\n历史记录文件：%s\n%s\n"+
			"历史记录文件中的摘要和最近对话已经附在本次消息开头，供你直接使用。\n"+
			"只有需要更早的旧信息时，才使用 bash 的 rg、head、tail 或 cat 读取历史记录文件中的必要片段。",
		memoryReference.WorkingDirectory,
		filepath.Base(strings.TrimSuffix(memoryReference.RelativeSessionFilePath, ".json")),
		memoryReference.RelativeSessionFilePath,
		projectRulesInstruction,
	)
}

// loadRecentConversation 从会话文件加载「历史摘要 + 最近对话」，供装配进请求上下文。
// 摘要（以 [历史会话摘要] 开头）固定保留在开头；最近消息按 token 预算从最新往回取。
// 会话文件不存在或为空时返回空列表，不视为错误。
func loadRecentConversation(
	executionEnvironment AgentExecutionEnvironment,
	conversationStore *memory.ProjectConversationStore,
	maximumRecentTokens int,
	tokenCounter AgentTokenCounter,
) ([]model.Message, error) {
	conversation, loadConversationError := conversationStore.LoadConversation(
		executionEnvironment.WorkingDirectory,
		executionEnvironment.ConversationID,
	)
	if loadConversationError != nil {
		return nil, loadConversationError
	}
	if conversation == nil || len(conversation.Messages) == 0 {
		return nil, nil
	}
	// 预算小于等于 0 表示不主动装配历史（旧行为：靠工具现读）。
	if maximumRecentTokens <= 0 {
		return nil, nil
	}

	allMessages := conversation.Messages
	var historySummary *model.Message
	historySummaryIndex := -1
	for index := range allMessages {
		if isHistorySummaryMessage(allMessages[index]) {
			historySummary = &allMessages[index]
			historySummaryIndex = index
			break
		}
	}

	// 从最新往回选，累计到预算；摘要不计入预算。
	selected := make([]model.Message, 0)
	budgetUsed := 0
	for index := len(allMessages) - 1; index >= 0; index-- {
		if index == historySummaryIndex {
			continue
		}
		messageTokens, countMessageError :=
			tokenCounter.CountText(messageContentText(allMessages[index]))
		if countMessageError != nil {
			return nil, countMessageError
		}
		if maximumRecentTokens > 0 && budgetUsed+messageTokens > maximumRecentTokens {
			continue
		}
		budgetUsed += messageTokens
		selected = append(selected, allMessages[index])
	}

	// 反转为正序：摘要在最前，最近消息按时间顺序。
	result := make([]model.Message, 0, len(selected)+1)
	if historySummary != nil {
		result = append(result, *historySummary)
	}
	for index := len(selected) - 1; index >= 0; index-- {
		result = append(result, selected[index])
	}
	return result, nil
}

func isHistorySummaryMessage(message model.Message) bool {
	return strings.HasPrefix(
		strings.TrimSpace(messageContentText(message)),
		"[历史会话摘要]",
	)
}

func messageContentText(message model.Message) string {
	var textParts []string
	for _, contentBlock := range message.Content {
		switch concreteContentBlock := contentBlock.(type) {
		case model.TextContentBlock:
			textParts = append(textParts, concreteContentBlock.Text)
		case model.ToolUseContentBlock:
			textParts = append(textParts, fmt.Sprintf("[tool_use:%s]", concreteContentBlock.Name))
		case model.ToolResultContentBlock:
			textParts = append(textParts, "[tool_result]")
		}
	}
	return strings.Join(textParts, " ")
}
