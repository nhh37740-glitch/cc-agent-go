# AGENT.MD — cc-agent 长期记忆

此文件由 Agent 自动维护，记录跨对话的用户偏好、项目规则和重要决策。

## 教学解释规则

- 解释代码时，先写出具体文件、函数、参数、执行顺序和数据值。
- 禁止使用类比、比喻，以及“抽象”“链路”“上下文”等不能对应到具体代码的说法。
- 函数调用必须直接说明。例如：`main.go` 调用 `service.Run(req.Message, req.ConversationId, ...)`；`req.Message` 和 `req.ConversationId` 都是 `string` 参数。
- 出现 `包名.函数名` 时，必须分别说明包名、文件路径和函数定义位置。例如：`service.Run` 表示 `main.go` 调用 `service` 包中的 `Run` 函数；`service` 来自 `service/agent.go` 的 `package service`，`Run` 定义为 `func Run(...)`。文件名 `agent.go` 不是包名。
- 遇到不清楚的内容，先说明已经读取到的代码和还没有读取的代码，不猜测。

## 项目工具与 v11 决策

- GitHub CLI 的完整路径是 `C:\Program Files\GitHub CLI\gh.exe`。调用 `gh` 时直接使用这个完整路径。
- v11 不实现 Trace ID、请求流程追踪、JSONL Trace 文件或 Trace 查询接口。
- v11 使用 `slog` JSON 日志和分类错误；工具执行错误写入 `tool_result`，由下一次 LLM 调用处理。
