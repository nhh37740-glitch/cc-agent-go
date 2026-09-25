package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var safeConversationIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)

type AgentExecutionEnvironment struct {
	WorkingDirectory string
	ConversationID   string
	// Context 控制本次 Run 是否继续。用户停止或客户端断开时会被取消。
	// Validate 不检查 Context；为空时 Run 使用 context.Background()。
	Context context.Context
}

func (executionEnvironment AgentExecutionEnvironment) Validate() error {
	if executionEnvironment.WorkingDirectory == "" {
		return fmt.Errorf("WorkingDirectory 不能为空")
	}
	if !filepath.IsAbs(executionEnvironment.WorkingDirectory) {
		return fmt.Errorf("WorkingDirectory 必须是绝对路径: %s", executionEnvironment.WorkingDirectory)
	}
	workingDirectoryInformation, readWorkingDirectoryError :=
		os.Stat(executionEnvironment.WorkingDirectory)
	if readWorkingDirectoryError != nil {
		return fmt.Errorf("WorkingDirectory 无法读取: %w", readWorkingDirectoryError)
	}
	if !workingDirectoryInformation.IsDir() {
		return fmt.Errorf("WorkingDirectory 不是目录: %s", executionEnvironment.WorkingDirectory)
	}
	if !safeConversationIDPattern.MatchString(executionEnvironment.ConversationID) {
		return fmt.Errorf("ConversationID 只允许字母、数字、点、下划线和连字符，长度必须为 1 到 100")
	}
	return nil
}
