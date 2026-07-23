# cc-agent-go Agent 工程化学习路线

## 方向

v0–v10 已经完成 Agent 基础能力：HTTP、SSE、模型调用、工具循环、会话持久化、上下文压缩、Skill 和多 Agent 辩论。

主线从 v11 开始停止扩展游戏功能，优先学习求职面试中能直接展示的 Agent 开发能力。

## v11：结构化日志与错误分类

目标：让前端收到稳定、安全的错误码，让 Codex 可以直接分析 Go 服务输出的 JSON 日志。

- 使用 `log/slog` 向 stderr 输出 JSON 日志
- 将配置、网络、超时、DeepSeek 鉴权、限流、Provider、存储和 Agent 轮数错误分类
- 普通 HTTP 接口返回统一 JSON 错误，SSE 返回统一 `error` 事件
- 工具错误继续作为 `tool_result` 交给下一次 LLM 调用
- API Key、用户正文、模型回复和工具参数不得进入日志

## v12：可配置的 MCP Client 与动态工具注册

目标：网页选择外部 MCP Server 后，Go 启动对应进程、读取工具并注册到现有 Agent 工具表。

- 使用 JSON 配置文件维护 MCP Server 命令和参数，增加 Server 不修改 Go 代码
- 从独立 `messages.json` 读取 MCP 2025-11-25 标准消息
- 实现 STDIO、JSON-RPC 请求编号、initialize、notifications/initialized、tools/list 和 tools/call
- 将 MCP 工具定义和执行函数一起注册到现有 `tool.Registry`
- 网页显示 Server 运行状态、工具数量和工具名称
- 第一台 Server 使用 Playwright MCP，关闭图片结果，只读取网页文字和控件信息

## v13：RAG 与资料检索

状态：已跳过，未实现。用户决定把学习时间优先用于通用型 SubAgent。

- 归档计划：[`openspec/changes/archive/2026-07-19-v13-local-rag-retrieval/`](openspec/changes/archive/2026-07-19-v13-local-rag-retrieval/)
- 30 个实施任务均未执行，RAG spec 未同步到正式 `openspec/specs/`

## v14：多 Agent 任务分配与协作

目标：把通用型 SubAgent 注册成普通工具，让主 Agent 通过固定 JSON 一次传入多个独立任务，并收回固定 JSON 结果。

- 当前状态：✅ 已完成。核心 Go 实现、本地完整 HTTP 测试、真实 Playwright MCP 和真实 DeepSeek 双 SubAgent 检查均已通过
- 完整计划：[`openspec/changes/v14-general-subagent-tool/`](openspec/changes/v14-general-subagent-tool/)
- 注册普通工具 `run_subagent`，输入 JSON 的 `subAgentTasks` 数组保存 `taskId` 和 `task`
- 输出 JSON 的 `results` 数组保存每个任务的 `taskId`、`status`、`result` 和 `error`
- `config.Config.MaximumParallelSubAgents` 决定同时执行的 SubAgent 数量，默认 5，硬上限 5
- SubAgent 使用新的临时消息记录和现有 DeepSeek API
- SubAgent 可以使用执行时已有的 Bash、Skill、CreateSkill 和 MCP 工具
- 从 SubAgent 工具表删除 `run_subagent`，禁止继续创建 SubAgent
- 每个 SubAgent 最多执行 12 轮，全部结果组成一个 JSON `tool_result` 返回主 Agent
- 第一版不实现 router、固定职能、handoff、独立会话、后台任务或新网页

## v15：长任务、后台运行与恢复

目标：Agent 长任务可以在后台运行，并支持查询、取消和服务重启后继续。

- 为每次长任务生成 taskId
- 保存 Pending、Running、Completed、Failed、Cancelled 状态
- 支持后台运行、进度查询和取消
- 支持超时、有限次数重试和 checkpoint
- Go 服务重启后读取 checkpoint 并继续未完成任务

## v16：A2A 与远程 Agent 调用

目标：Go Agent 可以读取其他服务器的 Agent 信息，并向远程 Agent 发送任务。

- 发布和读取 Agent Card
- 实现 message/send、tasks/get、任务状态和产物传递
- 处理远程 Agent 的成功、失败、超时和取消结果

## 暂不优先

- 继续扩展狼人杀和更多人格 Demo
- 只靠修改 Prompt 追求偶然效果
- 以测试系统本身作为一个独立学习版本
- 把大量版本内容放在审批流程上
- 为追逐框架而同时学习多个 Agent 框架

## 参考规范

- [OpenAI Agents SDK](https://developers.openai.com/api/docs/guides/agents)
- [OpenAI Agent Evals](https://developers.openai.com/api/docs/guides/agent-evals)
- [MCP Architecture](https://modelcontextprotocol.io/docs/learn/architecture)
- [OWASP Top 10 for Agentic Applications 2026](https://genai.owasp.org/resource/owasp-top-10-for-agentic-applications-for-2026/)
- [Google Developer's Guide to AI Agent Protocols](https://developers.googleblog.com/en/developers-guide-to-ai-agent-protocols/)
