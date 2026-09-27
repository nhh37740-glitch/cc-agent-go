# cc-agent-go 项目索引

本文件只记录正式服务的实际文件、函数、参数和调用顺序。`democode/` 不写入
本索引。

## 一次 WebAgent 任务的实际顺序

1. `index.html` 发送 `workingDirectory`、当前选中会话的 `conversationId`
   （新会话为空字符串）和 `message`。
2. `main.handleChat` 或 `main.handleChatStream` 解包请求；新会话调用
   `service.GenerateConversationId()` 创建实际编号，然后校验目录、编号和任务。
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

## 一次 Harness 编排的实际顺序

1. `main.handleHarnessChatStream` 校验请求（message 必填、目录绝对存在，失败
   返回 400 JSON）→ 检查 `harnessPromptsLoadError` → 检查 Key →
   `harness.GetOrCreateRuntime(workingDirectory, dependencies)`。首次创建时
   创建文档树（harness/AGENTS.md、shared/**、residents/*/AGENTS.md）、加载
   `.cc-agent/harness/agents.json`、确保 4 个常驻 Agent 在注册表、构建含
   `agent`、`memory`、`docs` 的工具表、启动完成队列消费 goroutine，并补扫
   重启前未收取的结果直接入队。
2. Harness 模型的每次调用（用户对话或完成队列自调用）都由
   `Runtime.HarnessSystemPromptWithLiveStatus` 在 system prompt 后追加三段实况：
   Agent 资源（常驻 4 个不计容量、临时 Agent 容量/已用/可用、名单带类型）、
   运行中的 MCP Server 及工具名称（`main.harnessMCPLiveStatusText` 提供）、
   已创建的应用（扫描 `.cc-agent/apps/` 下含 app.json 的目录）。
3. Harness 决定派任务时，`agent.Agent.Run` 调用 `harness.managedAgentTool.Execute`：
   校验参数 → 常驻名不可 forget → `AgentRegistry.UpsertAgent` → 运行中同名拒绝
   → `MarkRunning` → 后台 goroutine 调 `service.RunAgentTask`
   （`HostedAgentTaskInput`、`buildManagedAgentSystemPrompt`（常驻注入身份路径，
   临时声明一次性）、现取的基础工具表、逐 token 事件扇出到该 Agent 频道）→
   立即返回 running。
4. 后台结束用 `buildManagedAgentFinalReport` 整理汇报（成功/失败都有正文），
   失败走 `MarkFailedWithReport`（保留汇报正文），写回 `MarkCompleted`/`MarkFailed`
   后记录推入 `completionQueue`。
5. 消费者 `MarkResultCollected` 后以 `InternalContinuationTaskInput` 经 `harness`
   会话锁调用同一个 `service.RunAgentTask`，汇报文字流式扇出到 `harness` 频道；
   自调用失败只写 `slog`。网页用 `GET /api/conversations/harness/events` 接收
   后台汇报，用 `GET /api/conversations/harness-agent-<slug>/events` 看某个
   Agent 的逐 token 进度。
6. 前端不再拼最近对话上下文；Harness 与常驻 Agent 按各自 AGENTS.md/memory/docs
   提示主动检索记忆。

## 正式文件

