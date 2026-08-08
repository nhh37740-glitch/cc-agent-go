## Context

当前正式代码把 Agent 绑定在一个进程启动时确定的 `workspace` 上：

```text
main.go
→ tool.NewBashTool("workspace")
→ BashTool永久保存workspace字符串

config.Load
→ SessionsDir固定为data/sessions

service.Run / service.RunStream
→ Store.LoadMessages(conversationId)
→ 把全部旧消息加到history
→ 每一轮DeepSeek都再次收到完整history
```

这导致三个实际问题：

1. 同一个 Agent 进程不能自然地为多个项目目录工作。
2. 当前会话越长，每次 DeepSeek 请求自动携带的历史消息越多。
3. 狼人杀、剧本杀、元老院等应用容易把自己的角色和回合规则继续写进 Agent 内部。

用户现在确定的目标是：外部程序为每次运行传入当前项目目录和当前会话 ID。Agent 可以访问这两个值对应的历史会话和项目文件，但不把全部历史会话自动塞进每次 API 请求。DeepSeek 需要旧信息时调用工具读取。应用主持人只负责选择目录、会话 ID 和当前任务。

当前 `tool.BashTool` 已经允许 `rg`、`cat`、`head`、`tail`，执行环境是 Windows Git Bash。计划中的 `agent/`、`memory/`、运行环境类型和新工具函数签名尚未实现。

## Goals / Non-Goals

**Goals:**

- 一个封装完成的 `Agent` 拥有模型调用、思考与工具循环、普通工具、MCP、SubAgent、会话记忆、token 统计和压缩能力。
- `Agent.Run()` 每次收到外部传入的项目工作目录、会话 ID 和具体任务。
- 工具和记忆文件都使用本次运行的项目工作目录，不使用全局 workspace。
- 历史会话默认只以文件位置进入模型提示词，DeepSeek 按需要调用工具读取片段。
- 使用模型对应 tokenizer 计算请求和记忆文件 token，并使用 DeepSeek usage 校正实际调用数据。
- 未来应用只在 Agent 外部保存参与者、角色、回合和规则。

**Non-Goals:**

- 本次不实现新的狼人杀、剧本杀或元老院应用。
- 本次不修改现有 `RunCouncil`。
- 不增加取消、重试、checkpoint、服务重启恢复、A2A、WebSocket 或 MCP 并发控制。
- 不自动移动旧 `workspace/data/sessions` 中的用户运行数据。
- 不让浏览器直接读取本机目录；浏览器只把用户填写的目录字符串发送给 Go 服务。

## Decisions

### 1. Agent.Run收到任务和运行环境两个明确参数

计划新增：

```go
type AgentExecutionEnvironment struct {
    WorkingDirectory string
    ConversationID   string
}

type AgentTaskInput interface {
    agentTaskInput()
}

func (executableAgent *Agent) Run(
    agentTaskInput AgentTaskInput,
    agentExecutionEnvironment AgentExecutionEnvironment,
) (AgentRunResult, error)
```

`AgentExecutionEnvironment` 的两个字段每次运行都必填：

- `WorkingDirectory`：外部调用者选中的项目目录绝对路径。
- `ConversationID`：外部调用者选中的当前会话 ID。

`Agent.Run()` 不生成 ID，也不从全局变量读取目录。HTTP 层如果想为新任务生成 ID，必须在调用 `Agent.Run()` 前生成，然后把实际值传入。

开始执行时，`Agent.Run()` 按顺序检查：

```text
WorkingDirectory非空
→ filepath.Abs
→ filepath.Clean
→ os.Stat确认目录存在
→ ConversationID符合安全字符规则
→ 构建项目会话文件路径
```

### 2. 三种任务输入不使用布尔开关

计划定义：

```go
type UserTaskInput struct {
    UserMessage string
}

type InternalContinuationTaskInput struct {
    InternalMessage model.Message
}

type HostedAgentTaskInput struct {
    HostTask model.Message
}
```

保存规则：

```text
UserTaskInput
→ 保存为普通用户任务

InternalContinuationTaskInput
→ 本次模型可见
→ 不保存成普通用户任务
→ 保存最终assistant结果

HostedAgentTaskInput
→ 保存主持人交给该角色的任务
→ 保存最终assistant结果
```

