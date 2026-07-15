# cc-agent-go Agent 工程化学习路线

## 方向

v0–v10 已经完成 Agent 基础能力：HTTP、SSE、模型调用、工具循环、会话持久化、上下文压缩、Skill 和多 Agent 辩论。

主线从 v11 开始停止扩展游戏功能，转向求职更有价值的 Agent 工程化能力：可观察、可评测、可控制、可恢复、可互操作。

## v11：Agent Trace 与运行回放

目标：能用证据回答一次 Agent 运行中“谁在什么时候做了什么，为什么失败”。

- 为每次请求生成 `traceId`，使用 `context.Context` 贯穿 HTTP、Agent、模型和工具层
- 用 JSONL 记录 `run_start`、`model_call`、`tool_start`、`tool_end`、`run_end`、`run_error`
- 记录 conversationId、轮次、耗时、token、工具名、脱敏参数、错误类别
- 将错误分类为配置、网络、代理、鉴权、Provider、模型、工具和存储错误
- 提供 Trace 查询和回放能力
- API Key、Authorization header 和敏感正文不得进入日志

## v12：Agent Eval 与回归测试

目标：证明 Prompt、模型或工具改动没有让 Agent 行为退化。

- 建立版本化评测数据集
- 验证工具选择、参数、完成度、轮数、耗时和安全约束
- 使用模拟模型与工具实现确定性测试
- 支持历史 Trace 转换为回归用例
- 输出通过率和版本对比报告

## v13：Guardrails 与人工审批

目标：高风险副作用发生前必须经过规则或人工批准。

- 为工具定义只读、写入、危险三个权限等级
- 校验工具输入、输出和路径边界
- 写文件、删除、外部消息等敏感操作支持暂停与批准
- 持久化待审批状态，批准后从同一次运行恢复
- 使用幂等键避免重试造成重复副作用

## v14：MCP Server 与 Client

目标：让 cc-agent-go 的工具可被其他 Agent 发现，也能调用外部 MCP 服务。

- 使用 Go 标准库实现 JSON-RPC 2.0 和 MCP lifecycle
- 支持 Tools、Resources、Prompts
- 支持本地 STDIO 和远程 Streamable HTTP
- 加入 capability negotiation、取消、进度和错误响应
- 对远程工具执行认证、权限过滤和审批

## v15：Durable Workflow

目标：Agent 长任务可以暂停、恢复、取消和安全重试。

- 明确 Pending、Running、WaitingApproval、Completed、Failed、Cancelled 状态
- 保存 checkpoint，并在服务重启后恢复
- 支持 `context` 取消、超时、指数退避和重试上限
- 区分可重试错误与永久错误
- 支持后台任务和进度查询

## v16：A2A 与真正的多 Agent

目标：不同语言、不同部署位置的 Agent 可以发现并协作。

- 发布 Agent Card 和能力描述
- 实现任务创建、状态查询、消息和产物传递
- 对独立任务使用受控并行，对共享状态使用顺序执行
- 为每个子 Agent 设置最小工具集、预算、超时和 Trace
- 通过 Eval 判断多 Agent 是否真的优于单 Agent

## 暂不优先

- 继续扩展狼人杀和更多人格 Demo
- 只靠修改 Prompt 追求偶然效果
- 在没有 Trace 和 Eval 前增加复杂多 Agent 编排
- 为追逐框架而同时学习多个 Agent 框架

## 参考规范

- [OpenAI Agents SDK](https://developers.openai.com/api/docs/guides/agents)
- [OpenAI Agent Evals](https://developers.openai.com/api/docs/guides/agent-evals)
- [MCP Architecture](https://modelcontextprotocol.io/docs/learn/architecture)
- [OWASP Top 10 for Agentic Applications 2026](https://genai.owasp.org/resource/owasp-top-10-for-agentic-applications-for-2026/)
- [Google Developer's Guide to AI Agent Protocols](https://developers.googleblog.com/en/developers-guide-to-ai-agent-protocols/)