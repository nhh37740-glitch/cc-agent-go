## 1. 固定重构前的实际行为

- [x] 1.1 增加回归测试，记录当前 `service.Run`、`service.RunStream`、后台 SubAgent 回调分别如何读取完整会话、调用模型、执行工具和保存会话
- [x] 1.2 增加回归测试，记录当前 `tool.BashTool` 保存固定 `workspace`、MCP 工具和 `run_subagent` 使用共享工具表的实际行为
- [x] 1.3 增加回归测试，记录当前会话 JSON 路径、压缩阈值、累计 token 字段和压缩失败回退结果
- [x] 1.4 运行 `go test ./...`，保存重构前通过与预期失败测试的实际结果

## 2. 定义 Agent 每次执行收到的外部数据

- [x] 2.1 新增 `agent/execution_environment.go`，定义必填 `WorkingDirectory` 和 `ConversationID` 的 `AgentExecutionEnvironment`
- [x] 2.2 校验 `WorkingDirectory` 是存在的绝对目录，校验 `ConversationID` 只能生成当前项目目录内的会话文件
- [x] 2.3 新增 `agent/task_input.go`，分别定义 `UserTaskInput`、`InternalContinuationTaskInput` 和 `HostedAgentTaskInput`
- [x] 2.4 不在 `Agent` 字段中保存当前激活的工作目录或会话 ID；每次 `Agent.Run` 都由调用者明确传入
- [x] 2.5 增加测试，确认同一个 `Agent` 可以先后使用两个工作目录和两个会话 ID，数据不会互相混用

## 3. 建立按项目目录保存的会话记忆

- [x] 3.1 新增 `memory/conversation_store.go`，定义 `ProjectConversationStore`
- [x] 3.2 根据 `AgentExecutionEnvironment` 生成 `<WorkingDirectory>/.cc-agent/sessions/<ConversationID>.json`
- [x] 3.3 迁移会话 ID 校验、每会话锁、JSON 读取、JSON 保存、旧消息归档、会话列表和会话删除函数
- [x] 3.4 保留现有会话 JSON 中仍有用的标题、时间、消息和 token 字段；删除依赖固定 `workspace` 或固定全局会话目录的代码
- [x] 3.5 增加临时目录测试，确认两个项目目录中相同的会话 ID 会生成两个不同文件
- [x] 3.6 增加路径测试，确认会话 ID 不能让文件写到 `<WorkingDirectory>/.cc-agent/sessions/` 之外

## 4. 让工具使用本次 Agent 执行的工作目录

- [x] 4.1 新增 `tool/execution_environment.go`，定义包含必填 `WorkingDirectory` 的 `ToolExecutionEnvironment`
- [x] 4.2 修改 `tool.Tool.Execute` 和 `tool.Registry.Execute`，由 `Agent.Run` 把本次 `ToolExecutionEnvironment` 传给具体工具
- [x] 4.3 修改 `tool.NewBashTool()`，不再接收或保存固定目录；`BashTool.Execute` 每次用收到的 `WorkingDirectory` 设置 `cmd.Dir`
- [x] 4.4 修改本地文件工具、Skill 工具、动态函数工具和 `run_subagent` 工具，使它们明确接收本次执行目录或明确声明不使用目录
- [x] 4.5 修改 MCP 动态工具的执行函数签名，使新增 MCP Server 不需要在 Agent 内增加名称分支
- [x] 4.6 增加测试，确认同一个已注册 Bash 工具连续在两个工作目录执行时分别只看到对应目录的文件

## 5. 定义封装后的 Agent 公开类型

- [x] 5.1 新增 `agent/agent.go`，定义字段私有的 `Agent`
- [x] 5.2 新增 `agent/result.go`，定义正常完成、终止工具完成和达到最大轮数三种具体完成结果
- [x] 5.3 新增 `agent/events.go`，定义 round 开始、文字增量、工具开始、工具成功、工具失败、记忆压缩和 Agent 完成事件
- [x] 5.4 新增 `agent/model_call.go`，定义模型调用函数收到的消息、工具定义、最大输出 token 和返回结果
- [x] 5.5 实现 `Agent.Run(agentTaskInput, agentExecutionEnvironment)`，返回 `AgentRunResult` 和 `error`
- [x] 5.6 增加构造和输入校验测试，错误内容必须指出缺少的具体字段

