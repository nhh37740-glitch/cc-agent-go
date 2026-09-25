package tool

import "context"

type ToolExecutionEnvironment struct {
	WorkingDirectory string
	ConversationID   string
	// Context 是本次工具执行所属的会话运行上下文。
	// 用户停止或客户端断开时会被取消；工具应把它作为 CommandContext 父上下文。
	Context context.Context
}
