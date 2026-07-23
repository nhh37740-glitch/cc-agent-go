# cc-agent-go 项目索引

本文件记录正式 Go 服务的文件、包名、导出函数和主要调用位置。`democode/` 是历史教学程序，不写入本索引。

更新规则：修改根目录 `main.go`、`config/`、`model/`、`service/` 或 `tool/` 中的正式 Go 文件时，同一次修改更新对应条目。回答代码问题时，先读取本文件；本文件没有该项目时，才读取代码并补充条目。

| 文件 | 包名 | 导出类型和函数 | 调用关系 |
| --- | --- | --- | --- |
| `main.go` | `main` | `RunSubAgentToolInput`、`StartedSubAgentTask`、`RunSubAgentToolOutput`、四种后台回复事件类型、`main` | `handleChat` 和 `handleChatStream` 先确定 `conversationId`，调用 `createConversationToolRegistry` 复制当前工具表并注册保留该 ID 的 `run_subagent`，再取得该会话的主 Agent执行锁并调用 `service.Run` 或 `service.RunStream`。`run_subagent` 调用 `service.RunSubAgentsInBackground` 后立即返回 `tasks:[{"taskId","status":"running"}]`。全部 SubAgent完成时调用 `continueMainAgentAfterSubAgents`；该函数取得同一会话执行锁，调用 `service.ContinueConversationAfterSubAgentsStream`，再把开始、token、完成或失败 JSON 写入会话事件 channel。`handleConversationEvents` 处理 `GET /api/conversations/{id}/events`，一直等待 channel、心跳或浏览器断开。MCP 列表、选择和停止仍由 `MCPServerManager` 处理。 |
| `config/config.go` | `config` | `Config`、`Load` | `Load` 调用 `loadDeepSeekAPIKey`：先读取 `DEEPSEEK_API_KEY`，空值时读取 `config/local.json` 的 `deepseekApiKey`；`CC_AGENT_LOCAL_CONFIG` 可以替换本地配置文件路径。`Load` 还读取 `MAX_PARALLEL_SUBAGENTS`；并行数只接受 1–5，空值或非法值使用 5。API Key 最终仍为空时，`service.Chat` 和 `ChatStream` 返回 `config_error`。 |
| `model/types.go` | `model` | `MessageContentBlock`、`TextContentBlock`、`ToolUseContentBlock`、`ToolResultContentBlock`、`Message`、`ToolCall`、`ChatRequest`、`ChatResponse`、`ErrorResponse`、`ApiResponse`、`SessionJson`、`ConversationSummary`、`Compressor` | `Message.Content` 保存三种独立内容类型。`ToolUseContentBlock.MarshalJSON` 强制写入 `input`，无参数时写 `{}`；`Message.UnmarshalJSON` 读取每块的 `type` 后创建对应具体类型。`main.go` 和 `service/` 创建并读取这些具体类型。 |
| `service/agent.go` | `service` | `Run`、`RunStream`、`ContinueConversationAfterSubAgentsStream`、`GenerateConversationId` | `handleChat` 调用 `Run`，`handleChatStream` 调用 `RunStream`。SubAgent完成回调调用 `ContinueConversationAfterSubAgentsStream`；它读取最新会话，把 SubAgent结果只放入本次 DeepSeek history，然后通过共用的 `runStreamWithInputMessage` 执行现有工具循环，只保存最终 assistant 回复。 |
| `service/errors.go` | `service` | `ErrorKind`、`AppError`、`NewAppError` | `Chat`、`ChatStream` 和 `Run` 创建分类错误；`main.go` 使用 `errors.As` 读取 `Kind` 和 `ProviderStatus`。 |
| `service/client.go` | `service` | `Chat` | 向 DeepSeek 发送非流式请求；检查 API Key、网络错误、HTTP 状态和响应 JSON。 |
| `service/stream.go` | `service` | `ChatStream` | 向 DeepSeek 发送流式请求；检查 API Key、网络错误和 HTTP 状态，再读取 SSE 数据。 |
| `service/subagent.go` | `service` | `SubAgentTask`、`SubAgentResult`、`CompletedSubAgentResultsCallback`、`RunSubAgent`、`RunSubAgentsInParallel`、`RunSubAgentsInBackground` | `RunSubAgent` 使用只包含本次任务的临时消息记录，执行最多 12 轮 DeepSeek 和工具调用，不读写会话文件。`RunSubAgentsInParallel` 并行执行并按输入位置返回。`RunSubAgentsInBackground` 启动一个后台 goroutine 后立即返回；后台收齐全部结果后调用一次回调，并明确传回父 `conversationId`。 |
| `service/conversation_events.go` | `service` | `ConversationEventReceivers`、`NewConversationEventReceivers`；方法：`AddReceiver`、`RemoveReceiver`、`SendEventJSON`、`ReceiverCount` | `handleConversationEvents` 为每个网页 GET 长连接增加一个缓冲 channel并在断开时删除。SubAgent完成回调调用 `SendEventJSON`，同一会话当前打开的全部网页都收到事件。 |
| `service/conversation_execution.go` | `service` | `ConversationExecutionLocks`、`NewConversationExecutionLocks`；方法：`LockConversation` | 普通聊天处理函数和 SubAgent完成回调在调用主 Agent前使用同一个 `conversationId` 取得同一把锁；不同会话使用不同锁。 |
| `service/store.go` | `service` | `Store`、`NewStore`、`LoadMemory`；`Store` 方法：`LoadMessages`、`LoadConversation`、`ListConversations`、`DeleteConversation`、`AppendTurn`、`AppendTurnWithCompression` | `main.go` 创建 `Store`；`Run` 和 `RunStream` 调用消息读取和保存方法；`firstUserText` 按完整 Unicode 字符生成最多 40 字符的标题；`ListConversations` 调用 `readableSessionTitle`，旧标题含 `�` 时从第一条完整用户消息重新生成列表标题；存储日志使用 `slog`。 |
| `service/council.go` | `service` | `Speech`、`CouncilRequest`、`CouncilResponse`、`RunCouncil` | `main.go` 的 `handleCouncil` 调用 `RunCouncil`；`RunCouncil` 调用 `Chat`。 |
| `tool/tool.go` | `tool` | `Tool` | `BashTool`、`SkillTool` 和 `CreateSkillTool` 都实现 `Tool` 的 `Name`、`Description`、`InputSchema`、`Execute` 方法。 |
| `tool/registry.go` | `tool` | `Registry`、`NewRegistry`；方法：`CopyExcludingTools` | `main.go` 创建工具表；`Run` 和 `RunStream` 调用 `GetDefinitions` 与 `Execute`；`MCPServerManager` 调用 `RegisterFunctionTool` 和 `Unregister` 动态增加或删除 MCP 工具。`CopyExcludingTools` 复制调用时的工具并排除指定名称。内部使用 `sync.RWMutex` 保护工具 map。 |
| `tool/function.go` | `tool` | `FunctionTool`、`FunctionToolExecuteFunction`、`NewFunctionTool` | `RegisterFunctionTool` 把工具名称、说明、参数定义和执行函数保存为一个普通 `Tool`。MCP 工具保存的执行函数调用 `MCPServerManager.callMCPServerTool`。 |
| `tool/bash.go` | `tool` | `BashTool`、`NewBashTool` | `main.go` 注册 `BashTool`；`Registry.Execute` 调用它的 `Execute` 方法。 |
| `tool/skill.go` | `tool` | `SkillTool`、`NewSkillTool` | `main.go` 注册 `SkillTool`；`Registry.Execute` 调用它的 `Execute` 方法。 |
| `tool/create_skill.go` | `tool` | `CreateSkillTool`、`NewCreateSkillTool` | `main.go` 注册 `CreateSkillTool`；`Registry.Execute` 调用它的 `Execute` 方法。 |
| `tool/validator.go` | `tool` | `ValidatePath` | 当前正式服务代码没有调用 `ValidatePath`。 |
| `mcp/server_configuration.go` | `mcp` | `MCPServerConfigurationFile`、`MCPServerConfiguration` | `NewMCPServerManager` 调用内部 `loadMCPServerConfigurations`，读取 `config/mcp_servers.json`。 |
| `mcp/protocol_messages.go` | `mcp` | `MCPProtocolMessageTemplates` | `NewMCPServerManager` 读取 `messages.json`；每次 initialize、tools/list、tools/call 和取消请求都调用 `copyProtocolMessageJSON` 拷贝一份消息再填本次数据。 |
| `mcp/json_rpc_types.go` | `mcp` | `MCPJSONRPCMessage`、`MCPInitializeResult`、`MCPServerToolDefinition`、`MCPToolListResult`、`MCPToolCallResult` | 标准输出读取函数使用这些类型解包 MCP Server 返回的 JSON。 |
| `mcp/started_server_process.go` | `mcp` | 无包外导出类型 | `startMCPServerProcess` 启动配置中的命令并取得三个管道；一个 goroutine 读取标准输出并按请求 id 返回结果，另一个 goroutine 排空标准错误。 |
| `mcp/server_manager.go` | `mcp` | `MCPServerManager`、`MCPServerStatus`、`NewMCPServerManager`；方法：`ListConfiguredMCPServers`、`StartSelectedMCPServers`、`CloseAllStartedMCPServers` | `main.go` 创建并调用。内部依次启动进程、initialize、发送 initialized notification、tools/list、注册工具；停止未选中的 Server。MCPServerManager 不保存 Agent 工具注册表。 |
| `mcp/server_tools.go` | `mcp` | 无包外导出函数 | `registerMCPServerTools` 遍历返回工具并注册 `mcp_<server>__<tool>`；保存的执行函数调用 `callMCPServerTool`，后者发送 tools/call 并把文字结果返回 `Registry.Execute`。 |
| `mcp/errors.go` | `mcp` | `ErrorKind`、`Error`、`NewError` | MCP 配置、进程、初始化、工具列表、超时和返回 JSON 错误；`main.go` 的 `publicError` 读取错误类别。 |

`service.Run` 的固定说明：`service` 是包名，来自 `service/agent.go` 的 `package service`。`Run` 是该文件定义的包级函数。`agent.go` 是文件名，不是包名；`Run` 不是接口方法。

## 版本状态

- v13 RAG 已跳过且未实现，归档位于 [`openspec/changes/archive/2026-07-19-v13-local-rag-retrieval/`](openspec/changes/archive/2026-07-19-v13-local-rag-retrieval/)。当前正式代码中不存在 `rag` 包或 `search_local_documents` 工具。
- v14 已完成：核心 Go 代码、本地完整 HTTP 测试、真实 Playwright MCP 和真实 DeepSeek 双 SubAgent 检查均已通过。
- v15 第一部分已实现：`run_subagent` 后台返回、完成回调、同会话主 Agent执行锁、独立会话事件 SSE 和 Web `EventSource`。取消、重试、checkpoint 和服务重启恢复尚未实现。
