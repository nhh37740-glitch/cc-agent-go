## 1. 读取 SubAgent 并行数配置

- [x] 1.1 在 `config.Config` 增加 `MaximumParallelSubAgents int`
- [x] 1.2 在 `config/config.go` 实现 `loadMaximumParallelSubAgents()`，读取 `MAX_PARALLEL_SUBAGENTS`
- [x] 1.3 确认空值、非整数、小于 1 或大于 5 时返回默认值 5，合法的 1 到 5 保留原值
- [x] 1.4 增加 `config/config_test.go`，检查默认值、合法值、非法值和硬上限

## 2. 复制 SubAgent 可用工具

- [x] 2.1 在 `tool/registry.go` 实现 `CopyExcludingTools(excludedToolNames ...string) *Registry`
- [x] 2.2 确认复制结果包含调用时已经注册的普通工具和 MCP 工具，同时排除 `run_subagent`
- [x] 2.3 增加 `tool/registry_test.go`，检查复制前后是两个 Registry、排除工具不存在、其他工具仍可执行

## 3. 执行一个临时通用 SubAgent

- [x] 3.1 新建 `service/subagent.go`，定义 `SubAgentTask`、`SubAgentResult` 和明确的 JSON tag
- [x] 3.2 定义通用 SubAgent system prompt、最多 12 轮、最多 4096 输出 token 和最多 8000 字符工具结果
- [x] 3.3 实现 `RunSubAgent(subAgentTask, applicationConfig, availableSubAgentTools)`，创建只包含本次 task 的新消息记录
- [x] 3.4 在 `RunSubAgent` 中调用现有 `Chat`，没有工具调用时直接返回 DeepSeek 最终文字
- [x] 3.5 在 `RunSubAgent` 中把 DeepSeek 返回的 assistant 文字和 `tool_use` 加入临时消息记录
- [x] 3.6 在 `RunSubAgent` 中调用 `availableSubAgentTools.Execute`，把成功结果或错误文字组成 `tool_result` 后继续下一轮
- [x] 3.7 实现 SubAgent 工具结果超过 8000 字符时截取，以及 12 轮后返回 `agent_limit_reached`
- [x] 3.8 增加 `service/subagent_test.go`，用本地测试 HTTP Server 检查临时消息、最终文字、工具调用、工具错误和 12 轮限制

## 4. 同时执行多个 SubAgent

- [x] 4.1 在 `service/subagent.go` 定义 `indexedSubAgentResult`
- [x] 4.2 实现 `RunSubAgentsInParallel(subAgentTasks, applicationConfig, availableSubAgentTools)`
- [x] 4.3 为每个 `SubAgentTask` 启动一个 goroutine，并把输入位置和任务作为 goroutine 的明确参数
- [x] 4.4 使用 `completedSubAgentResults` channel 收取成功或失败结果
- [x] 4.5 按输入位置把结果写回 `orderedSubAgentResults`，保证返回顺序与输入顺序一致
- [x] 4.6 增加测试，确认配置允许 5 个时三个任务会同时开始，并确认先完成的第二个任务仍在结果数组第二位
- [x] 4.7 增加测试，确认一个任务失败时其他成功结果仍然返回

## 5. 注册固定 JSON 输入输出的 `run_subagent`

- [x] 5.1 在 `main.go` 定义 `RunSubAgentToolInput` 和 `RunSubAgentToolOutput`
- [x] 5.2 在 `main.go` 定义 `run_subagent` JSON Schema，输入为必填 `subAgentTasks` 数组，每项包含必填 `taskId` 和 `task`，数组绝对上限为 5
- [x] 5.3 实现 `decodeAndValidateRunSubAgentToolInput(toolArguments, maximumParallelSubAgents)`
- [x] 5.4 检查任务数组非空、任务数不超过配置值、`taskId` 和 `task` 非空、`taskId` 不重复
- [x] 5.5 实现 `registerGeneralSubAgentTool(mainAgentToolRegistry)`
- [x] 5.6 在工具执行函数中调用 `config.Load()`、`decodeAndValidateRunSubAgentToolInput` 和 `CopyExcludingTools("run_subagent")`
- [x] 5.7 在工具执行函数中调用 `service.RunSubAgentsInParallel`，再把 `RunSubAgentToolOutput` 编码成 JSON 字符串返回
- [x] 5.8 在 `main()` 注册 Bash、Skill 和 CreateSkill 后注册 `run_subagent`，注册失败时写启动错误并退出
- [x] 5.9 增加测试，检查无效输入在启动 SubAgent 前失败，以及成功和失败结果都包含固定 JSON 字段
- [x] 5.10 确认现有 `service.Run`、`service.RunStream` 和 HTTP 函数签名没有变化

## 6. 实际运行检查

- [x] 6.1 把 `MAX_PARALLEL_SUBAGENTS` 设为 3，启动服务并确认运行配置中的最大并行数是 3
- [x] 6.2 使用有效 `DEEPSEEK_API_KEY`，让主 Agent 一次提交两个独立任务，确认两个 SubAgent 都开始执行
- [x] 6.3 检查返回值是 JSON，包含 `maximumParallelSubAgents: 3`，并包含两个任务的 `taskId`、`status`、`result` 和 `error`
- [x] 6.4 提交四个任务，确认工具在任何 SubAgent 启动以前返回超过配置数量的参数错误
- [x] 6.5 启动一台 MCP Server 后再调用 `run_subagent`，确认 SubAgent 工具定义包含已注册 MCP 工具但不包含 `run_subagent`
- [x] 6.6 检查多个 SubAgent 完成后没有生成独立 conversation ID 或 session JSON
- [x] 6.7 分别通过 `/api/chat` 和 `/api/chat/stream` 调用，确认非流式和 SSE 主 Agent 都收到完整 JSON `tool_result`

## 7. 文档、完整检查和版本状态

- [x] 7.1 更新 `PROJECT_INDEX.md`，写出 `config/config.go`、`main.go`、`service/subagent.go` 和 `tool/registry.go` 的实际函数、参数、调用顺序和返回值
- [x] 7.2 更新 `README.md`、`ROADMAP.md` 和 `AGENTS.md`，记录 v14 的固定 JSON 输入输出、配置并行数和最多 5 个临时 SubAgent
- [x] 7.3 运行 `go fmt ./...`、`go build ./...`、`go vet ./...` 和 `go test ./...`，任何一项失败都先修复
- [x] 7.4 检查暂存内容不含 API Key、用户文件或 `workspace/银河争霸战.txt`，然后再提交、推送和更新 GitHub Project