## 6. 首次模型调用只发送当前任务和历史记录位置

- [x] 6.1 新增 `agent/memory_reference.go`，定义包含工作目录、会话文件路径和允许使用的读取命令的 `AgentMemoryReference`
- [x] 6.2 `Agent.Run` 开始时只读取项目级 `AGENTS.md` 等本次必须放入 system message 的规则，不把旧会话正文全部装入请求消息
- [x] 6.3 第一次模型调用的 system message 写入本次 `WorkingDirectory`、会话文件路径和按需使用 `rg`、`head`、`tail`、`cat` 读取历史记录的明确指令
- [x] 6.4 第一次模型调用的 user message 只放当前 `AgentTaskInput` 的具体任务内容
- [x] 6.5 增加模型请求捕获测试，确认旧会话正文没有自动进入第一次请求，但会话文件路径和当前任务已经进入请求
- [x] 6.6 增加工具测试，确认模型可以通过 Bash 工具在本次工作目录中读取指定会话文件

## 7. 实现唯一的思考、模型调用和工具执行循环

- [x] 7.1 在 `Agent.Run` 内建立只属于本次执行的消息、round、token 和工具调用状态
- [x] 7.2 每轮由 `Agent.Run` 取得当前工具定义并调用模型函数
- [x] 7.3 模型返回文字且没有工具调用时结束本次执行
- [x] 7.4 模型返回工具调用时，`Agent.Run` 按返回顺序调用 `tool.Registry.Execute`
- [x] 7.5 把本轮 assistant 工具调用消息和对应 `tool_result` 消息留在本次执行消息中，再开始下一轮模型调用
- [x] 7.6 工具失败时把规范化错误写入对应 `tool_result`，由下一轮模型调用处理
- [x] 7.7 达到最大 round 时返回具体的最大轮数结果，不在 `agent` 包生成 HTTP 状态码
- [x] 7.8 增加假的模型调用函数测试，确认完整模型—工具循环只在 `Agent.Run` 存在一份

## 8. 统一普通工具、MCP 工具和 SubAgent 工具

- [x] 8.1 `Agent.Run` 只通过 `tool.Registry` 获取定义和执行工具，不检查普通工具名称或 MCP Server 名称
- [x] 8.2 保留 MCP Server Manager 负责进程、协议消息、工具查询和 `tools/call`；它只把动态工具注册到 `tool.Registry`
- [x] 8.3 保留 `run_subagent` 作为一个已注册工具；Agent 内只保留“终止当前回复并等待后台结果”的明确工具结果类型
- [x] 8.4 SubAgent 创建自己的 `AgentExecutionEnvironment`，由调用者明确传入工作目录和独立会话 ID
- [x] 8.5 增加普通工具、动态 MCP 工具、`run_subagent`、工具成功和工具失败测试

## 9. 使用模型对应的 tokenizer 计算请求 token

- [x] 9.1 在 `go.mod` 固定 `github.com/amikos-tech/pure-tokenizers v0.1.5`，并把 `AGENTS.md` 的零第三方依赖规则改为只允许这个 tokenizer 依赖
- [x] 9.2 新增 `config/model_tokenizers.json`，为 `deepseek-v4-pro[1m]` 保存 tokenizer 类型、本地文件路径和上下文窗口大小
- [x] 9.3 从 `deepseek-ai/DeepSeek-V4-Pro` 的固定 revision 保存官方 `tokenizer.json`，记录来源、revision 和 SHA-256
- [x] 9.4 新增 `modeltoken/huggingface_json_token_counter.go`，调用 `tokenizers.FromFile` 实现 `HuggingFaceJSONTokenCounter`
- [x] 9.5 新增 `agent/token_counter.go`，定义 `CountPreparedModelRequest`、`CountText` 和 `TruncateText` 所需的明确接口
- [x] 9.6 服务启动时按当前模型名称读取配置并创建 token counter；文件或原生库加载失败时返回 tokenizer 配置错误，不启动 HTTP 服务
- [x] 9.7 在每次调用 DeepSeek 前计算这一次实际发送的 system message、当前执行消息、工具定义和预留输出 token
- [x] 9.8 DeepSeek 返回后读取 API usage，分别记录实际 input token 和 output token，不再只累计 output token
- [x] 9.9 使用中文、英文、JSON、特殊 token 和工具定义固定样本，把 Go 结果与官方 Hugging Face `AutoTokenizer` 结果逐项核对
- [x] 9.10 使用固定 DeepSeek API 请求比较本地预计算结果和 `usage.InputTokens`，记录服务端消息包装造成的实测差值，不写死未经测量的差值

