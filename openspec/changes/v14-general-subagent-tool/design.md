## Context

当前 `main.go` 创建一个全局 `registry = tool.NewRegistry()`。`main()` 注册 Bash、Skill 和 CreateSkill；网页选择 MCP Server 后，`MCPServerManager.StartSelectedMCPServers` 把 MCP 工具注册到同一个 `registry`。

`service.Run` 和 `service.RunStream` 每轮调用 `registry.GetDefinitions()`，把当前工具定义传给 DeepSeek。DeepSeek 返回工具调用后，这两个函数调用 `registry.Execute(toolName, toolArguments)`，再把工具返回的字符串写入下一轮 `tool_result`。

v14 继续使用这个执行位置。主 Agent 看到一个名为 `run_subagent` 的普通工具。它调用这个工具时传入固定 JSON；工具同时运行一组临时 SubAgent；工具完成后返回固定 JSON 字符串。`service.Run` 和 `service.RunStream` 不需要增加新的参数。

## Goals / Non-Goals

**Goals:**

- `run_subagent` 的输入固定为包含 `subAgentTasks` 的 JSON。
- `run_subagent` 的输出固定为包含 `maximumParallelSubAgents` 和 `results` 的 JSON。
- 每个任务包含 `taskId` 和完整的 `task`。
- 每个结果包含 `taskId`、`status`、`result` 和 `error`。
- 一个工具调用可以同时启动多个临时 SubAgent。
- 实际并行数由 `config.Config.MaximumParallelSubAgents` 决定，默认 5，硬上限 5。
- 每个 SubAgent 使用自己的临时消息记录，不读取主 Agent 之前的消息。
- 所有 SubAgent 可以使用执行时已经存在的普通工具和 MCP 工具，但不能再次调用 `run_subagent`。
- 实现继续使用现有 DeepSeek Client、工具类型和错误分类，不增加第三方 Go 依赖。

**Non-Goals:**

- 不定义代码检查、资料搜索等固定职能 Agent。
- 不实现 Agent handoff。
- 不增加新的 HTTP 路由或网页控件。
- 不保存独立 SubAgent 会话。
- 不增加后台任务、任务状态、任务取消、恢复或重试。
- 不让 SubAgent 在 `run_subagent` 返回后继续运行。

## Decisions

### 1. `config.Load` 读取 SubAgent 并行数

`config/config.go` 的 `Config` 增加：

```go
MaximumParallelSubAgents int
```

文件内增加：

```go
const defaultMaximumParallelSubAgents = 5
const hardMaximumParallelSubAgents = 5

func loadMaximumParallelSubAgents() int
```

`loadMaximumParallelSubAgents` 的执行顺序：

1. 调用 `os.Getenv("MAX_PARALLEL_SUBAGENTS")` 读取字符串。
2. 环境变量为空时返回 5。
3. 调用 `strconv.Atoi` 把字符串转换为整数。
4. 转换失败、整数小于 1 或整数大于 5 时返回 5。
5. 整数位于 1 到 5 时返回该整数。

`config.Load()` 把这个返回值写入 `Config.MaximumParallelSubAgents`。

这样部署者可以通过环境变量选择 1、2、3、4 或 5。代码仍然保留 5 的硬上限，即使环境变量写成 20，也只允许 5 个 SubAgent 同时执行。

没有使用 `mcp_servers.json` 保存这个值，因为它只保存 MCP Server 的启动命令。没有增加新的配置文件，因为当前应用运行配置已经由 `config.Load()` 统一生成。

### 2. `run_subagent` 使用固定 JSON 输入

`main.go` 增加用于解包工具参数的类型：

```go
type RunSubAgentToolInput struct {
    SubAgentTasks []service.SubAgentTask `json:"subAgentTasks"`
}
```

`service/subagent.go` 定义每个任务：

```go
type SubAgentTask struct {
    TaskID string `json:"taskId"`
    Task   string `json:"task"`
}
```

工具输入示例：

```json
{
  "subAgentTasks": [
    {
      "taskId": "compile-check",
      "task": "读取当前仓库的 Go 文件，运行 go build ./...，返回失败文件、行号和错误文字"
    },
    {
      "taskId": "document-check",
      "task": "读取 ROADMAP.md 和 PROJECT_INDEX.md，返回两份文件中不一致的版本信息"
    }
  ]
}
```

