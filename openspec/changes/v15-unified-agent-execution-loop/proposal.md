## Why

当前代码把 Agent 固定在一个 `workspace` 目录中，并在每次 DeepSeek 调用前把当前会话的全部历史消息放入请求。这样做同时固定了文件位置、会话来源和应用形态；历史越长，每次请求携带的内容越多，狼人杀、剧本杀或元老院也只能继续修改 Agent 内部代码。v15 需要先把 Agent 封装成一个可以由外部程序传入项目目录和会话 ID 的执行单元。

## What Changes

- 新增独立 `agent` 包和封装完成的 `Agent` 类型；一个 Agent 固定拥有模型调用、思考与工具循环、普通工具、MCP 工具、SubAgent、记忆读写、token 统计和记忆压缩能力。
- 删除 Agent 对固定 `workspace` 的依赖。每次 `Agent.Run()` 都必须收到外部程序传入的 `workingDirectory` 和 `conversationId`。
- 当前项目目录中的会话记忆固定保存到 `<workingDirectory>/.cc-agent/sessions/<conversationId>.json`；不同会话 ID 读取和保存不同文件。
- 每次 DeepSeek 调用不再自动携带该会话的全部历史消息。第一次调用只携带当前任务、工作目录、会话记忆文件位置以及按需读取说明。
- DeepSeek 需要旧信息时，通过现有工具调用 `rg`、`head`、`tail` 或 `cat` 读取会话记忆文件；工具返回的必要片段才进入本次模型—工具循环。
- 当前一次 `Agent.Run()` 中已经发生的 assistant 工具调用和对应 `tool_result` 继续发送给下一轮 DeepSeek，保证工具协议完整；以前 HTTP 请求留下的全部历史消息不自动发送。
- 工具执行也接收本次 `workingDirectory`。`bash`、Skill 和文件工具基于该目录运行，不再在构造工具时永久保存一个全局 workspace 路径。
- 使用共用的 Hugging Face tokenizer 执行程序加载当前模型自己的 `tokenizer.json`。当前 DeepSeek V4 配置加载 DeepSeek 官方文件；以后增加模型时增加配置和该模型的 tokenizer 文件，不修改 `Agent.Run()`。
- 使用 `github.com/amikos-tech/pure-tokenizers v0.1.5` 读取 `tokenizer.json`，计算准备发送的请求和会话记忆文件 token；DeepSeek 返回后使用 API `usage` 中的实际输入、输出 token 校正本次请求统计。达到配置的记忆 token 阈值后压缩会话记忆文件。
- 分别统计“当前 API 请求 token”和“会话记忆文件 token”。会话文件没有被工具读取时不占用当前 API 上下文。
- 主 Agent 与 SubAgent 调用同一个 `Agent.Run()`；SubAgent、后台回调和未来应用都由调用者传入自己的工作目录和会话 ID。
- 狼人杀、剧本杀、元老院作为 Agent 外部的第二次包装：它们保存自己的角色、回合和规则，然后把工作目录、角色对应的会话 ID 和当前任务传给 `Agent.Run()`；不得在 `agent` 包中增加应用名称或应用分支。
- 使用不同的具体事件类型表示轮次、文字、工具、记忆读取、记忆压缩、记忆保存和 Agent 完成；删除首字符 `{` 判断。
- 保留浏览器 WebAgent。用户选择项目工作目录后，页面读取该项目的全部会话摘要；点击会话后加载历史消息。用户开始新会话时不填写会话 ID，由 Go HTTP handler 调用 `service.GenerateConversationId()` 创建，再把实际 ID 传给 `Agent.Run()`。
- WebAgent 页面显示本次 Agent SSE 事件和服务端结构化日志；日志接口只返回 `logs/server.jsonl` 中已经脱敏的最近记录。
- WebAgent 页面读取并管理 MCP Server 列表；请求失败时显示实际 HTTP 状态和后端错误 JSON，不能把“Go 服务未启动”和“配置中没有 MCP Server”显示成同一个结果。
- 非目标：本次不实现狼人杀、剧本杀或元老院的新版本；不修改 `RunCouncil`；不增加取消、重试、checkpoint、服务重启恢复、A2A、WebSocket 或 MCP 并发控制。

## Capabilities

### New Capabilities

- `unified-agent-execution-loop`: 外部程序为每次运行传入项目工作目录、会话 ID 和当前任务；`agent.Agent` 在指定目录中按需取得历史记忆，执行模型与工具循环，统计 token，保存或压缩会话记忆，并让外部应用在不修改 Agent 内部代码的情况下重复使用它。

### Modified Capabilities

无。

## Impact

- 新增 `agent/` 包及对应测试，保存 `Agent`、运行环境、任务输入、唯一循环、运行结果、模型调用函数、token 计数函数和具体事件类型。
- 新增 `memory/` 包及对应测试；从 `service/store.go` 迁移会话 JSON 读写、归档、会话锁和压缩后保存，并改为根据每次运行的工作目录计算文件位置。
- 新增 `modeltoken/` 和模型 tokenizer 配置文件；`go.mod` 允许且只为 tokenizer 增加 `github.com/amikos-tech/pure-tokenizers v0.1.5`。启动服务时加载固定版本的原生 tokenizer 库和 DeepSeek V4 官方 `tokenizer.json`，不得在每次 Agent 请求中重新下载。
- 修改 `tool/`，让工具执行函数收到本次 Agent 的工作目录和会话 ID；`BashTool` 不再保存固定 workspace。
- 修改 `service/agent.go` 和 `service/subagent.go`，创建具体 Agent 运行环境与任务输入并调用 `Agent.Run()`，不再拥有模型—工具循环或手工加载全部会话消息。
- 修改 `main.go` 和 HTTP 请求类型，接收前端传来的 `workingDirectory`、可为空的 `conversationId` 和任务；为空时由 handler 创建 ID，再按具体 Agent 事件编码 HTTP/SSE。
- 修改 `main.go`，保留按项目目录列出、读取和删除会话的接口，并增加只读取最近脱敏 JSON 日志的接口。
- 修改 `index.html`，实现项目会话列表、历史消息、新会话、任务输入、运行日志、最终结果和 MCP Server 管理。
- 更新 `PROJECT_INDEX.md`、`ROADMAP.md`、`README.md` 和 `AGENTS.md` 中的实际调用关系与版本状态。
