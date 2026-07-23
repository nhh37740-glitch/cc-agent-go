# cc-agent-go 项目索引

本文件记录正式 Go 服务的文件、包名、导出函数和主要调用位置。`democode/` 是历史教学程序，不写入本索引。

更新规则：修改根目录 `main.go`、`config/`、`model/`、`service/` 或 `tool/` 中的正式 Go 文件时，同一次修改更新对应条目。回答代码问题时，先读取本文件；本文件没有该项目时，才读取代码并补充条目。

| 文件 | 包名 | 导出类型和函数 | 调用关系 |
| --- | --- | --- | --- |
| `main.go` | `main` | `RunSubAgentToolInput`、`RunSubAgentToolOutput`、`main` | 注册 HTTP 路由和工具，配置 `slog` JSON 日志。`registerGeneralSubAgentTool` 注册 `run_subagent`；它的执行函数调用 `config.Load`、`decodeAndValidateRunSubAgentToolInput`、`CopyExcludingTools` 和 `service.RunSubAgentsInParallel`，再返回固定 JSON。`handleChat` 调用 `service.Run`；`handleChatStream` 调用 `service.RunStream`；`handleListMCPServers` 调用 `MCPServerManager.ListConfiguredMCPServers`；`handleSelectMCPServers` 调用 `StartSelectedMCPServers`；退出时调用 `CloseAllStartedMCPServers`。`publicError` 把 `service.AppError` 和 `mcp.Error` 转成 HTTP 状态。 |
| `config/config.go` | `config` | `Config`、`Load` | `Load` 读取 `DEEPSEEK_API_KEY` 和 `MAX_PARALLEL_SUBAGENTS`；并行数只接受 1–5，空值或非法值使用 5。API Key 空值由 `service.Chat` 和 `ChatStream` 返回 `config_error`。 |
| `model/types.go` | `model` | `ContentBlock`、`Message`、`ToolCall`、`ChatRequest`、`ChatResponse`、`ErrorResponse`、`ApiResponse`、`SessionJson`、`ConversationSummary`、`Compressor` | `main.go`、`service/` 和 `tool/` 读写这些类型的值。 |
| `service/agent.go` | `service` | `Run`、`RunStream`、`GenerateConversationId` | `main.go` 的 `handleChat` 调用 `Run`。`Run` 调用 `Chat`；API 错误返回 `main.go`，工具错误写入 `tool_result` 后继续下一轮，保存错误只写 ERROR 日志。 |
| `service/errors.go` | `service` | `ErrorKind`、`AppError`、`NewAppError` | `Chat`、`ChatStream` 和 `Run` 创建分类错误；`main.go` 使用 `errors.As` 读取 `Kind` 和 `ProviderStatus`。 |
| `service/client.go` | `service` | `Chat` | 向 DeepSeek 发送非流式请求；检查 API Key、网络错误、HTTP 状态和响应 JSON。 |
| `service/stream.go` | `service` | `ChatStream` | 向 DeepSeek 发送流式请求；检查 API Key、网络错误和 HTTP 状态，再读取 SSE 数据。 |
| `service/subagent.go` | `service` | `SubAgentTask`、`SubAgentResult`、`RunSubAgent`、`RunSubAgentsInParallel` | `RunSubAgent` 使用只包含本次任务的临时消息记录，执行最多 12 轮 DeepSeek 和工具调用，不读写会话文件。`RunSubAgentsInParallel` 为每个任务启动一个 goroutine，通过 channel 收取结果，再按输入位置返回。 |
| `service/store.go` | `service` | `Store`、`NewStore`、`LoadMemory`；`Store` 方法：`LoadMessages`、`LoadConversation`、`ListConversations`、`DeleteConversation`、`AppendTurn`、`AppendTurnWithCompression` | `main.go` 创建 `Store`；`Run` 和 `RunStream` 调用消息读取和保存方法；存储日志使用 `slog`。 |
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