当前 `saveInputMessage bool` 删除。调用者通过具体任务类型表达用途。

### 3. 会话文件属于项目目录

会话文件位置固定由 Agent 计算：

```text
<WorkingDirectory>/.cc-agent/sessions/<ConversationID>.json
```

例如：

```text
WorkingDirectory = C:/games/werewolf-1
ConversationID   = player-3

SessionFilePath  = C:/games/werewolf-1/.cc-agent/sessions/player-3.json
```

项目指令文件位置为：

```text
<WorkingDirectory>/AGENTS.md
```

`AGENTS.md` 不存在时只说明不存在，不报错。Agent 不再读取 `workspace/memory/AGENT.MD`。

`memory.JSONConversationStore` 不保存一个固定 `sessionsDir`。公开方法都收到本次项目目录：

```go
func (conversationStore *JSONConversationStore) ResolveSessionFile(
    workingDirectory string,
    conversationID string,
) (string, error)

func (conversationStore *JSONConversationStore) LoadConversation(
    workingDirectory string,
    conversationID string,
) (model.SessionJson, error)

func (conversationStore *JSONConversationStore) AppendConversationTurn(
    conversationTurn ConversationTurnToAppend,
) error
```

`ConversationTurnToAppend` 必须包含 `WorkingDirectory` 和 `ConversationID`。同一个 `conversationID` 在两个工作目录中不会读写同一个文件。

### 4. 第一次模型调用只发送当前任务和记忆位置

`Agent.Run()` 解析运行环境后创建：

```go
type AgentMemoryReference struct {
    WorkingDirectory          string
    SessionMemoryRelativePath string
    ProjectInstructionsPath   string
}
```

交给 DeepSeek 的实际文字包含：

```text
当前工作目录：C:/games/werewolf-1
当前会话ID：player-3
历史会话文件：.cc-agent/sessions/player-3.json
项目指令文件：AGENTS.md

不要默认读取完整历史。
当前任务需要旧信息时，调用bash：
- rg -n "关键词" .cc-agent/sessions/player-3.json
- head -100 .cc-agent/sessions/player-3.json
- tail -100 .cc-agent/sessions/player-3.json
- cat AGENTS.md
先读取小片段；结果不足时再缩小或调整关键词。
```

第一次请求消息为：

```text
system：Agent固定执行规则 + AgentMemoryReference文字
user：当前UserTaskInput或HostedAgentTaskInput
```

不执行：

```text
LoadConversation
→ append全部旧Messages
→ 发送全部history
```

会话 JSON 仍然由 Go 保存。它是 DeepSeek 可以按需查询的文件，不是每次请求必须自动携带的消息列表。

### 5. 同一次Agent.Run保留工具协议所需消息

按需读取历史以后，当前运行消息按实际发生顺序保存：

```text
当前任务
→ assistant请求bash rg
→ bash tool_result
→ assistant请求另一个工具
→ 第二个tool_result
→ assistant最终文字
```

这些消息属于正在执行的同一次 `Agent.Run()`。下一轮 DeepSeek 必须收到已经完成的 assistant tool-use 和对应 tool-result，否则 DeepSeek 无法知道工具返回了什么。

新 HTTP 请求开始后，不重新发送上一轮的这些消息。上一轮内容已经写入项目会话文件，新请求只得到该文件的位置。

因此“历史不自动发送”的边界是：

```text
以前Agent.Run留下的会话记录：不自动发送
当前Agent.Run尚未结束的工具协议消息：继续发送
```

### 6. 工具执行环境由Agent.Run传到Registry.Execute

计划新增：

```go
type ToolExecutionEnvironment struct {
    WorkingDirectory string
    ConversationID   string
}
```

工具接口计划改为：

```go
type Tool interface {
    Name() string
    Description() string
    InputSchema() map[string]any
    Execute(
        toolArguments map[string]any,
        toolExecutionEnvironment ToolExecutionEnvironment,
    ) (string, error)
}
```

调用顺序：

```text
Agent.Run
→ executeOneReturnedToolCall
→ Registry.Execute(toolName, toolArguments, toolExecutionEnvironment)
→ 具体Tool.Execute(toolArguments, toolExecutionEnvironment)
```