## 10. 超过模型窗口前压缩本次执行内容

- [x] 10.1 根据模型上下文上限减去预留输出 token，计算本次请求允许使用的最大输入 token
- [x] 10.2 请求未超过上限时直接调用 DeepSeek
- [x] 10.3 请求超过上限时，先保留 system message、当前任务、尚未完成的工具调用和最近消息，再压缩更早的本次执行内容
- [x] 10.4 压缩后重新计算实际请求 token；仍超过上限时返回明确错误，不发送必然失败的请求
- [x] 10.5 用 tokenizer token 数替换当前工具结果的 8000 字符截断，并保留截断说明
- [x] 10.6 增加中文、英文、JSON、长工具结果和接近窗口上限的测试

## 11. 按会话 ID 保存和压缩历史记录

- [x] 11.1 本次 Agent 完成后，把当前任务、最终回复和需要保留的执行结果写入本次会话文件
- [x] 11.2 使用 tokenizer 计算保存后的会话文件内容 token，单独保存为 `storedMemoryTokens`
- [x] 11.3 `storedMemoryTokens` 未达到记忆阈值时直接保存，不调用压缩模型
- [x] 11.4 达到阈值时读取旧会话内容，调用无工具模型请求生成摘要，再保存摘要和最近记录
- [x] 11.5 压缩失败时保存未压缩的新记录并发出记忆压缩失败事件
- [x] 11.6 保存失败时保留 Agent 最终结果，返回具体记忆保存结果并写不含正文的日志
- [x] 11.7 增加普通保存、压缩保存、压缩失败回退、保存失败和服务重启后按会话 ID 恢复测试

## 12. 把现有 service 调用迁移到 Agent.Run

- [x] 12.1 修改非流式入口，创建 `UserTaskInput` 和 `AgentExecutionEnvironment` 后调用 `Agent.Run`
- [x] 12.2 修改流式入口，使用同一个 `Agent.Run`，把具体 Agent 事件编码成 SSE
- [x] 12.3 修改后台 SubAgent 完成回调，创建 `InternalContinuationTaskInput` 后调用同一个 `Agent.Run`
- [x] 12.4 修改 `service.RunSubAgent`，创建 `HostedAgentTaskInput` 和独立 `AgentExecutionEnvironment` 后调用同一个 `Agent.Run`
- [x] 12.5 删除 `service.Run`、`service.RunStream`、后台回调和 `service.RunSubAgent` 中重复的模型—工具循环
- [x] 12.6 删除固定 `workspace` 读取、自动加载完整会话和字符估算 token 的旧代码
- [x] 12.7 保持正式 HTTP 错误码、日志字段、SubAgent 部分结果和会话事件行为

## 13. 让 WebAgent 明确传入工作目录和会话 ID

- [x] 13.1 为 WebAgent 任务请求定义必填 `workingDirectory`、`conversationId` 和 `message`
- [x] 13.2 HTTP handler 解包后校验三个字段，再创建 `AgentExecutionEnvironment` 和 `UserTaskInput`
- [x] 13.3 WebAgent 页面增加工作目录输入、会话 ID 输入、任务输入和开始执行按钮
- [x] 13.4 页面显示 round、工具执行、SubAgent、记忆保存和最终结果事件，不恢复聊天机器人气泡和连续聊天输入
- [x] 13.5 浏览器使用两个工作目录和两个会话 ID 执行任务，确认后端读取和保存到各自项目目录

