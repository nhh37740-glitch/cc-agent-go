# cc-agent-go

用 Go 实现可复用的 AI Agent。当前主线不是聊天机器人，而是一个可以被
WebAgent、SubAgent 和其他应用共同调用的 `agent.Agent`。

## v15 已完成的执行方式

WebAgent 每次执行发送三个字段。新会话的 `conversationId` 是空字符串：

```json
{
  "workingDirectory": "C:/projects/example",
  "conversationId": "",
  "message": "检查当前项目并给出结果"
}
```

`main.handleChatStream` 先检查 `conversationId`。空值时调用
`service.GenerateConversationId()` 创建实际编号，并在第一条 SSE 事件中返回。
选择已有会话时沿用页面发送的实际编号。随后 handler 将它们分别放入：

- `agent.UserTaskInput.Message`
- `agent.AgentExecutionEnvironment.WorkingDirectory`
- `agent.AgentExecutionEnvironment.ConversationID`

随后 `service.RunAgentTask` 创建配置完成的 `agent.Agent`，并调用：

```go
configuredAgent.Run(agentTaskInput, executionEnvironment)
```

`Agent.Run` 中只有一份模型和工具循环。普通 WebAgent 任务、后台 SubAgent
结果回调、SubAgent 任务和 `host.RunParticipantTurn` 都使用这一份循环。

## 项目目录和历史记录

Agent 不保存固定 workspace。调用者传入的 `workingDirectory` 必须是已经
存在的绝对目录。

会话文件固定写入：

```text
<WorkingDirectory>/.cc-agent/sessions/<ConversationID>.json
```

第一次 DeepSeek 调用只发送当前任务。system message 会告诉 DeepSeek
当前工作目录、会话编号、会话文件位置和 `AGENTS.md` 位置；不会自动发送
会话文件中的全部旧正文。需要旧信息时，DeepSeek 可以调用 bash，使用
`rg`、`head`、`tail` 或 `cat` 读取必要片段。

狼人杀、剧本杀、元老院和其他应用负责角色、回合、顺序和胜负。它们只把
一个角色的工作目录、角色会话编号和当前任务传给 `Agent.Run`，不得把应用
模式写入 `agent.Agent`。

## Tokenizer

唯一直接引入的第三方功能依赖是
`github.com/amikos-tech/pure-tokenizers v0.1.5`。它读取仓库中的 DeepSeek
V4 官方 `tokenizer.json`：

- 配置：`config/model_tokenizers.json`
- 文件：`tokenizers/deepseek-v4-pro/tokenizer.json`
- 固定 revision：`b5968e9190ef611bbf34a7229255be88a0e937c1`
- SHA-256：`8f9f37ca37fdc4f5fd36d5cf4d3b0e8392edb4e894fd10cc0d70b4957c8633cf`

`pure-tokenizers` 第一次运行会把匹配当前操作系统的原生库放入用户缓存。
加载 tokenizer 文件或原生库失败时，HTTP 服务不会启动，也不会回退到字符
数量估算。

## DeepSeek Key

服务先读取 `DEEPSEEK_API_KEY`，空值时读取被 Git 忽略的
`config/local.json`：

```json
{"deepseekApiKey":"your-key"}
```

也可以用 `CC_AGENT_LOCAL_CONFIG` 指定另一个本地 JSON 文件。

## MCP Server

`config/mcp_servers.json` 保存 MCP Server 名称、命令和参数。网页选中 Server
后，`MCPServerManager` 启动进程、完成 MCP 初始化、获取工具列表，并把每个
动态工具注册进同一个 `tool.Registry`。增加 MCP Server 不修改 `Agent.Run`。

## WebAgent 页面

打开项目后，页面调用：

- `GET /api/conversations?workingDirectory=...`：显示当前项目全部会话。
- `GET /api/conversations/{id}?workingDirectory=...`：显示选中会话的历史记录。
- `GET /api/logs?limit=200`：显示 `logs/server.jsonl` 最近的脱敏 JSON 日志。
- `GET /api/mcp/servers`：显示 MCP Server、运行状态和已注册工具数量。

页面不会要求用户填写会话编号。点击“新建会话”后，第一次执行由 Go 创建
编号。MCP 请求失败时页面显示实际 HTTP 状态和后端错误 JSON；如果 Go 服务
未运行，页面明确显示连接失败。

## 运行与验证

```text
go run .
go fmt ./...
go build ./...
go vet ./...
go test ./...
```

打开 `http://localhost:8080/`，填写项目绝对目录并点击“加载项目”。左侧显示
该目录的全部会话和 MCP Server；中间显示会话历史、当前任务和结果；右侧显示
本次 Agent 事件及服务端 JSON 日志。

项目看板：[GitHub Project #1](https://github.com/users/nhh37740-glitch/projects/1/views/1)。
完整版本顺序见 [ROADMAP.md](ROADMAP.md)，实际文件和函数见
[PROJECT_INDEX.md](PROJECT_INDEX.md)。
