# cc-agent-go 项目索引

本文件记录正式 Go 服务的文件、包名、导出函数和主要调用位置。`democode/` 是历史教学程序，不写入本索引。

更新规则：修改根目录 `main.go`、`config/`、`model/`、`service/` 或 `tool/` 中的正式 Go 文件时，同一次修改更新对应条目。回答代码问题时，先读取本文件；本文件没有该项目时，才读取代码并补充条目。

| 文件 | 包名 | 导出类型和函数 | 调用关系 |
| --- | --- | --- | --- |
| `main.go` | `main` | `main` | 注册 HTTP 路由和工具，配置 `slog` JSON 日志。`handleChat` 调用 `service.Run`；`handleChatStream` 调用 `service.RunStream`；`publicError` 使用 `errors.As` 把 `service.AppError` 转成 HTTP 状态和 `model.ErrorResponse`。 |
| `config/config.go` | `config` | `Config`、`Load` | `Load` 只读取 `DEEPSEEK_API_KEY`；空值由 `service.Chat` 和 `ChatStream` 返回 `config_error`。 |
| `model/types.go` | `model` | `ContentBlock`、`Message`、`ToolCall`、`ChatRequest`、`ChatResponse`、`ErrorResponse`、`ApiResponse`、`SessionJson`、`ConversationSummary`、`Compressor` | `main.go`、`service/` 和 `tool/` 读写这些类型的值。 |
| `service/agent.go` | `service` | `Run`、`RunStream`、`GenerateConversationId` | `main.go` 的 `handleChat` 调用 `Run`。`Run` 调用 `Chat`；API 错误返回 `main.go`，工具错误写入 `tool_result` 后继续下一轮，保存错误只写 ERROR 日志。 |
| `service/errors.go` | `service` | `ErrorKind`、`AppError`、`NewAppError` | `Chat`、`ChatStream` 和 `Run` 创建分类错误；`main.go` 使用 `errors.As` 读取 `Kind` 和 `ProviderStatus`。 |
| `service/client.go` | `service` | `Chat` | 向 DeepSeek 发送非流式请求；检查 API Key、网络错误、HTTP 状态和响应 JSON。 |
| `service/stream.go` | `service` | `ChatStream` | 向 DeepSeek 发送流式请求；检查 API Key、网络错误和 HTTP 状态，再读取 SSE 数据。 |
| `service/store.go` | `service` | `Store`、`NewStore`、`LoadMemory`；`Store` 方法：`LoadMessages`、`LoadConversation`、`ListConversations`、`DeleteConversation`、`AppendTurn`、`AppendTurnWithCompression` | `main.go` 创建 `Store`；`Run` 和 `RunStream` 调用消息读取和保存方法；存储日志使用 `slog`。 |
| `service/council.go` | `service` | `Speech`、`CouncilRequest`、`CouncilResponse`、`RunCouncil` | `main.go` 的 `handleCouncil` 调用 `RunCouncil`；`RunCouncil` 调用 `Chat`。 |
| `tool/tool.go` | `tool` | `Tool` | `BashTool`、`SkillTool` 和 `CreateSkillTool` 都实现 `Tool` 的 `Name`、`Description`、`InputSchema`、`Execute` 方法。 |
| `tool/registry.go` | `tool` | `Registry`、`NewRegistry` | `main.go` 创建并注册工具；`Run` 和 `RunStream` 调用 `GetDefinitions` 与 `Execute`。 |
| `tool/bash.go` | `tool` | `BashTool`、`NewBashTool` | `main.go` 注册 `BashTool`；`Registry.Execute` 调用它的 `Execute` 方法。 |
| `tool/skill.go` | `tool` | `SkillTool`、`NewSkillTool` | `main.go` 注册 `SkillTool`；`Registry.Execute` 调用它的 `Execute` 方法。 |
| `tool/create_skill.go` | `tool` | `CreateSkillTool`、`NewCreateSkillTool` | `main.go` 注册 `CreateSkillTool`；`Registry.Execute` 调用它的 `Execute` 方法。 |
| `tool/validator.go` | `tool` | `ValidatePath` | 当前正式服务代码没有调用 `ValidatePath`。 |

`service.Run` 的固定说明：`service` 是包名，来自 `service/agent.go` 的 `package service`。`Run` 是该文件定义的包级函数。`agent.go` 是文件名，不是包名；`Run` 不是接口方法。
