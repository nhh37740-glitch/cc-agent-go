# cc-agent-go 项目索引

本文件只记录正式服务的实际文件、函数、参数和调用顺序。`democode/` 不写入
本索引。

## 一次 WebAgent 任务的实际顺序

1. `index.html` 发送 `workingDirectory`、`conversationId` 和 `message`。
2. `main.handleChat` 或 `main.handleChatStream` 解包并校验这三个值。
3. handler 创建 `agent.UserTaskInput` 和 `agent.AgentExecutionEnvironment`。
4. handler 调用 `service.RunAgentTask(...)`。
5. `service.RunAgentTask` 把 `service.Chat` 或 `service.ChatStream` 放入
   `agent.AgentModelCallFunction`，创建 `agent.Agent`。
6. `agent.Agent.Run(agentTaskInput, executionEnvironment)` 准备当前任务、
   项目 `AGENTS.md` 和会话文件位置，计算请求 token，然后调用 DeepSeek。
7. DeepSeek 返回工具调用时，`Agent.Run` 调用
   `tool.Registry.Execute(toolName, toolArguments, toolExecutionEnvironment)`。
8. 没有工具调用时，`Agent.Run` 把当前任务和最终回复保存到
   `<WorkingDirectory>/.cc-agent/sessions/<ConversationID>.json`，返回具体
   `AgentRunResult`。

## 正式文件

| 文件 | 具体类型或函数 | 实际调用关系 |
| --- | --- | --- |
| `agent/execution_environment.go` | `AgentExecutionEnvironment`、`Validate` | 每次 `Agent.Run` 收到 `WorkingDirectory` 和 `ConversationID`；校验绝对目录、目录存在和安全会话编号。`Agent` 字段不保存这两个值。 |
| `agent/task_input.go` | `UserTaskInput`、`InternalContinuationTaskInput`、`HostedAgentTaskInput` | WebAgent、后台结果回调和外部 Host 分别创建不同类型；三个类型都向 `Agent.Run` 提供本次任务文字。 |
| `agent/agent.go` | `AgentConfiguration`、`Agent`、`NewAgent`、`Agent.Run` | `Run` 中保存唯一循环：准备请求 → 精确计数 → DeepSeek → 工具 → 下一轮或完成 → 保存项目会话。它只使用 `tool.Registry`，不检查 MCP Server 名称或普通工具名称。 |
| `agent/model_call.go` | `AgentModelCallRequest`、`AgentModelCallFunction` | `service.RunAgentTask` 提供具体 DeepSeek 调用函数；`Agent.Run` 只调用该函数。 |
| `agent/result.go` | 三种完成结果和两种记忆保存结果 | 分别表达正常完成、终止工具完成、达到最大轮数，以及记忆保存成功或失败。 |
| `agent/events.go` | round、文字、工具、压缩、保存和完成事件 | `Agent.Run` 发送具体事件；`main.writeAgentEventSSE` 按具体事件类型编码 JSON。 |
| `agent/memory_reference.go` | `AgentMemoryReference` | 生成工作目录、`.cc-agent/sessions/<id>.json`、`AGENTS.md` 和允许读取命令的 system instruction。 |
| `agent/token_counter.go` | `PreparedModelRequest`、`AgentTokenCounter`、`TokenTruncationResult` | `Agent.Run` 在每次模型调用前计数请求，并用相同 tokenizer 限制工具结果和历史文件。 |
| `memory/conversation_store.go` | `ProjectConversationStore` | 每个读取、保存、列表和删除函数都收到工作目录；同名会话在不同项目生成不同文件。内部锁按完整会话文件路径区分。 |
| `modeltoken/huggingface_json_token_counter.go` | `HuggingFaceJSONTokenCounter` | 启动时调用 `tokenizers.FromFile` 一次；实现请求计数、文字计数和按 token 截断。 |
| `config/model_tokenizers.go` | `ModelTokenizerConfiguration`、`LoadModelTokenizerConfiguration` | 按 `Config.Model` 读取 `config/model_tokenizers.json`，返回 tokenizer 文件和上下文窗口。 |
| `config/config.go` | `Config`、`Load` | 读取 DeepSeek Key、并行 SubAgent 数和 SubAgent 最大轮数；不再提供固定 workspace 或固定 sessions 目录。 |
| `model/types.go` | 三种消息内容类型、`ChatRequest`、`SessionJson` | `ChatRequest` 的三个必填 JSON 字段是 `workingDirectory`、`conversationId`、`message`；`SessionJson.StoredMemoryTokens` 保存项目会话文件计数。 |
| `tool/execution_environment.go` | `ToolExecutionEnvironment` | `Agent.Run` 把本次工作目录和会话编号传给 `Registry.Execute`。 |
| `tool/tool.go` | `Tool` | 每个具体工具的 `Execute` 都收到工具参数和本次 `ToolExecutionEnvironment`。 |
| `tool/registry.go` | `Registry`、`RegisterFunctionTool`、`RegisterTerminalFunctionTool`、`Execute` | 保存普通工具、MCP 动态工具和 `run_subagent`；终止行为是注册信息，不是 `Agent.Run` 中的工具名称判断。 |
| `tool/bash.go` | `NewBashTool()`、`BashTool.Execute` | `BashTool` 不保存目录；每次用 `ToolExecutionEnvironment.WorkingDirectory` 设置 `cmd.Dir`。 |
| `tool/skill.go` | `NewSkillTool()`、`SkillTool.Execute` | 读取本次项目的 `.cc-agent/skills/<skill>.md`。 |
| `tool/create_skill.go` | `NewCreateSkillTool()`、`CreateSkillTool.Execute` | 写入本次项目的 `.cc-agent/skills/<name>.md`。 |
| `service/agent_runner.go` | `AgentRunOptions`、`RunAgentTask` | 把 DeepSeek `Chat`/`ChatStream` 适配成 `AgentModelCallFunction`，然后只调用 `Agent.Run`。 |
| `service/subagent.go` | `RunSubAgent`、`RunSubAgentsInParallel`、`RunSubAgentsInBackground` | `RunSubAgent` 创建 `HostedAgentTaskInput` 和独立 `AgentExecutionEnvironment` 后调用 `RunAgentTask`；这里不再保存第二份模型—工具循环。 |
| `service/client.go` / `service/stream.go` | `Chat`、`ChatStream` | 只处理 DeepSeek HTTP 请求和响应，不管理 Agent round 或工具。 |
| `main.go` | HTTP handlers、SubAgent 工具注册、`main` | 启动时加载 tokenizer；WebAgent handler 传入项目目录和会话编号；后台回调创建 `InternalContinuationTaskInput` 并调用同一个 `RunAgentTask`。 |
| `mcp/server_tools.go` | MCP 动态工具注册和 `tools/call` | 注册的执行函数接受 `ToolExecutionEnvironment`，但浏览器 MCP 不读取本地目录；增加 MCP Server 不修改 `Agent.Run`。 |
| `host/participant_host.go` | `ParticipantTurn`、`RunParticipantTurn` | 外部主持人选择角色、项目目录、角色会话编号和当前任务，再调用同一个 `Agent.Run`。 |
| `index.html` | WebAgent 任务页面 | 显示项目目录、会话编号、任务输入、开始按钮、Agent 事件、最终结果和 MCP Server 列表；没有聊天气泡和历史会话卡片。 |

## 当前版本

- v15：✅ `v15-unified-agent-execution-loop` 已实现。
- v16：下一步，A2A 与远程 Agent 调用。
