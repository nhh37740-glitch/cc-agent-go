## Why

当前 `service.Run` 只能由主 Agent 自己完成用户任务。v14 增加通用型 SubAgent 工具，让主 Agent 可以一次提交一组独立任务，由多个临时 SubAgent 同时执行，并通过固定 JSON 格式收回每个任务的结果。

## What Changes

- 在现有 `tool.Registry` 注册普通工具 `run_subagent`；输入固定为 JSON 对象，其中 `subAgentTasks` 数组保存一个或多个任务，每个任务包含 `taskId` 和 `task`。
- `run_subagent` 返回固定 JSON 对象，其中 `results` 数组按输入顺序保存每个任务的 `taskId`、`status`、`result` 和 `error`。
- `config.Config` 增加 `MaximumParallelSubAgents`；配置值决定一次可以同时执行几个 SubAgent，默认值和硬上限都是 5。
- 主 Agent 调用 `run_subagent` 时，每个任务新建一份只包含本次 `task` 的临时消息记录，再分别调用现有 DeepSeek API。
- SubAgent 可以调用执行当时已经注册的 Bash、Skill、CreateSkill 和 MCP 工具。
- 给 SubAgent 的工具表排除 `run_subagent`，SubAgent 不能继续创建另一个 SubAgent。
- 每个 SubAgent 最多执行 12 轮 DeepSeek 和工具调用；得到最终文字或错误后，写入与该 `taskId` 对应的 JSON 结果。
- SubAgent 不创建会话文件，不写入主 Agent 的会话历史；只有最终结果作为主 Agent 的 `tool_result` 继续参与主 Agent 下一轮调用。
- 不增加 router、固定职能 Agent、handoff、新 HTTP 路由、网页修改、后台任务、任务取消或跨请求保留的 SubAgent。

## Capabilities

### New Capabilities

- `general-subagent-as-tool`: 主 Agent 通过一个普通工具提交 JSON 任务数组，在配置限制内同时运行多个临时通用 SubAgent，并接收固定 JSON 格式的执行结果。

### Modified Capabilities

无。

## Impact

- 新增 `service/subagent.go`，实现通用 SubAgent 的 DeepSeek 和工具调用过程。
- 修改 `tool/registry.go`，增加复制当前工具表并排除指定工具的函数。
- 修改 `config/config.go`，保存最多同时执行的 SubAgent 数量。
- 修改 `main.go`，注册 `run_subagent`、解析 JSON 工具参数并把执行函数连接到 `service.RunSubAgentsInParallel`。
- 更新 `PROJECT_INDEX.md`、`README.md`、`ROADMAP.md` 和 `AGENTS.md`，记录 v13 已跳过以及 v14 的实际执行顺序。
- 继续使用当前 DeepSeek 配置、错误处理、`tool_result` 格式和 Go 标准库；`go.mod` 不增加第三方依赖。
