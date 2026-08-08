# v16 Harness 设计

## 1. 总体形态：Harness 是一个只有一件工具的 Agent

```
用户（harness.html）
  → POST /api/harness/chat/stream
  → service.RunAgentTask(UserTaskInput, env{dir, "harness"}, harnessPrompt,
                         只含 agent 工具的注册表, ...)
  → agent.Agent.Run
      ↕ 模型决定调用 agent 工具（唯一工具）
  → harness.agentTool.Execute(agent, request, forget)
      → forget=true：移除注册表记录（会话文件保留）
      → 否则 upsert 后立即返回「已启动」，后台 goroutine 执行：
          service.RunAgentTask(HostedAgentTaskInput,
                               env{dir, "harness-agent-<slug>"},
                               统一执行 prompt + request 原文, 基础工具表,
                               StreamText: true, ReceiveEvent → harness 本地
                                 switch 编码 → 该 Agent 自己的会话频道)
          结束后把状态与格式化结果写回注册表，并把记录推入完成队列

  完成队列（每项目目录一个 buffered channel + 一个消费 goroutine）：
      后台 goroutine 完成时入队；消费者阻塞接收（休眠，零 CPU）逐条取出
      → 标记已收取
      → RunAgentTask(InternalContinuationTaskInput, env{dir, "harness"}, ...)
        调用 Harness 自己（同一把会话锁），汇报内容写入 harness 频道
      → Runtime 创建时补扫注册表：重启前未收取的结果直接入队
```

- Harness 复用 `agent.Agent`，不新增第二份模型—工具循环。它的特殊性只体现在两点：
  system prompt（`harness/system_prompt.md`）和只含 `agent` 工具的工具表。
- `agent` 工具**不是**终止工具：模型收到工具结果后继续编排（再调 agent、或直接回复）。
- Harness 不注册 `run_subagent`；被管理 Agent 也不注册 `run_subagent`（v16 不允许嵌套
  编排，防止失控；编码 Agent 需要并行时由 Harness 自己分多个 task 串行下发）。

## 2. `agent` 工具：三个自然语言参数，不写死动作

被管理 Agent 已经封装完毕（`agent.Agent.Run` + 独立会话记忆），Harness 不需要也不应该
关心它的内部。因此工具输入不是动作枚举，而是最少参数：

| 字段 | 必填 | 含义 |
| --- | --- | --- |
| `agent` | 是 | Agent 名字。名字不存在即创建（upsert）；同一个名字永远是同一个 Agent、同一份记忆 |
| `request` | 是（`forget` 为 true 时除外） | 交给这个 Agent 的完整自然语言内容：第一次写清角色与职责，之后写当前任务 |
| `forget` | 否（默认 false） | 为 true 时从注册表移除该名字并释放名额；会话文件保留，同名重建时记忆延续 |

- 工具描述只写清 Agent 的能力（持久记忆、独立执行、可使用 bash/Skill/MCP 工具）和
  池上限规则；先给谁派什么任务、何时询问进展、何时 forget，全部由 Harness 自行决定。
- Go 的实现是确定性的：校验参数（缺失时报具体字段名）→ 按名字 upsert → 用统一执行
  prompt + request 原文同步调用 `service.RunAgentTask` → 返回最终文字并写回状态。
  没有第二个模型调用，没有自然语言解析。
- 池容量上限默认 11 个（`MAX_HARNESS_AGENTS` 环境变量可调）；创建第 12 个名字时返回
  包含当前全部名单和引导信息的错误（用 `forget` 释放或复用现有 Agent）。
- 非阻塞启动：工具在后台 goroutine 中执行 `service.RunAgentTask` 并立即返回
  `{agent, conversationId, status: "running"}`；Harness 可连续启动多个 Agent。
- 完成即入队：后台 goroutine 把状态（completed/failed）和格式化结果写回注册表后，
  把记录推入完成队列（buffered channel）。收取结果的是队列消费者（见第 4 节）。
- Agent 的最终回复由 `managed_agent_prompt.md` 要求格式化：自己是谁、执行了什么
  任务、结果如何（含关键文件与数值），供检查循环直接取用。

## 3. 注册表与记忆布局

```
<workingDirectory>/.cc-agent/
├── harness/agents.json            # 注册表（sync.RWMutex 保护，写时整体落盘）
├── sessions/harness.json          # Harness 自己的记忆
├── sessions/harness-agent-<slug>.json   # 每个被管理 Agent 的记忆
└── apps/<app>/app.json + 页面      # 应用（见第 5 节）
```

- `slug`：小写后仅保留 `[a-z0-9-_]`，其余字符折叠为 `-`；结果为空时用
  `agent-<创建序号>`。会话 ID 因此永远满足现有安全字符集。
- 注册表记录：`name`、`slug`、`conversationId`、`status`
  （idle/running/completed/failed）、`result`（格式化最终结果）、`resultCollected`、
  `lastError`、`taskCount`、`createdAt`、`updatedAt`。角色与职责不进注册表——它写在
  Agent 的第一次 request 里，由 Agent 的会话记忆持久保存。

## 4. MCP 自然语言引导与进度事件

- 每次 Harness 对话请求，`main.go` 把三段自然语言实况追加到 Harness system prompt：
  当前 Agent 名单（名称、状态、任务数，来自注册表）、当前运行中的 MCP Server 及
  工具名称、当前已创建的应用。Harness 因此不需要 `list` 类动作；引导 Agent 使用 MCP
  资源也只通过 `request` 文本。被管理 Agent 的工具表 = bash、activate_skill、
  create_skill + 当前全局已注册的 MCP 工具。