`BashTool` 改为无固定目录：

```go
func NewBashTool() *BashTool
```

`BashTool.Execute()` 执行：

```text
读取ToolExecutionEnvironment.WorkingDirectory
→ 校验命令白名单
→ exec.CommandContext(ctx, "bash", "-c", command)
→ cmd.Dir = WorkingDirectory
→ CombinedOutput
```

`SkillTool`、`CreateSkillTool` 和其他文件工具也从 `ToolExecutionEnvironment.WorkingDirectory` 解析相对路径。MCP 浏览器工具不需要本地文件时可以不读取该字段，但仍使用同一个工具接口。

### 7. Agent配置不保存项目目录或会话ID

计划中的固定 Agent 配置：

```go
type Agent struct {
    applicationConfig        config.Config
    availableTools           *tool.Registry
    maximumRounds            int
    maximumOutputTokens      int
    maximumToolResultTokens  int
    maximumStoredMemoryTokens int
    modelContextWindowTokens int
    callModel                AgentModelCallFunction
    countTokens              AgentTokenCounter
    receiveAgentEvent        AgentEventReceiver
    prepareRound             prepareAgentRoundFunction
    executeReturnedToolCalls executeReturnedToolCallsFunction
}
```

`WorkingDirectory` 和 `ConversationID` 不在 `Agent` 字段里，因为同一个配置完成的 Agent 可以连续执行不同项目和不同会话。它们只保存在一次 `Agent.Run()` 的 `agentRunState` 中。

主 Agent 和 SubAgent 仍使用不同配置类型安装不同轮数和终止工具规则，但两个配置都不包含固定 workspace。

### 8. TokenCounter属于模型适配，不属于应用

计划定义：

```go
type AgentTokenCounter interface {
    CountPreparedModelRequest(
        preparedModelRequest PreparedModelRequest,
    ) (int, error)

    CountText(textToCount string) (int, error)

    TruncateText(
        textToTruncate string,
        maximumTokens int,
    ) (TokenTruncationResult, error)
}
```

`Agent` 只调用这个接口，不判断模型名称。新增 `modeltoken/` 包提供具体实现：

```go
type ModelTokenizerConfiguration struct {
    ModelName                 string
    TokenizerFile             string
    MaximumContextTokens      int
}

func NewHuggingFaceJSONTokenCounter(
    modelTokenizerConfiguration ModelTokenizerConfiguration,
) (*HuggingFaceJSONTokenCounter, error)
```

`modeltoken.HuggingFaceJSONTokenCounter` 导入
`github.com/amikos-tech/pure-tokenizers v0.1.5`。构造函数执行：

```text
读取ModelTokenizerConfiguration.TokenizerFile
→ tokenizers.FromFile(tokenizerFile)
→ 保存已启动的Hugging Face tokenizer
→ 返回HuggingFaceJSONTokenCounter
```

模型配置保存在 `config/model_tokenizers.json`：

```json
{
  "deepseek-v4-pro[1m]": {
    "tokenizerType": "huggingface_json",
    "tokenizerFile": "tokenizers/deepseek-v4-pro/tokenizer.json",
    "maximumContextTokens": 1048576
  }
}
```

`tokenizers/deepseek-v4-pro/tokenizer.json` 来自
`deepseek-ai/DeepSeek-V4-Pro` 的固定 revision。实现时记录下载地址、
revision 和 SHA-256，不在每次 `Agent.Run()` 中联网下载。

`main.main()` 启动服务前按当前模型名称查配置，调用
`modeltoken.NewHuggingFaceJSONTokenCounter(...)`，再把返回值传给
`agent.NewMainAgent(...)` 和 `agent.NewSubAgent(...)`。启动失败直接返回
tokenizer 配置错误，不回退到字符比例。

调用前：

```text
Agent准备system、messages和tool definitions
→ AgentTokenCounter.CountPreparedModelRequest
→ 得到PreparedRequestTokens
→ 检查上下文窗口
→ 调DeepSeek
```

调用后：

```text
DeepSeek返回ApiResponse.InputTokens和OutputTokens
→ 记录为本次调用实际usage
→ 不使用字符数乘0.6
```