`main.go` 增加：

```go
func decodeAndValidateRunSubAgentToolInput(
    toolArguments map[string]any,
    maximumParallelSubAgents int,
) (RunSubAgentToolInput, error)
```

这个函数的执行顺序：

1. 调用 `json.Marshal(toolArguments)`，得到 `runSubAgentToolInputJSONBytes`。
2. 调用 `json.Unmarshal`，把 JSON 解包到 `RunSubAgentToolInput`。
3. 检查 `SubAgentTasks` 至少有 1 个任务。
4. 检查任务数不超过 `maximumParallelSubAgents`。
5. 逐个检查 `taskId` 和 `task` 都是去除首尾空格后的非空字符串。
6. 使用 `map[string]bool` 检查 `taskId` 没有重复。
7. 验证成功时返回完整的 `RunSubAgentToolInput`。

工具 JSON Schema 的 `subAgentTasks` 同时写入 `minItems: 1` 和 `maxItems: 5`。Schema 告诉 DeepSeek 绝对不能生成 6 个任务；运行时检查再执行当前配置值。例如配置值是 3，Schema 仍允许最多 5，但函数会拒绝第 4 个任务。

没有继续使用单个 `task` 字符串，因为一个字符串无法明确表示多个独立任务，也无法给每个返回结果保留稳定的 `taskId`。

### 3. `main.go` 注册工具并连接配置、工具表和并行执行函数

`main.go` 增加：

```go
func registerGeneralSubAgentTool(
    mainAgentToolRegistry *tool.Registry,
) error
```

`main()` 在注册 Bash、Skill 和 CreateSkill 之后调用：

```go
registerGeneralSubAgentError := registerGeneralSubAgentTool(registry)
```

`registerGeneralSubAgentTool` 调用：

```go
mainAgentToolRegistry.RegisterFunctionTool(
    "run_subagent",
    generalSubAgentToolDescription,
    generalSubAgentToolInputSchema,
    executeRunSubAgentTool,
)
```

`executeRunSubAgentTool` 是 `registerGeneralSubAgentTool` 内创建的函数值。它保留 `mainAgentToolRegistry`，所以 DeepSeek 以后调用工具时，这个函数仍然可以复制当时的主 Agent 工具表。

`executeRunSubAgentTool` 收到 `toolArguments map[string]any` 后依次执行：

1. 调用 `config.Load()`，得到 `applicationConfig`。
2. 调用 `decodeAndValidateRunSubAgentToolInput(toolArguments, applicationConfig.MaximumParallelSubAgents)`。
3. 调用 `mainAgentToolRegistry.CopyExcludingTools("run_subagent")`，得到 `availableSubAgentTools`。
4. 调用：

```go
subAgentResults := service.RunSubAgentsInParallel(
    runSubAgentToolInput.SubAgentTasks,
    applicationConfig,
    availableSubAgentTools,
)
```

5. 创建：

```go
runSubAgentToolOutput := RunSubAgentToolOutput{
    MaximumParallelSubAgents: applicationConfig.MaximumParallelSubAgents,
    Results:                  subAgentResults,
}
```

6. 调用 `json.Marshal(runSubAgentToolOutput)`。
7. 把 JSON 字节转换成字符串，返回给 `Registry.Execute`。

没有新增 router。现有 DeepSeek 工具选择已经能够调用 `run_subagent`，增加 router 会在工具选择以前再调用一次模型。

### 4. 每次工具调用复制当前工具表并删除 `run_subagent`

`tool/registry.go` 增加：

```go
func (sourceToolRegistry *Registry) CopyExcludingTools(
    excludedToolNames ...string,
) *Registry
```

函数执行顺序：

1. 把 `excludedToolNames` 写入 `map[string]bool`。
2. 创建 `copiedToolRegistry := NewRegistry()`。
3. 对 `sourceToolRegistry.toolsMutex` 加读锁。
4. 遍历 `sourceToolRegistry.tools`。
5. 工具名称不在排除表中时，把同一个 `Tool` 值写入 `copiedToolRegistry.tools`。
6. 解除读锁并返回 `copiedToolRegistry`。

复制发生在每次 `run_subagent` 开始执行时。因此，已经通过网页启动的 MCP Server 工具会进入 SubAgent 工具表。复制完成以后，主工具表增加或删除工具，不修改这批正在运行的 SubAgent 使用的工具表。