## 14. 让狼人杀、剧本杀和元老院只调用封装后的 Agent

- [x] 14.1 定义示例 Host 调用代码：主持人选择参与者、工作目录、角色会话 ID 和本轮任务
- [x] 14.2 Host 为每个角色调用同一个 `Agent.Run`，传入该角色的 `HostedAgentTaskInput` 和 `AgentExecutionEnvironment`
- [x] 14.3 Host 自己管理角色、回合、发言顺序和胜负；`agent` 包不增加狼人杀、剧本杀、元老院或 WebAgent 模式字段
- [x] 14.4 增加测试，确认同一个 Agent 类型能运行 WebAgent 任务和 Host 角色任务，且 Agent 内没有应用名称分支

## 15. 文档和完整验证

- [x] 15.1 更新 `PROJECT_INDEX.md`，记录实际完成的文件、函数、参数、执行顺序和返回值
- [x] 15.2 更新 `ROADMAP.md`、`README.md` 和 `AGENTS.md`，把固定 workspace 改为外部传入项目工作目录和会话 ID
- [x] 15.3 文档说明会话文件位于 `<WorkingDirectory>/.cc-agent/sessions/<ConversationID>.json`，第一次模型调用不会自动发送全部旧会话
- [x] 15.4 文档说明狼人杀、剧本杀、元老院和其他应用只能在 Agent 外部组织调用
- [x] 15.5 文档说明唯一第三方依赖、DeepSeek tokenizer 文件来源、首次原生库安装和本机缓存位置
- [x] 15.6 运行 `go fmt ./...`
- [x] 15.7 运行 `go build ./...`、`go vet ./...` 和 `go test ./...`
- [x] 15.8 启动服务，用真实 DeepSeek、Bash、Playwright MCP 和 SubAgent 完成 WebAgent 任务
- [x] 15.9 重启服务后使用相同工作目录和会话 ID，确认 Agent 能按需读取此前会话文件
- [x] 15.10 使用不同工作目录和相同会话 ID，确认两份历史记录完全分开
- [x] 15.11 检查 Git 差异不包含 API Key、用户项目数据、`.cc-agent/sessions/`、日志或无关文件
- [x] 15.12 运行 `openspec validate v15-unified-agent-execution-loop --strict --json`

## 16. 修正 WebAgent 项目会话、日志和 MCP 页面

- [x] 16.1 WebAgent 新任务允许 `conversationId` 为空；`handleChat` 和 `handleChatStream` 调用 `service.GenerateConversationId()` 创建实际 ID
- [x] 16.2 增加 handler 测试，确认新会话由 Go 创建 ID，已有会话继续使用原 ID
- [x] 16.3 页面删除会话 ID 输入，增加“新建会话”和当前实际会话 ID 显示
- [x] 16.4 页面调用 `GET /api/conversations?workingDirectory=...` 显示当前项目全部会话
- [x] 16.5 点击会话后调用 `GET /api/conversations/{id}?workingDirectory=...` 并显示 user、assistant 历史记录
- [x] 16.6 增加 `GET /api/logs?limit=...`，只返回 `logs/server.jsonl` 最近的有效脱敏 JSON 日志
- [x] 16.7 页面增加运行日志区域，显示 Agent SSE 事件和服务端结构化日志
- [x] 16.8 MCP 列表和启动请求检查 HTTP 状态，显示真实后端错误；服务恢复后可以重新刷新
- [x] 16.9 重新设计桌面和窄屏布局，保留工作目录、项目会话、历史、任务、日志、结果和 MCP Server
- [x] 16.10 更新 `PROJECT_INDEX.md`、`README.md`、`ROADMAP.md` 和 `AGENTS.md`
- [x] 16.11 运行格式化、测试、静态检查、编译和 OpenSpec 严格校验
- [x] 16.12 启动 Go 服务，在浏览器验证新会话编号、项目会话列表、历史加载、日志和 MCP Server 列表
