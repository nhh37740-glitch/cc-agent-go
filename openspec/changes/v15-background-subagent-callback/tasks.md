## 1. 保存会话事件连接

- [x] 1.1 新增 `service/conversation_events.go`，保存 `conversationId` 对应的网页事件 channel
- [x] 1.2 实现增加接收者、删除接收者和向全部接收者发送 JSON
- [x] 1.3 增加并发测试，确认不同会话隔离、同一会话多个接收者都能收到消息

## 2. 建立固定长连接

- [x] 2.1 在 `main.go` 注册 `GET /api/conversations/{id}/events`
- [x] 2.2 实现 `handleConversationEvents`，写 SSE 响应头、发送心跳并等待 channel 或浏览器断开
- [x] 2.3 增加 HTTP 测试，确认事件写入后收到 SSE，取消请求后接收者被删除

## 3. 后台启动 SubAgent

- [x] 3.1 在 `service/subagent.go` 增加后台启动函数和明确的完成回调类型
- [x] 3.2 修改 `run_subagent` 输出为每个任务的 `taskId` 和 `status: "running"`
- [x] 3.3 让每次聊天请求注册保留当前 `conversationId` 的 `run_subagent` 执行函数
- [x] 3.4 增加测试，确认工具立即返回且完成后只调用一次回调

## 4. SubAgent完成后调用主 Agent

- [x] 4.1 增加 `conversationId` 对应的主 Agent执行锁
- [x] 4.2 在 `service/agent.go` 增加读取最新会话并处理 SubAgent结果的流式继续函数
- [x] 4.3 确认内部 SubAgent结果进入 DeepSeek history，但不作为普通用户消息保存
- [x] 4.4 在完成回调中调用主 Agent继续函数，并发送开始、token、完成或失败事件
- [x] 4.5 增加测试，确认回调等待当前主 Agent执行结束并读取最新会话

## 5. Web页面接收后台回复

- [x] 5.1 在 `index.html` 根据当前 `conversationId` 创建和关闭 `EventSource`
- [x] 5.2 收到后台回复开始事件时创建新的主 Agent消息
- [x] 5.3 收到 token 时更新消息，收到完成事件时使用完整文字结束消息
- [x] 5.4 保持 `POST /api/chat/stream` 的原有关闭方式，不根据 SubAgent状态修改

## 6. 文档和完整验证

- [x] 6.1 更新 `PROJECT_INDEX.md`、`README.md`、`ROADMAP.md` 和 `AGENTS.md`
- [x] 6.2 运行 `go fmt ./...`
- [x] 6.3 运行 `go build ./...`、`go vet ./...` 和 `go test ./...`
- [x] 6.4 启动服务，用浏览器验证聊天短连接关闭、输入框恢复和后台回复从长连接出现

## 7. 让调用 LLM 配置每个 SubAgent 的轮数

- [x] 7.1 在 `config.Config` 增加最高允许轮数，并从 `MAXIMUM_SUBAGENT_ROUNDS` 读取
- [x] 7.2 在每个 `SubAgentTask` 和 `run_subagent` JSON Schema 中增加必填 `maximumRounds`
- [x] 7.3 启动任务前验证 `maximumRounds`，并把每个任务自己的数值传给 `RunSubAgent`
- [x] 7.4 增加配置、参数验证和实际轮数测试，更新 `PROJECT_INDEX.md` 与相关文档
- [x] 7.5 运行格式化、编译、静态检查、全部测试和 OpenSpec 校验

## 8. 保留部分结果并限制主 Agent职责

- [x] 8.1 让 `RunSubAgent` 最后一轮禁用工具并整理已有结果
- [x] 8.2 增加 `limit_reached` 状态，轮数耗尽时同时保留 `result` 和 `error`
- [x] 8.3 让主 Agent成功执行 `run_subagent` 后保存启动回复并结束当前调用
- [x] 8.4 回调主 Agent使用空工具表，并明确只分析汇总、不得继续执行任务
- [x] 8.5 增加部分结果、停止原主 Agent工具调用和回调无工具的测试，更新项目文档
- [x] 8.6 运行格式化、编译、静态检查、全部测试、OpenSpec 校验并重启服务