没有直接把主工具表传给 SubAgent，因为主工具表包含 `run_subagent`，会允许 SubAgent 继续创建 SubAgent。

### 5. `RunSubAgentsInParallel` 同时启动任务并按输入顺序收集结果

`service/subagent.go` 增加结果类型：

```go
type SubAgentResult struct {
    TaskID string `json:"taskId"`
    Status string `json:"status"`
    Result string `json:"result"`
    Error  string `json:"error"`
}
```

文件内增加只在收集过程中使用的类型：

```go
type indexedSubAgentResult struct {
    InputIndex     int
    SubAgentResult SubAgentResult
}
```

并行函数签名：

```go
func RunSubAgentsInParallel(
    subAgentTasks []SubAgentTask,
    applicationConfig config.Config,
    availableSubAgentTools *tool.Registry,
) []SubAgentResult
```

`RunSubAgentsInParallel` 的执行顺序：

1. 创建长度等于 `len(subAgentTasks)` 的 `orderedSubAgentResults`。
2. 创建容量等于任务数的 `completedSubAgentResults` channel。
3. 遍历 `subAgentTasks`。
4. 每次遍历用 `go func` 启动一个 goroutine，并明确传入 `inputIndex` 和 `subAgentTask`，避免 goroutine 读取下一次循环的新值。
5. 每个 goroutine 调用 `RunSubAgent(subAgentTask.Task, applicationConfig, availableSubAgentTools)`。
6. `RunSubAgent` 成功时，goroutine 创建 `status: "completed"`、`result: <最终文字>`、`error: ""` 的结果。
7. `RunSubAgent` 失败时，goroutine 创建 `status: "failed"`、`result: ""`、`error: <错误文字>` 的结果。
8. goroutine 把 `inputIndex` 和结果写入 `completedSubAgentResults`。
9. 调用者从 channel 读取与任务数相同次数。
10. 每次读取后，按照 `inputIndex` 把结果写入 `orderedSubAgentResults` 的对应位置。
11. 全部结果收到以后，返回 `orderedSubAgentResults`。

输入任务数已经由 `decodeAndValidateRunSubAgentToolInput` 检查，不会超过 `applicationConfig.MaximumParallelSubAgents`，所以这里启动的 goroutine 数量就是本次实际并行数，也不会超过 5。

没有让一个 SubAgent 失败后取消其他 SubAgent。各任务相互独立，已经成功的结果仍然需要返回给主 Agent。

没有按照完成时间追加结果。第二个任务即使先结束，也仍然写回数组的第二个位置，主 Agent可以使用 `taskId` 和稳定顺序读取结果。

### 6. 每个 goroutine 调用 `RunSubAgent` 完成一个任务

`service/subagent.go` 增加：

```go
func RunSubAgent(
    subAgentTask string,
    applicationConfig config.Config,
    availableSubAgentTools *tool.Registry,
) (string, error)
```

文件内固定值：

```go
const subAgentMaximumRounds = 12
const subAgentMaximumOutputTokens = 4096
const subAgentMaximumToolResultCharacters = 8000
```

`RunSubAgent` 的执行顺序：

1. 创建 `subAgentMessageHistory`，只放一条 `Role: "user"`、`Text: subAgentTask` 的消息。
2. 调用 `availableSubAgentTools.GetDefinitions()` 取得工具定义。
3. 进入最多 12 轮的 `for`。
4. 每轮调用 `Chat(subAgentMessageHistory, generalSubAgentSystemPrompt, applicationConfig, subAgentToolDefinitions, 4096)`。
5. DeepSeek 没有返回工具调用时，返回 `deepSeekResponse.Text`。
6. DeepSeek 返回工具调用时，把 assistant 文字和每个 `tool_use` 加入 `subAgentMessageHistory`。
7. 对每个工具调用执行 `availableSubAgentTools.Execute(toolCall.Name, toolCall.Input)`。
8. 工具执行失败时，把 `工具执行错误: <error>` 作为该工具结果，继续下一轮。
9. 工具结果超过 8000 个字符时截取前 8000 个字符，并写出原长度。
10. 把全部 `tool_result` 组成一条 user 消息加入 `subAgentMessageHistory`，开始下一轮。
11. 12 轮以后仍没有最终文字时，返回 `NewAppError(ErrorAgentLimit, "service.RunSubAgent", 0, error)`。

