## Why

当前 `run_subagent` 会等待全部 SubAgent 完成，导致主 Agent 的聊天请求和网页输入框一直等待。需要让 SubAgent 在后台继续执行，并在完成后主动调用主 Agent，再把主 Agent 的新回复推送给正在查看该会话的网页。

## What Changes

- `run_subagent` 启动 SubAgent goroutine 后立即返回已启动的任务信息，不再等待最终结果。
- `run_subagent` 的每个任务由调用它的 LLM 填写 `maximumRounds`；Go 配置只给出允许填写的最高轮数，不再把所有 SubAgent 固定为 12 轮。
- SubAgent 完成后调用固定回调；回调读取主 Agent 最新会话记录，把任务和执行结果交给主 Agent，继续执行现有 DeepSeek 和工具循环。
- 新增独立的会话事件 SSE 路由。聊天 SSE 每次回复后固定关闭，会话事件 SSE 在网页查看会话期间固定保持连接。
- Web 页面进入会话时建立会话事件 SSE，收到后台主 Agent token 后创建并更新新的主 Agent 消息。
- 暂不实现任务查询工具、取消、重试、checkpoint、跨进程恢复或 MCP 并发控制。

## Capabilities

### New Capabilities

- `background-subagent-callback`: SubAgent 后台完成后主动调用主 Agent，并通过会话事件 SSE 向网页推送主 Agent 回复。

### Modified Capabilities

无。

## Impact

- 修改 `main.go` 中的 `run_subagent` 注册、聊天处理和 HTTP 路由。
- 修改 `service/subagent.go`，增加后台启动和完成回调。
- 修改 `config/config.go` 和 `run_subagent` 参数定义，读取 SubAgent 最高轮数并验证每个任务的 `maximumRounds`。
- 修改 `service/agent.go`，增加使用既有会话继续处理 SubAgent 结果的入口。
- 新增保存 `conversationId` 对应网页事件 channel 的正式 Go 文件。
- 修改 `index.html`，建立会话事件 SSE 并显示后台主 Agent 回复。
- 更新相关测试、`PROJECT_INDEX.md`、`README.md`、`ROADMAP.md` 和 `AGENTS.md`。
- 继续只使用 Go 标准库，不修改 `go.mod`。
