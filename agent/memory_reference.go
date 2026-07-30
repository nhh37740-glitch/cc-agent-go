package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cc-agent-go/memory"
)

type AgentMemoryReference struct {
	WorkingDirectory         string
	RelativeSessionFilePath  string
	RelativeProjectRulesPath string
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
		AllowedReadingCommands:   []string{"rg", "head", "tail", "cat"},
	}, strings.TrimSpace(string(projectRulesContent)), nil
}

func (memoryReference AgentMemoryReference) SystemInstruction() string {
	return fmt.Sprintf(
		"本次工作目录：%s\n本次会话编号：%s\n历史记录文件：%s\n项目规则文件：%s\n"+
			"不要预先读取或要求发送全部历史记录。只有当前任务需要旧信息时，"+
			"才使用 bash 的 rg、head、tail 或 cat 读取历史记录文件中的必要片段。",
		memoryReference.WorkingDirectory,
		filepath.Base(strings.TrimSuffix(memoryReference.RelativeSessionFilePath, ".json")),
		memoryReference.RelativeSessionFilePath,
		memoryReference.RelativeProjectRulesPath,
	)
}