`RunSubAgent` 不接收 `Store`、`conversationID` 或主 Agent 历史，所以它不会读取或保存会话文件。

没有直接调用现有 `service.Run`，因为 `Run` 会读取和保存会话 JSON，也会生成 conversation ID。

### 7. `run_subagent` 返回固定 JSON

`main.go` 增加：

```go
type RunSubAgentToolOutput struct {
    MaximumParallelSubAgents int                      `json:"maximumParallelSubAgents"`
    Results                  []service.SubAgentResult `json:"results"`
}
```

两个任务都成功时，工具返回：

```json
{
  "maximumParallelSubAgents": 5,
  "results": [
    {
      "taskId": "compile-check",
      "status": "completed",
      "result": "go build ./... 已通过",
      "error": ""
    },
    {
      "taskId": "document-check",
      "status": "completed",
      "result": "ROADMAP.md 与 PROJECT_INDEX.md 都记录当前版本为 v14",
      "error": ""
    }
  ]
}
```

一个任务失败时，工具仍然返回完整 JSON：

```json
{
  "maximumParallelSubAgents": 5,
  "results": [
    {
      "taskId": "compile-check",
      "status": "failed",
      "result": "",
      "error": "network_timeout"
    },
    {
      "taskId": "document-check",
      "status": "completed",
      "result": "未发现版本信息不一致",
      "error": ""
    }
  ]
}
```

非流式调用的返回顺序：

1. `service.Run` 调用 `registry.Execute("run_subagent", toolArguments)`。
2. `executeRunSubAgentTool` 等待所有 SubAgent 结束并返回 JSON 字符串。
3. `registry.Execute` 把 JSON 字符串返回 `service.Run`。
4. `service.Run` 把 JSON 字符串写入匹配本次 `tool_use` ID 的 `tool_result`。
5. 主 Agent 下一轮 DeepSeek 调用读取 JSON，并生成最终回答或继续使用工具。

`service.RunStream` 执行相同顺序。SubAgent 内部不把中间 token 发给网页；所有 SubAgent 结束后，主 Agent 才收到一个完整 JSON 工具结果。现有 SSE 事件格式不修改。

## Risks / Trade-offs

- [多个 SubAgent 同时调用 DeepSeek，可能更快遇到 Provider 限流] → `MaximumParallelSubAgents` 可以配置为 1 到 5；单个任务失败写入自己的 JSON 结果，不删除其他结果。
- [多个 SubAgent 同时调用同一个有写入行为的工具，可能修改同一个文件] → 主 Agent 必须把独立任务分开；v14 不增加文件锁或修改冲突合并，这属于后续工作流版本。
- [主 Agent 写入的 `task` 信息不完整] → 工具说明要求每个 `task` 包含文件位置、要执行的动作和需要返回的内容；SubAgent 不读取父消息记录。
- [主 Agent 流式请求等待期间没有 SubAgent 中间 token] → v14 只返回最终 JSON，不修改 SSE 事件格式。
- [主循环和 SubAgent 循环存在重复代码] → v14 保持已经运行的主循环不变；等两套执行过程经过实际验证后再决定是否整理共同函数。

## Migration Plan

1. 修改 `config/config.go`，增加 `MaximumParallelSubAgents` 和环境变量读取。
2. 修改 `tool/registry.go`，增加 `CopyExcludingTools` 和对应测试。
3. 新增 `service/subagent.go` 和对应测试，实现单任务循环、并行启动和有序结果。
4. 修改 `main.go`，增加 JSON 输入输出类型、参数验证和 `run_subagent` 注册。
5. 更新 `PROJECT_INDEX.md`、`README.md`、`ROADMAP.md` 和 `AGENTS.md`。
6. 运行 JSON 输入检查、配置并行数检查、并行执行检查、结果顺序检查和失败结果检查。
7. 运行 `go fmt ./...`、`go build ./...`、`go vet ./...` 和 `go test ./...`。
8. 回滚时删除 `service/subagent.go`，删除 `CopyExcludingTools`，撤销 `config/config.go` 和 `main.go` 的 v14 修改；没有会话数据需要迁移。

## Open Questions

无。固定职能 Agent、handoff、后台任务、任务取消和任务恢复是否进入后续版本，等 v14 的 JSON 输入输出与最多 5 个并行 SubAgent 实际运行后再决定。