DeepSeek 官方 `usage` 是实际调用后的权威值；本地 tokenizer 用于调用前
计算已知文字、工具 JSON 和尚未发送的记忆文件。DeepSeek 服务端还可能
增加本地看不到的消息分隔 token。实现时使用固定请求样本比较本地结果与
API `usage.InputTokens`，把实测差值和样本来源记录到测试中；没有实测结果
前不编造一个固定差值。调用后保存 API `usage` 的实际数值。

以后增加 Hugging Face 模型时，只在 `config/model_tokenizers.json` 增加
模型名称、该模型的 `tokenizer.json` 和窗口大小。`Agent.Run()` 和
`HuggingFaceJSONTokenCounter` 不增加模型名称分支。OpenAI tiktoken 系列
如果以后需要接入，则新增独立的具体 `TikTokenCounter`，不把不同格式塞进
`HuggingFaceJSONTokenCounter`。

### 9. API请求token和记忆文件token是两个数

`agentRunState` 分别保存：

```go
type agentRunState struct {
    executionEnvironment  AgentExecutionEnvironment
    currentRunMessages    []model.Message
    preparedRequestTokens int
    actualInputTokens     int
    actualOutputTokens    int
    storedMemoryTokens    int
}
```

具体含义：

```text
preparedRequestTokens
= 下一次实际准备发送给DeepSeek的system + messages + tools

storedMemoryTokens
= .cc-agent/sessions/<ConversationID>.json整个文件经过模型tokenizer后的数量
```

会话文件没有被工具读取时，不进入 `preparedRequestTokens`。`rg` 或 `cat` 返回的内容成为 tool-result 后，才进入下一次请求计数。

### 10. 当前运行上下文超限前先压缩当前运行消息

每次模型调用前执行：

```text
preparedRequestTokens + maximumOutputTokens
<= modelContextWindowTokens
→ 可以调用模型

超过modelContextWindowTokens
→ 找到已经完成的旧tool-use + tool-result轮次
→ 调用compactCurrentRunMessages
→ 用一条current-run summary替换这些已完成轮次
→ 保留当前任务和任何必须配对的工具协议消息
→ 重新CountPreparedModelRequest
```

压缩后仍超限时返回 `agent_context_limit_reached`，不发送已经确定超限的请求。

这项压缩只处理当前 `Agent.Run()` 已经读入 API 消息的内容，不读取整个历史会话文件。

### 11. 工具结果使用token限制

当前 `maxToolResult = 8000` 按字符截断。计划改为：

```text
Agent收到工具结果
→ AgentTokenCounter.CountText
→ 小于maximumToolResultTokens：完整加入tool_result
→ 超过：AgentTokenCounter.TruncateText
→ 追加“结果已截断，请使用更具体的rg/head/tail命令”
```

这样 `cat` 一个大文件不会把整个文件送入下一轮 API，也不会在多字节中文中按字节切断。

### 12. 保存会话后计算记忆文件token并决定压缩

一次 Agent 完成后：

```text
Agent根据具体TaskInput选择需要保存的任务消息
→ JSONConversationStore.AppendConversationTurn
→ 读取刚保存的session JSON
→ AgentTokenCounter.CountText
→ 得到storedMemoryTokens
```

判断：

```text
storedMemoryTokens <= maximumStoredMemoryTokens
→ 保存token元数据并结束

storedMemoryTokens > maximumStoredMemoryTokens
→ 调用compressStoredConversationMemory
→ 压缩调用不带工具
→ 旧记录写入同目录archive文件
→ session文件保存摘要 + 最近记录
→ 再次CountText保存新token数
```

这是唯一允许主动读取并总结大量历史记录的调用，因为它的目的就是压缩历史文件。普通任务调用仍只发送记忆位置。

压缩失败时保留原文件。保存失败时 `AgentRunResult` 仍返回已经生成的模型结果，同时带 `AgentMemorySaveFailed`。

### 13. 主Agent、SubAgent和主持人都传运行环境

WebAgent：

```text
index.html
→ POST {workingDirectory, conversationId, message}
→ main.handleChatStream
→ service.RunStream
→ Agent.Run(UserTaskInput, AgentExecutionEnvironment)
```

