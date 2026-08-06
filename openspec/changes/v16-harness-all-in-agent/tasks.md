# v16 Harness 任务清单

## 1. 固定现有行为基线

- [x] 1.1 运行 `go build ./...`、`go vet ./...`、`go test ./...`，记录当前全部通过的实际结果
- [x] 1.2 增加回归测试，固定 `service.RunAgentTask` 的事件顺序、`host.RunParticipantTurn` 的传参和 `ConversationEventReceivers` 的扇出行为
- [x] 1.3 再次运行 `go test ./...`，确认基线全绿后再开始新代码

## 2. Harness 注册表与目录布局

- [x] 2.1 新增 `harness/paths.go`：计算 `.cc-agent/harness/agents.json`、会话 ID（`harness`、`harness-agent-<slug>`）和 slug 清洗（`[a-z0-9-_]`，空结果回退 `agent-<序号>`）
- [x] 2.2 新增 `harness/registry.go`：注册表类型、`sync.RWMutex`、整体落盘与启动加载；只记录运行元数据（名称、会话 ID、状态、最近错误、任务数和时间）；提供 forget 移除（保留会话文件）和容量上限检查
- [x] 2.3 增加测试：中文名称 slug、同名 upsert、重启后重载、两个项目目录互不干扰、满池错误包含当前名单、forget 释放名额且会话文件保留

## 3. `agent` 工具（三参数 + 非阻塞启动 + 完成队列）

- [x] 3.1 新增 `harness/agent_tool.go`：实现 `agent`/`request`/`forget` 三参数；校验缺失字段（报具体字段名）、按名字 upsert、`forget` 移除注册表记录
- [x] 3.2 非阻塞启动：upsert 后在后台 goroutine 调用 `service.RunAgentTask`（`HostedAgentTaskInput{Task: request}`、统一执行 prompt `harness/managed_agent_prompt.md`、基础工具表含当前全局 MCP 工具、`StreamText: true` + 事件经 harness 本地 switch 编码写入该 Agent 会话频道，agent 包零改动）；工具立即返回 `{agent, conversationId, status: "running"}`；后台结束后把状态与格式化结果写回注册表，并把记录推入完成队列
- [x] 3.3 新增完成队列：每项目目录一个 buffered channel + 一个消费 goroutine；后台 goroutine 完成时入队，消费者阻塞接收（休眠，零 CPU）、逐条标记已收取，再以 `InternalContinuationTaskInput` 经会话执行锁调用 Harness 自己（Harness 自调用事件用 harness 本地 switch 编码，扇出到 `harness` 频道）；自调用失败只写日志；Runtime 创建时补扫注册表，重启前未收取的结果直接入队
- [x] 3.4 编写工具描述与输入 Schema：只写清 Agent 能力（持久记忆、独立执行、可用工具）和池上限规则，不规定使用顺序
- [x] 3.5 测试：无 API 单元测试覆盖参数缺失报具体字段、满池错误内容、队列顺序与标记已收取、memory 工具读取与 pattern 过滤；真实 API 集成测试（`RUN_HARNESS_DEEPSEEK_INTEGRATION=1` 守卫，沿用 `RUN_DEEPSEEK_TOKEN_INTEGRATION` 模式）覆盖首次 request 创建并立即返回、同名复用记忆、一轮启动多个 Agent、完成后队列触发自调用、状态迁移
- [x] 3.7 新增 `memory` 工具：读取 Harness 自己的会话记忆（可选 `recentMessages`，默认 20；可选 `pattern`，用 Go `regexp` 过滤匹配消息），返回标题、消息数和匹配消息；增加读取与 pattern 过滤测试
- [x] 3.6 池容量：`config.Load` 增加 `MaximumHarnessAgents`（`MAX_HARNESS_AGENTS`，默认 11，非法值回退）；第 12 个名字返回包含上限、当前名单和补救引导的错误；增加满池、forget 后新建、forget 保留会话文件的测试

## 4. 实况注入与工具表组成

- [x] 4.1 `main.go` 在每次 Harness 对话请求时，把当前 Agent 名单、当前运行中的 MCP Server 及工具名称（或"无 MCP 资源"）、当前已创建的应用，以自然语言追加到 Harness system prompt
- [x] 4.2 被管理 Agent 的工具表固定为 bash、activate_skill、create_skill + 当前全局 MCP 工具，不做按 Agent 过滤
- [x] 4.3 增加测试：名单/MCP/应用三种注入文本在有与无两种情况下的内容；被管理 Agent 的工具表组成正确

