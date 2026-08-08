# v16：Harness —— ALL IN AGENT

> 版本定义变更说明：AGENTS.md 版本表中 v16 原为「Agent Card、A2A 任务协议和远程 Agent 调用」。
> 经用户确认，v16 重新定义为 Harness（ALL IN AGENT 编排层）；A2A 顺延到后续版本。

## Why

v15 已把 Agent 封装为外部程序可调用的执行单元（`agent.Agent.Run`），设计哲学是尽最大可能
ALL IN LLM：模型自己决定思考、工具和记忆读取。但当前服务仍然把「用户直接驱动一个 Agent」
当作唯一用法：狼人杀、剧本杀、元老院想要复用 Agent，就必须由 Go 开发者手写 Host 代码。

Harness 的设计哲学是 ALL IN AGENT：编排者本身也是一个 Agent，但它有且只有一个工具
`agent`——它通过创建、驱动、检查其他 Agent 来完成用户任务。编排能力写在 system prompt
和 `agent` 工具的描述与使用说明里，而不是写死在 Go 分支里。这样「实现元老院」这类需求
不再需要修改 Go 代码：Harness 启动一个编码 Agent 写出应用配置和页面，应用运行时再由
每个参与者各自的 Agent 完成任务。

Harness 的关键职责只有两件：管理好 agents（注册表、状态、MCP 资源分配），管理好各个
agents 的记忆（每个 Agent 独立的会话文件，可检查、可回顾）。

## What Changes

- 新增 `harness/` 包。Harness 本身复用 `agent.Agent`：它的工具注册表中有且只有一个
  `agent` 工具；不注册 `bash`、Skill、`run_subagent` 或 MCP 工具。
- 新增 `agent` 工具，参数只有三个：`agent`（名字）、`request`（交给这个 Agent 的
  完整自然语言内容）、`forget`（可选，为 true 时释放这个名字）。不写死动作枚举：
  名字不存在即创建，同一个名字永远是同一个 Agent；工具描述只写清 Agent 的能力
  （持久记忆、独立执行、可使用 bash/Skill/MCP 工具），怎么用由 Harness 自行决定。
  Go 只做三件事：校验参数、按名字 upsert 并同步执行、返回最终结果文字。
- Harness 持有两个工具：`agent`（驱动 Agent）和 `memory`（读取 Harness 自己的会话
  记忆——Harness 没有 bash，无法像普通 Agent 那样用命令读自己的会话文件，因此需要
  专门工具）。除此之外不注册任何工具。
- Harness 复用 `service.RunAgentTask`，其会话与所有 Agent 一样进行 token 统计和
  阈值记忆压缩。
- 新增 Harness Agent 注册表：`<workingDirectory>/.cc-agent/harness/agents.json`，只
  记录运行元数据：名称、会话 ID、状态、最近错误、任务数和时间。角色与职责不进
  注册表——它写在 Agent 第一次收到的 request 里，由 Agent 的会话记忆持久保存。
  Harness 自身的会话 ID 固定为 `harness`；被管理 Agent 的会话 ID 为
  `harness-agent-<名称slug>`，重启后记忆延续。
- Harness 可动态调整 Agent 数目：注册表容量上限默认 11 个（`MAX_HARNESS_AGENTS`
  环境变量可调，非法值回退默认）；达到上限时返回包含当前全部名单和引导信息的错误，
  Harness 用 `forget` 释放名额或复用现有 Agent。`forget` 只移除注册表记录，会话文件
  保留；同名 Agent 再次创建时记忆自动延续。
- 被管理 Agent 非阻塞执行：`agent` 工具 upsert 后在后台 goroutine 中复用
  `service.RunAgentTask`（统一执行 prompt + request 原文 + 基础工具表），工具立即
  返回「已启动」；执行事件按该 Agent 的会话 ID 写入现有 `ConversationEventReceivers`。
  Agent 的最终回复按提示词要求格式化（自己是谁、执行了什么任务、结果如何），结果
  文字写回注册表。Harness 可因此同时运行多个 Agent。
- 新增独立的完成检查循环：Harness 的「对话—工具执行循环」和「Agent 状态检查循环」
  相互独立。检查循环每 3 秒扫描注册表，发现执行完毕且结果未收取的 Agent 时，取出
  格式化结果并以 `InternalContinuationTaskInput` 调用 Harness 自己（同一把会话执行
  锁），由 Harness 决定如何汇报；不接线式完成回调。
