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

目标：让 Agent 从本地资料中找到实际原文，并在回答中说明使用了哪个文件的哪一部分。

- 当前状态：OpenSpec 计划已完成，Go 实现尚未开始
- 完整计划：[`openspec/changes/v13-local-rag-retrieval/`](openspec/changes/v13-local-rag-retrieval/)
- 从 `workspace/knowledge/` 读取 `.md` 和 `.txt`，保存每段的相对文件名、起始行、结束行和原文
- 使用 Go 标准库完成中英文搜索词处理和 BM25 初次排序
- 使用 DeepSeek 把最多 12 个候选结果重排；重排失败时继续返回 BM25 结果
- 把 `search_local_documents` 作为普通工具注册到现有 `tool.Registry`，不增加 HTTP 路由
- 工具结果返回实际原文和 `[source:<文件>:<起始行>-<结束行>]`，最终回答复制实际采用的来源编号
- 比较不同分段大小和检索数量对命中位置、耗时和回答结果的影响
- 不读取 PDF，不做 OCR、文件上传、embedding 或向量数据库

## v14：多 Agent 任务分配与协作

目标：把不同工作交给职责和工具明确的 Agent，并比较顺序执行和同时执行的结果。

- 实现 router、agent-as-tool 和 handoff
- 每个 Agent 使用明确的输入参数、工具列表、最大轮数和 token 限制
- 支持可以独立完成的子任务同时执行
- 比较单 Agent、顺序多 Agent 和同时执行多 Agent 的耗时与结果

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