## 5. Harness 装配与 HTTP

- [x] 5.1 新增 `harness/system_prompt.md`（编排职责、agent 与 memory 两个工具的能力说明（不规定用法顺序）、用自然语言引导 MCP 使用的原则、创建应用流程、名单上限（默认 11）与 forget/复用策略）与 `harness/managed_agent_prompt.md`（执行型 Agent 统一职责，最终回复须格式化：自己是谁、执行了什么任务、结果如何）；启动加载，缺失时 Harness 路由返回配置错误
- [x] 5.2 新增 `harness/harness.go`：装配 Harness（加载 prompt、建含 agent 与 memory 两个工具的注册表、封装对 `service.RunAgentTask` 的调用、启动完成检查循环）
- [x] 5.3 `main.go` 注册路由：`POST /api/harness/chat/stream`、`GET /api/harness/agents`、`GET /api/harness/agents/{name}/memory`、`GET /harness`；Agent 进度与 Harness 后台回复复用现有 `GET /api/conversations/{id}/events`
- [x] 5.4 用 `net/http/httptest` 测试：必填校验 400、名单接口、chat SSE 事件类型、检查循环路径

## 6. Harness Web 页面

- [x] 6.1 新增 `harness.html`：项目路径输入、对话区（POST SSE + 监听 `harness` 频道接收后台汇报）、Agent 列表面板（名称、状态、任务数）
- [x] 6.2 Agent 进度视图：点击列表项后用 `/api/conversations/{conversationId}/events` 显示实时事件（逐 token），用 `/api/harness/agents/{name}/memory` 显示记忆内容
- [x] 6.3 页面复用现有 Markdown 渲染与智能滚动；错误显示真实 HTTP 状态和后端 JSON
- [ ] 6.4 浏览器手动验证：发消息 → 后台启动 Agent → 列表状态变化 → 进度视图有事件 → 完成后 Harness 对话区收到汇报

## 7. 应用运行时（⏸ 暂缓：先完成 Harness 本体，后续再实现）

- [ ] 7.1 `host/app_config.go`：app.json 类型、加载与校验（flow 类型、参与者必填字段、会话 ID 约定）
- [ ] 7.2 `host/app_runner.go`：round_robin 运行器（规则 + 议题 + 发言记录 → `RunParticipantTurn`）、单运行互斥、`app_round`/`app_speech`/`app_completed`/`app_failed` 事件扇出到 `app-<slug>`
- [ ] 7.3 `main.go` 注册路由：`GET /api/harness/apps`、`GET /api/apps/{name}`、`POST /api/apps/{name}/run`、`GET /api/apps/{name}/events`、`GET /apps/{name}/`；slug 校验 + HasPrefix 路径安全
- [ ] 7.4 增加测试：app.json 校验错误命名缺失字段、运行顺序与事件顺序、并发 run 拒绝、路径穿越拒绝

## 8. 元老院端到端（⏸ 暂缓，依赖任务 7）

- [ ] 8.1 启动服务，对 Harness 发送「实现元老院」：编码 Agent 后台写出 `apps/council/app.json`（三位以上元老、角色提示词、rounds）和 `apps/council/index.html`，完成回调后 Harness 汇报页面地址
- [ ] 8.2 打开 `/apps/council/?workingDirectory=...`，输入议题运行：确认每位元老按顺序发言、事件流完整、结束后页面显示完成
- [ ] 8.3 再次运行新议题：确认元老能按需读取自己此前的会话记忆

## 9. 文档与完整验证

- [ ] 9.1 更新 `PROJECT_INDEX.md`：harness 包、agent 工具、memory 工具、完成检查循环、新路由和实际调用顺序；应用运行时部分待任务 7 完成后补充
- [ ] 9.2 更新 `ROADMAP.md`、`README.md`、`AGENTS.md`：v16 = Harness（ALL IN AGENT），A2A 顺延；记录目录布局与接口
- [ ] 9.3 运行 `go fmt ./...`、`go build ./...`、`go vet ./...`、`go test ./...`
- [ ] 9.4 检查 Git 差异不包含 API Key、用户项目数据、`.cc-agent/` 运行产物或无关文件
- [ ] 9.5 运行 `openspec validate v16-harness-all-in-agent --strict --json`