- 后台执行使用 `StreamText: true` 和事件接收器（调用处传参，agent 包零改动）：
  事件 JSON 用 harness 包本地的 switch 编码，写入现有 `ConversationEventReceivers`
  （键 = 该 Agent 的会话 ID），状态与结果写回注册表。进度页复用**现有**
  `GET /api/conversations/{id}/events`（EventSource）实时查看每一个 token，
  完整记忆用 `GET /api/harness/agents/{name}/memory` 读取；Harness 自己的汇报
  （用户对话与完成队列触发的自调用）都出现在 `GET /api/conversations/harness/events`。
- 完成队列：每个项目目录的 Runtime 持有一个 buffered channel 和一个消费 goroutine
  （首次 Harness 请求时创建）。后台 goroutine 完成时把记录入队；消费者阻塞接收
  （等同休眠，零 CPU），逐条标记已收取后以 `InternalContinuationTaskInput` 调用
  Harness 自己（经同一把会话执行锁，和用户对话互斥）；Harness 自调用的事件用
  harness 包本地的 switch 编码，扇出到 `harness` 频道。自调用失败时只写日志，
  结果仍留在注册表供人工查看。Runtime 创建时先补扫一次注册表，把重启前未收取的
  结果直接入队（channel 是内存队列，重启后靠注册表的 resultCollected 标志恢复）。
- Harness 持有第二个工具 `memory`：读取 Harness 自己的会话记忆（可选
  `recentMessages`，默认 20；可选 `pattern`，用 Go `regexp` 过滤匹配消息，效果等同
  rg 搜索但不依赖外部命令）。Harness 没有 bash，无法像普通 Agent 那样用 rg 等命令
  读取自己的会话文件，因此需要专门工具；被管理 Agent 有 bash，继续按 v15 记忆指引
  使用 rg/head/tail/cat 读自己的会话文件。
- Harness 复用 `service.RunAgentTask`，token 统计与阈值记忆压缩和 WebAgent 一致。

## 5. 应用运行时（apps）

- 应用 = `<项目>/.cc-agent/apps/<appSlug>/` 中的：
  - `app.json`：`name`、`displayName`、`description`、`webui`（页面文件名）、
    `flow`（v16 固定 `round_robin` + `rounds`）、`participants[]`（`name`、
    `displayName`、`rolePrompt`、`conversationId`、`maximumRounds`）。
    参与者会话 ID 约定 `app-<appSlug>-participant-<participantSlug>`。
  - 一个静态页面（由编码 Agent 编写），从 `location.search` 读取 `workingDirectory`。
- `host/` 新增 `app_config.go`（类型 + 加载 + 校验）和 `app_runner.go`：
  输入 `{topic}`；逐 round 逐参与者：任务 = 规则说明 + 议题 + 当前发言记录，
  调 `RunParticipantTurn`；产出 `app_round`/`app_speech`/`app_completed`/`app_failed`
  事件，扇出到 `app-<appSlug>` 频道。参与者提示词与工具表按 app.json 装配。
- 接口：`POST /api/apps/{name}/run`（后台 goroutine 运行，立即返回）、
  `GET /api/apps/{name}/events`、`GET /api/apps/{name}`（JSON 配置）、
  `GET /apps/{name}/?workingDirectory=`（静态页面）。路径全部经 slug 校验 +
  HasPrefix 检查。
- Go 代码不认识「元老院」：配置和页面都是 Harness 启动的编码 Agent 写出的数据。

## 6. Harness 自身装配

- `harness/system_prompt.md`：编排者职责、agent 与 memory 两个工具的能力说明
  （不写死用法）、池上限与 forget 策略、创建应用的步骤（派编码 Agent 写 app.json
  与页面 → 确认应用出现在实况注入中 → 告知用户页面地址）。启动时加载，缺失则拒绝
  启动 Harness 路由（配置错误）。
- `harness/managed_agent_prompt.md`：执行型 Agent 的统一职责，并要求最终回复格式化：
  自己是谁、执行了什么任务、结果如何（关键文件与数值）。
- Harness 会话 ID 固定 `harness`；其记忆同样受现有压缩与 token 统计管理。
- `config.Load` 增加 `MaximumHarnessAgents`（`MAX_HARNESS_AGENTS`，默认 11，非法值
  回退默认），作为注册表容量上限。
- 错误沿用 `service.AppError` 分类；`agent` 工具执行错误作为 tool_result 交给
  Harness 的下一轮模型调用。

## 7. 测试策略

- 真实 API 集成测试：沿用 `RUN_DEEPSEEK_TOKEN_INTEGRATION` 的环境变量守卫模式
  （设置 `RUN_HARNESS_DEEPSEEK_INTEGRATION=1` 才调用 DeepSeek），断言
  create→task→完成→队列自调用的注册表变化；参数校验、池上限、队列顺序、
  memory 工具为无 API 单元测试。
- 临时目录测试：agents.json 落盘/重载、slug 与路径安全、app.json 加载校验。
- `net/http/httptest`：harness chat SSE 首帧、agents 列表、app run/events 路由。
- 端到端（手动）：真实 DeepSeek 完成「实现元老院」全流程。

## 8. 明确不做

A2A；任务取消、checkpoint 与服务重启恢复；Harness 页面主题系统；
修改 `service.RunCouncil`；多用户与鉴权；嵌套编排（被管理 Agent 不持有 agent 工具）；
按 Agent 过滤 MCP 工具的 Go 机制（MCP 控制只走自然语言）；工具动作枚举与工具内的
自然语言解析路由（参数最少化，用法由 Harness 决定）。