后台 SubAgent 完成回调：

```text
continueMainAgentAfterSubAgents
→ 使用父任务原WorkingDirectory和ConversationID
→ Agent.Run(InternalContinuationTaskInput, AgentExecutionEnvironment)
```

`run_subagent` 工具：

```text
主Agent执行已注册run_subagent工具
→ 工具执行函数收到父ToolExecutionEnvironment
→ 外部SubAgent主持函数为每个任务选择child ConversationID
→ child WorkingDirectory默认继承父WorkingDirectory
→ NewSubAgent
→ Agent.Run(HostedAgentTaskInput, child AgentExecutionEnvironment)
```

Agent 核心只执行注册工具，不负责制定 child ID 命名规则。

### 14. 狼人杀、剧本杀和元老院是Agent外部包装

未来应用调用关系固定为：

```text
applications/werewolf.Host
→ 保存玩家、身份、白天/夜晚回合和胜负状态
→ 为每名玩家选择ConversationID
→ Agent.Run(HostedAgentTaskInput, AgentExecutionEnvironment)

applications/scriptmurder.Host
→ 保存角色、线索和阶段
→ 为每名角色选择ConversationID
→ Agent.Run(HostedAgentTaskInput, AgentExecutionEnvironment)

applications/council.Host
→ 保存议员、发言顺序和讨论轮数
→ 为每名议员选择ConversationID
→ Agent.Run(HostedAgentTaskInput, AgentExecutionEnvironment)
```

禁止在 `agent` 包中增加：

```go
AgentMode string

if AgentMode == "werewolf" { ... }
if AgentMode == "council" { ... }
```

应用之间共享的是 `Agent.Run()` 的能力，不共享应用规则。

### 15. 事件明确返回当前运行信息

计划事件包括：

```text
AgentMemoryReferenceReadyEvent
AgentRoundStartedEvent
AgentTextDeltaEvent
AgentToolExecutionStartedEvent
AgentToolExecutionSucceededEvent
AgentToolExecutionFailedEvent
AgentCurrentRunCompactedEvent
AgentStoredMemoryCompressedEvent
AgentMemorySaveFailedEvent
AgentCompletedEvent
```

记忆引用事件只返回 conversation ID 和相对会话文件路径，不发送历史正文。HTTP SSE 根据具体事件类型编码，删除 `token[0] == '{'`。

### 16. WebAgent页面管理项目会话、日志和MCP Server

页面固定输入只有：

```text
项目工作目录：文本输入
当前任务：多行文本输入
开始执行：按钮
```

页面不要求用户填写会话 ID。点击“新建会话”后，页面把空
`conversationId` 发送给 `main.handleChatStream`。handler 调用
`service.GenerateConversationId()` 得到实际 ID，把该 ID 写入第一条
`conversation_id` SSE 事件，然后再调用 `Agent.Run()`。

选择已有会话时，页面发送该会话实际 ID：

```json
{
  "workingDirectory": "C:/Users/Z/Documents/project/code/example",
  "conversationId": "已有会话的实际ID或空字符串",
  "message": "检查这个项目当前的编译错误"
}
```

项目会话读取顺序：

```text
工作目录输入发生变化或用户点击刷新
→ GET /api/conversations?workingDirectory=<实际绝对目录>
→ main.handleListConversations
→ ProjectConversationStore.ListConversations
→ 页面显示该目录中的会话标题、更新时间和token

用户点击一项会话
→ GET /api/conversations/<实际ID>?workingDirectory=<实际绝对目录>
→ main.handleGetConversation
→ 页面显示该会话的user和assistant历史消息
```

日志读取顺序：

```text
页面加载或用户点击刷新日志
→ GET /api/logs?limit=200
→ main.handleListRecentLogs
→ 读取logs/server.jsonl最后200条有效JSON
→ 页面显示time、level、component、operation、msg和error
```

日志接口只读取正式服务已经脱敏的结构化日志，不读取用户正文、模型回复、
工具参数或API Key。