| 文件 | 具体类型或函数 | 实际调用关系 |
| --- | --- | --- |
| `agent/execution_environment.go` | `AgentExecutionEnvironment`、`Validate` | 每次 `Agent.Run` 收到 `WorkingDirectory` 和 `ConversationID`；校验绝对目录、目录存在和安全会话编号。`Agent` 字段不保存这两个值。 |
| `agent/task_input.go` | `UserTaskInput`、`InternalContinuationTaskInput`、`HostedAgentTaskInput` | WebAgent、后台结果回调和外部 Host 分别创建不同类型；三个类型都向 `Agent.Run` 提供本次任务文字。 |
| `agent/agent.go` | `AgentConfiguration`、`Agent`、`NewAgent`、`Agent.Run` | `Run` 中保存唯一循环：准备请求 → 精确计数 → DeepSeek → 工具 → 下一轮或完成 → 保存项目会话。它只使用 `tool.Registry`，不检查 MCP Server 名称或普通工具名称。 |
| `agent/model_call.go` | `AgentModelCallRequest`、`AgentModelCallFunction` | `service.RunAgentTask` 提供具体 DeepSeek 调用函数；`Agent.Run` 只调用该函数。 |
| `agent/result.go` | 三种完成结果和两种记忆保存结果 | 分别表达正常完成、终止工具完成、达到最大轮数，以及记忆保存成功或失败。 |
| `agent/events.go` | round、文字、工具、压缩、保存和完成事件 | `Agent.Run` 发送具体事件；`main.writeAgentEventSSE` 按具体事件类型编码 JSON。 |
| `agent/memory_reference.go` | `AgentMemoryReference`、`loadRecentConversation` | 生成工作目录、`.cc-agent/sessions/<id>.json`、`AGENTS.md` 和允许读取命令的 system instruction；按 `KeepRecentMemoryTokens` 预算从会话文件装配「历史摘要 + 最近对话」到请求开头（摘要固定保留，预算外靠工具现读）。 |
| `agent/token_counter.go` | `PreparedModelRequest`、`AgentTokenCounter`、`TokenTruncationResult` | `Agent.Run` 在每次模型调用前计数请求，并用相同 tokenizer 限制工具结果和历史文件。 |
| `memory/conversation_store.go` | `ProjectConversationStore` | 每个读取、保存、列表和删除函数都收到工作目录；同名会话在不同项目生成不同文件。内部锁按完整会话文件路径区分。 |
| `modeltoken/huggingface_json_token_counter.go` | `HuggingFaceJSONTokenCounter` | 启动时调用 `tokenizers.FromFile` 一次；实现请求计数、文字计数和按 token 截断。 |
| `config/model_tokenizers.go` | `ModelTokenizerConfiguration`、`LoadModelTokenizerConfiguration` | 按 `Config.Model` 读取 `config/model_tokenizers.json`，返回 tokenizer 文件和上下文窗口。 |
| `config/config.go` | `Config`、`Load` | 读取 DeepSeek Key、并行 SubAgent 数、SubAgent 最大轮数和 Harness Agent 池容量（`MAX_HARNESS_AGENTS`，默认 11）；不再提供固定 workspace 或固定 sessions 目录。 |
| `model/types.go` | 三种消息内容类型、`ChatRequest`、`SessionJson` | `ChatRequest` 的三个必填 JSON 字段是 `workingDirectory`、`conversationId`、`message`；`SessionJson.StoredMemoryTokens` 保存项目会话文件计数。 |
| `tool/execution_environment.go` | `ToolExecutionEnvironment` | `Agent.Run` 把本次工作目录和会话编号传给 `Registry.Execute`。 |
| `tool/tool.go` | `Tool` | 每个具体工具的 `Execute` 都收到工具参数和本次 `ToolExecutionEnvironment`。 |
| `tool/registry.go` | `Registry`、`RegisterFunctionTool`、`RegisterTerminalFunctionTool`、`Execute` | 保存普通工具、MCP 动态工具和 `run_subagent`；终止行为是注册信息，不是 `Agent.Run` 中的工具名称判断。 |
| `tool/bash.go`、`tool/command_portable.go` | `NewNativeCommandTool()` / `command` | Windows 使用白名单 `.exe`，Linux 使用独立白名单程序；`program`+`args` 直接执行，返回结构化 `status/exit_code/output`，支持会话 `Context` 取消。 |
| `tool/file_crud_tool.go` | `NewFileTool()` / `file` | 工作目录内文件增删改查，不依赖 shell。 |
| `service/conversation_runs.go` | `ConversationRunRegistry` | 按会话登记 cancel；`POST /api/conversations/{id}/stop` 与 HTTP 断开都会取消当前 Run。 |
| `tool/skill.go` | `NewSkillTool()`、`SkillTool.Execute` | 读取本次项目的 `.cc-agent/skills/<skill>.md`。 |
| `tool/create_skill.go` | `NewCreateSkillTool()`、`CreateSkillTool.Execute` | 写入本次项目的 `.cc-agent/skills/<name>.md`。 |
| `service/agent_runner.go` | `AgentRunOptions`、`RunAgentTask` | 把 DeepSeek `Chat`/`ChatStream` 适配成 `AgentModelCallFunction`，然后只调用 `Agent.Run`。 |
| `service/subagent.go` | `RunSubAgent`、`RunSubAgentsInParallel`、`RunSubAgentsInBackground` | `RunSubAgent` 创建 `HostedAgentTaskInput` 和独立 `AgentExecutionEnvironment` 后调用 `RunAgentTask`；这里不再保存第二份模型—工具循环。 |
| `service/client.go` / `service/stream.go` | `Chat`、`ChatStream` | 只处理 DeepSeek HTTP 请求和响应，不管理 Agent round 或工具。 |
| `main.go` | HTTP handlers、`handleHarnessChatStream`、`handleHarnessAgents`、`handleHarnessAgentMemory`、`harnessMCPLiveStatusText`、`buildHarnessRuntimeDependencies`、`assignWebAgentConversationID`、`handleListRecentApplicationLogs`、SubAgent 工具注册、`main` | 启动时加载 tokenizer；WebAgent 新会话由 handler 创建编号，已有会话沿用前端选中的编号；会话接口按 `workingDirectory` 列出和读取项目历史；日志接口读取 `logs/server.jsonl` 最近的有效 JSON；后台回调创建 `InternalContinuationTaskInput` 并调用同一个 `RunAgentTask`；启动时加载 Harness 两个 prompt（缺失时 Harness 路由返回配置错误但不退出），Harness 路由校验先于 SSE 头（参数错误返回真实 400）。 |
| `mcp/server_tools.go` | MCP 动态工具注册和 `tools/call` | 注册的执行函数接受 `ToolExecutionEnvironment`，但浏览器 MCP 不读取本地目录；增加 MCP Server 不修改 `Agent.Run`。 |
| `host/participant_host.go` | `ParticipantTurn`、`RunParticipantTurn` | 外部主持人选择角色、项目目录、角色会话编号和当前任务，再调用同一个 `Agent.Run`。 |
| `harness/paths.go` | `SlugForAgentName`、`ManagedAgentConversationID`、`AgentRegistryFilePath`、`SharedDirectoryPath`、`ResidentDirectoryPath` | slug 清洗（中文临时名回退 `agent-<序号>`）、`harness-agent-<slug>` 会话编号、注册表/共享/常驻路径。 |
| `harness/registry.go` | `AgentRegistry`、`ManagedAgentRecord`（含 `Kind`、`CurrentTask`、`LastHeartbeatAt`）、`UpsertAgent`、`ForgetAgent`、`EnsurePermanentResidents`、`MarkRunning/Completed/Failed/MarkFailedWithReport`、`SetCurrentTask`、`HeartbeatAgent`、`CollectFinishedResults`、`MarkResultCollected` | 每个项目目录一份 `agents.json`；只存运行元数据；4 个常驻固定占名额且不可 forget；容量上限只约束临时 Agent；心跳与当前任务供主管理判断 Agent 是否仍在工作。 |
| `harness/residents.go` | `PermanentResidents`、`ResidentByName`、`ClassifyAgentName` | 4 个常驻（编码员/调研员/审查员/运维员）固定名字与 slug；临时 Agent 自动归类。 |
| `harness/workspace.go` | `EnsureHarnessWorkspace`、默认 AGENTS.md/shared 模板 | 首次创建文档树：harness/AGENTS.md、shared/AGENTS.md、shared/memory.md、shared/docs/、residents/*/AGENTS.md 与 docs/。 |
| `harness/runtime.go` | `Runtime`、`RuntimeDependencies`、`GetOrCreateRuntime`、`runManagedAgent`、`buildManagedAgentSystemPrompt`、`buildManagedAgentFinalReport`、`consumeFinishedAgents`、`HarnessSystemPromptWithLiveStatus` | 每个项目目录一套运行时；完成队列是 buffered channel，单消费者阻塞接收；依赖全部在创建时注入；失败/无正文也强制生成汇报；被管理 Agent 每轮循环打心跳；实况注入含当前任务与心跳状态（正常/超时）。 |
| `harness/agent_tool.go` | `managedAgentTool`（`agent`） | 三参数 `agent`/`request`/`forget`；非阻塞启动被管理 Agent；运行中同名拒绝；常驻名不可 forget。 |
| `harness/memory_tool.go` | `harnessMemoryTool`（`memory`） | 读取 Harness 自己的会话记忆；`recentMessages`（默认 20）与 Go 正则 `pattern` 过滤。 |
| `harness/docs_tool.go` | `harnessDocsTool`（`docs`） | 主管理受限读：仅 harness/AGENTS.md、shared/**、residents/*/AGENTS.md；禁止专属 docs 与会话文件，防上下文爆炸。 |
| `harness/agent_events_json.go` | `AgentEventJSONFields` | 事件→JSON 的 harness 本地 switch，字段名与 `main.writeAgentEventSSE` 对齐；扇出到 `ConversationEventReceivers`。 |
| `harness/harness.go` | `Prompts`、`LoadPrompts`、`BuildRuntimeDependencies` | 启动时加载 `harness/system_prompt.md` 与 `harness/managed_agent_prompt.md`；缺失时 Harness 路由返回配置错误。 |
| `harness/system_prompt.md` / `harness/managed_agent_prompt.md` | 编排者 prompt / 执行型 Agent 统一 prompt | 常驻/临时分类、文档布局、主管理严格可读范围、docs 工具、派工必填字段；执行型 Agent 强制最终汇报（成功/失败都必须有正文）。 |
| `index.html` | WebAgent 项目页面 | 用户输入项目目录后，页面调用会话列表接口；点击会话后加载历史；新会话编号由 Go 创建；中间显示历史和当前结果；右侧显示 Agent 事件及服务端 JSON 日志；左侧管理 MCP Server，并显示实际 HTTP 错误。顶栏与 `/harness`、`/council` 互链。 |
| `harness.html` | Harness 编排页面 | 三栏：Agent 名单（3 秒轮询名称/状态/任务数）、对话区（POST SSE + EventSource 监听 `harness` 频道接收后台汇报，并可加载/切换 harness 会话历史、新建会话）、Agent 详情（EventSource 逐 token 进度 + memory 接口记忆）。Markdown 渲染与智能滚动复用 index.html 实现；错误显示真实 HTTP 状态和后端 JSON。 |

## 当前版本

- v15：✅ `v15-unified-agent-execution-loop` 已实现。
- v16：⏳ `v16-harness-all-in-agent` 进行中——任务 1-6 主体已完成（基线、注册表、
  agent 工具 + 完成队列 + memory 工具、实况注入、prompt + 装配 + HTTP 路由、
  Harness 网页含会话历史）；真实 API 端到端已验证（HTTP 全链路 + WebAgent/元老院共存）。
  待办：6.4 浏览器手动验证、任务 7-8 应用运行时与元老院端到端、任务 9 文档收尾与完整验证。