- Harness 的每次对话请求把三段自然语言实况追加到 system prompt：当前 Agent 名单
  （名称、状态、任务数）、当前运行中的 MCP Server 及工具名称、当前已创建的应用。
  Harness 不需要 `list` 类动作即可掌握全局；引导 Agent 使用 MCP 资源也只用自然语言。
  被管理 Agent 的工具表 = 基础工具（bash、Skill、create_skill）+ 当前全局已注册的
  MCP 工具，Go 不做按 Agent 过滤。
- 新增 Harness Web 页面 `harness.html`：项目路径输入、与 Harness 的对话区、Agent 列表
  （名称、状态、任务数），点击进入单个 Agent 的进度视图（实时事件流 + 记忆内容）。
- 新增应用运行时：应用是 `<workingDirectory>/.cc-agent/apps/<app>/` 下的 `app.json` +
  静态页面。`host/` 包新增应用运行器，按配置的顺序为每个参与者调用
  `RunParticipantTurn`（每个参与者独立会话 ID、独立角色提示词），
  运行事件扇出到 `app-<slug>` 频道。应用配置和页面由 Harness 启动的编码 Agent 写出，
  Go 代码不出现任何应用名称。
- 新增 HTTP 接口：`POST /api/harness/chat/stream`（SSE）、`GET /api/harness/agents`、
  `GET /api/harness/agents/{name}/memory`、`GET /api/harness/apps`、
  `POST /api/apps/{name}/run`、`GET /api/apps/{name}/events`、`GET /harness` 页面和
  `GET /apps/{name}/` 应用静态页面。Agent 进度与 Harness 后台回复复用现有
  `GET /api/conversations/{id}/events`，不新增事件端点。
- 端到端验证用例：用户对 Harness 说「实现元老院」→ Harness 创建编码 Agent 写出
  `apps/council/app.json` 与页面 → 用户在生成的页面输入议题 → 应用运行器为每位元老
  调用独立 Agent 完成辩论。
- 非目标：不实现 A2A、远程 Agent、任务取消、checkpoint、服务重启恢复；不修改现有
  `service.RunCouncil` 与 `council.html`；不做 Harness 页面多主题；不做多用户与鉴权；
  不做嵌套编排（被管理 Agent 不持有 agent 工具）。

## Capabilities

### New Capabilities

- `harness-agent-orchestration`: Harness 作为只持有 `agent` 工具的编排 Agent，用
  自然语言请求非阻塞地驱动具名 Agent 完成用户任务，并通过独立的完成检查循环收取
  结果；它维护
  Agent 注册表与每个 Agent 的独立记忆，通过每次请求注入的实况名单掌握全局，并把
  每个 Agent 的执行事件按会话 ID 提供给 Web 页面实时查看。

- `harness-app-runtime`: 应用以项目目录中的 `app.json` 和静态页面声明；应用运行器按
  配置为每个参与者调用同一个 `Agent.Run`，并对外提供运行、事件和静态页面接口，使
  Harness 创建的应用（如元老院）以 agents 为后端运行。

### Modified Capabilities

无。

## Impact

- 新增 `harness/` 包及测试：Agent 注册表、`agent` 工具（upsert + forget）、`memory`
  工具（读取 Harness 自己的记忆）、完成检查循环、Harness 装配（system prompt 加载、
  双工具注册表、实况注入、事件扇出）、被管理 Agent 的统一执行 prompt（含格式化
  返回要求）。
- 新增 `harness/system_prompt.md` 与 `harness/managed_agent_prompt.md`：编排者的
  工作方式、agent 工具能力说明、名单上限与管理策略、创建应用流程；执行型 Agent 的
  统一职责说明。
- `tool/registry.go` 不变（不做按 Agent 的 MCP 过滤）。
- 修改 `host/`：新增应用配置类型与应用运行器及测试；`RunParticipantTurn` 不变。
- 修改 `main.go`：注册 Harness 与应用 HTTP 路由、装配 Harness 依赖（每次 Harness
  请求把当前 MCP Server 与工具名称注入 system prompt）、提供应用静态页面。
- 新增 `harness.html`：Harness Web 页面。
- 新增 `GET /api/harness/...` 与 `/api/apps/...` 接口；现有 WebAgent 接口保持不变。
- 更新 `PROJECT_INDEX.md`、`ROADMAP.md`、`README.md`、`AGENTS.md`：v16 定义为
  Harness，A2A 顺延；记录 Harness 目录布局与接口。
- `go.mod` 不增加任何新依赖（tokenizer 例外规则不变）。
