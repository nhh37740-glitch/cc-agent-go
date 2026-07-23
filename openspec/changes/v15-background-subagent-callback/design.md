## Context

当前 `main.go` 在服务启动时把一个全局 `run_subagent` 执行函数注册到全局工具表。这个函数调用 `service.RunSubAgentsInParallel`，而该函数必须从 `completedSubAgentResults` channel 收齐全部结果后才返回。`service.RunStream` 因此停在 `registry.Execute`，`POST /api/chat/stream` 不能结束，`index.html` 的输入框也一直禁用。

当前聊天 SSE 的 `http.ResponseWriter` 只属于一次 POST 请求。处理函数返回后不能保存该 `http.ResponseWriter` 给后续 SubAgent 使用。因此后台回复使用另一条固定的 GET SSE，而不是让聊天 SSE 根据是否启动 SubAgent决定是否关闭。

## Goals / Non-Goals

**Goals:**

- `run_subagent` 启动后台 goroutine 后立即返回。
- SubAgent结果完成后直接调用主 Agent，不依赖模型调用查询工具。
- 用户在 SubAgent执行期间可以继续发送消息并收到主 Agent回复。
- 聊天 SSE 每次固定关闭，会话事件 SSE 固定保持。
- 回调读取最新主 Agent会话，避免使用启动 SubAgent时的旧消息记录。
- 只使用 Go 标准库。

**Non-Goals:**

- 不实现任务查询工具。
- 不实现取消、超时、重试、checkpoint 或服务重启恢复。
- 不解决多个 SubAgent 同时操作同一个 MCP Server 的问题。
- 不增加 WebSocket 或第三方依赖。

## Decisions

### 1. 每次聊天请求创建包含 `conversationId` 的 `run_subagent` 执行函数

`handleChat` 和 `handleChatStream` 在调用 `service.Run` 或 `service.RunStream` 前确定 `conversationId`。它们复制当前全局工具表，在复制结果中注册本次请求自己的 `run_subagent`。这个执行函数直接保留本次 `conversationId` 和完成回调。

没有让 DeepSeek 在工具参数中填写 `conversationId`。这个值已经由 Go 后端确定，再让模型填写可能把结果交给错误会话。

### 2. `run_subagent` 只启动一个收集全部结果的后台 goroutine

工具执行函数验证 JSON 并复制可用工具以后，调用新的后台启动函数。后台函数内部继续调用现有 `RunSubAgentsInParallel`；全部结果返回后调用一次完成回调。工具执行函数立即返回每个 `taskId` 和 `status: "running"`。

没有给每个任务分别调用主 Agent。一次工具调用只产生一次主 Agent回调，避免同一批任务生成多条互相分开的总结。

### 3. 同一会话的主 Agent调用使用同一把锁

`main.go` 保存 `conversationId` 对应的 `sync.Mutex`。普通聊天调用和 SubAgent完成回调在调用 `service.Run`、`service.RunStream` 或继续函数前取得同一把锁。

如果 SubAgent在原聊天回复保存以前完成，回调会等待原回复保存后再读取会话。如果用户消息正在调用主 Agent，回调也等待该调用完成。这样每次主 Agent调用都能读取上一项已经保存的完整结果。

### 4. SubAgent结果只作为本次 DeepSeek输入，不保存成用户消息

`service/agent.go` 增加继续函数。它读取最新会话，把包含任务结果的内部 user 消息加入本次 DeepSeek history，但不把这条内部消息加入 `newMessages`。DeepSeek产生的最终 assistant 消息正常保存。

没有直接调用现有公开 `RunStream`，因为它会把传入文字保存成一条普通用户消息，网页重新加载会把内部回调内容显示成用户输入。

### 5. 会话事件连接保存 channel，不保存 `http.ResponseWriter`

新增的事件连接类型保存：

```text
conversationId → 当前连接使用的一个或多个 chan []byte
```

`handleConversationEvents` 自己持有 `http.ResponseWriter`，从它注册的 channel 读取已经编码好的 JSON，写成 SSE 并执行 `Flush()`。浏览器断开时，处理函数删除自己的 channel。

没有把 `http.ResponseWriter` 放进全局 map。只有创建它的 HTTP 处理函数持续写入它，避免多个 goroutine 同时直接操作同一个 writer。

### 6. 聊天 SSE 和会话事件 SSE 使用固定职责

- `POST /api/chat/stream`：处理一条用户消息，回复完成后固定关闭。
- `GET /api/conversations/{id}/events`：网页进入会话时建立，网页离开时关闭。

SubAgent回调把 `background_reply_started`、`background_reply_token`、`background_reply_completed` 或 `background_reply_failed` JSON 写入会话事件 channel。完成事件包含完整回复；即使某个 token 事件因浏览器速度过慢没有进入缓冲 channel，完成事件仍能把页面文字修正为完整结果。

## Risks / Trade-offs

- [浏览器尚未建立事件 SSE 时后台结果已经完成] → 主 Agent最终回复仍写入会话 JSON；当前版本优先保证流式聊天页面，重新加载会话仍能读取最终回复。
- [多个用户消息和回调同时调用同一会话] → 使用 `conversationId` 对应的锁让主 Agent调用按取得锁的顺序执行。
- [网页读取 token 速度过慢] → channel 使用缓冲区，完成事件携带完整文字用于最终修正。
- [服务重启时后台 goroutine 消失] → 当前版本不实现恢复，后续 checkpoint 版本处理。

## Migration Plan

1. 新增会话执行锁和会话事件 channel 保存代码。
2. 新增 GET 会话事件 SSE 路由。
3. 把 `run_subagent` 改成按聊天请求注册并立即返回。
4. 增加 SubAgent完成回调和主 Agent继续函数。
5. 修改 Web 页面建立事件 SSE并显示后台回复。
6. 增加测试并执行完整 Go 检查。

回滚时恢复全局同步 `run_subagent` 注册，删除事件 SSE 路由和 Web EventSource 代码；现有会话 JSON 不需要迁移。

## Open Questions

无。当前按用户确认的“短连接处理主动聊天，长连接接收后台回调”执行。