MCP Server 列表继续使用 `GET /api/mcp/servers`。页面必须检查
`response.ok`；失败时显示实际 HTTP 状态和后端错误 JSON。Go 服务未启动时，
浏览器显示网络连接失败，不能显示“没有配置 MCP Server”。

Go 后端不让浏览器读取文件。它只接收路径字符串，校验该目录存在，然后让 Agent 和工具在该目录工作。

### 17. 文件和依赖方向

计划目录：

```text
agent/
├── agent.go
├── configuration.go
├── execution_environment.go
├── task_input.go
├── memory_reference.go
├── run_state.go
├── run_context_compaction.go
├── token_counter.go
├── events.go
├── model_call.go
├── round.go
├── tool_execution.go
└── result.go

memory/
├── conversation_store.go
├── conversation_path.go
├── conversation_save.go
└── conversation_store_test.go
```

依赖：

```text
main
→ service
→ agent
→ memory、config、model、tool

future application host
→ agent

memory
→ model、Go标准库
```

`agent` 不导入 `service`、`main` 或任何应用包。应用包可以导入 `agent`，但 `agent` 不能反向导入应用包。

## Risks / Trade-offs

- [模型不主动读取需要的旧信息] → 初始提示词明确给出会话文件和查询命令；Agent事件记录是否执行过记忆读取，后续可评估提示词效果。
- [模型使用cat读取过多内容] → 工具结果改为token限制，并在截断结果中要求使用更具体的rg、head或tail。
- [调用者传错工作目录] → Agent.Run在模型和工具执行前校验绝对目录、存在性和会话ID。
- [旧workspace会话不会自动出现] → 本次明确放弃全局workspace；旧运行数据保持原样，不静默移动到任意项目。
- [工具接口变化影响所有工具] → 先修改Tool接口和Registry测试，再逐个迁移bash、Skill、函数工具和MCP动态工具。
- [tokenizer Go 包较新] → 固定 `github.com/amikos-tech/pure-tokenizers v0.1.5` 和原生库版本；先用 DeepSeek 官方 `tokenizer.json` 与官方 `AutoTokenizer` 生成的固定样本逐项核对，任何不一致都停止接入。
- [DeepSeek服务端消息包装不可见] → 用固定请求样本比较本地结果与 API `usage.InputTokens`；调用完成后以 DeepSeek API `usage` 为实际统计值，不在没有测量结果时写死差值。
- [tokenizer文件或原生库缺失] → 服务启动时加载并验证，不在 Agent 请求执行到一半时才下载或回退字符估算。
- [未来应用试图修改Agent内部] → 规范和测试明确禁止应用模式字段；使用测试Host证明外部调用即可完成角色任务。

## Migration Plan

1. 固定现有工具、会话JSON、主Agent、SubAgent和SSE行为测试。
2. 新增运行环境和三种任务输入类型。
3. 让Tool接口、Registry和所有具体工具接收本次工作目录与会话ID。
4. 把Store迁移为按每次workingDirectory计算`.cc-agent/sessions`路径的memory包。
5. 新增AgentMemoryReference，删除第一次请求自动加载全部历史消息。
6. 引入固定版本的`pure-tokenizers`，加载固定revision的DeepSeek V4 `tokenizer.json`，新增AgentTokenCounter并与官方AutoTokenizer样本核对。
7. 实现唯一Agent.Run循环、当前运行token检查、工具结果token截断和当前运行压缩。
8. 实现项目会话保存、精确记忆token计数和超限压缩。
9. 迁移主Agent流式、非流式、后台回调和SubAgent调用者。
10. 修改HTTP类型和WebAgent页面，传入项目目录、会话ID和任务。
11. 增加外部测试Host，证明同一个Agent可被两个应用目录和多个会话ID调用，且无需应用模式分支。
12. 更新项目文档并完成编译、测试、真实DeepSeek、MCP、SubAgent和按需记忆读取验证。

## Resolved Decisions

- 用户已经允许为 tokenizer 增加第三方依赖。
- v15 使用 `github.com/amikos-tech/pure-tokenizers v0.1.5` 加载 Hugging Face `tokenizer.json`，不启动独立 Python tokenizer 进程。
- 当前模型使用 DeepSeek V4 官方 `tokenizer.json`；API 返回的 `usage` 是调用后的实际 token 数。
