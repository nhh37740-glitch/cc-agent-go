# CC Agent (Go)

用 Go 实现可复用的 AI Agent。当前主线不是聊天机器人，而是一个可以被
WebAgent、SubAgent 和其他应用共同调用的 `agent.Agent`。

当前服务代码在 `agent/`、`memory/`、`model/`、`tool/`、`service/`、
`harness/` 和根目录 `main.go`。`democode/` 是独立 Go 模块中的历史教学快照，
不会参与当前服务的 `go test ./...` 与发布构建。`scripts/check_boundaries.py`
检查核心包的依赖方向，防止 `agent/` 反向依赖 `service/` 或 `harness/`。

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
会话文件中的全部旧正文。需要旧信息时，DeepSeek 可以调用 `file` 工具
读取文件，或通过 `command` 工具运行白名单中的 `rg` 搜索必要片段。

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

## v16 Harness 编排层

Harness 自身是一个只持有 `agent` 和 `memory` 两个工具的编排型 Agent。
它负责创建被管理 Agent、派任务并回收结果：

- `agent` 工具：三参数（agent/request/forget），非阻塞启动，后台 goroutine
  执行；同名 Agent 拥有同一份持久记忆。
- 注册表：`agents.json` 落盘，`sync.RWMutex`，容量上限
  `MAX_HARNESS_AGENTS`（默认 11）。
- 完成队列：后台结束后自动入队，消费者逐条标记已收取后触发 Harness
  自调用汇报。
- 实况注入：每次 Harness 对话自动追加当前 Agent 名单、MCP Server 和已创建应用。
- `harness.html`：Agent 名单轮询、对话区 SSE、会话历史、Agent 详情逐 token 进度。

Harness API 路由：

- `POST /api/harness/chat/stream`
- `GET /api/harness/agents`
- `GET /api/harness/agents/{name}/memory`
- `GET /harness`

## 运行与验证

```text
go run .
go fmt ./...
go build ./...
go vet ./...
go test ./...
python scripts/release.py
```

`scripts/release.py` 依次检查依赖方向、运行测试和静态分析，然后生成
`dist/cc-agent-go`（Windows 上为 `.exe`）及 `dist/manifest.json`。清单记录
Git commit、commit tree、构建输入 SHA-256、构建工具版本及二进制 SHA-256。

## Docker 与 Jenkins

`Dockerfile` 用 Go 1.26.4 构建和测试服务，再把可运行二进制及必需的
HTML、prompt、配置、人格和 tokenizer 文件放入 Linux 运行镜像。
Linux 容器里的 `command` 工具直接调用白名单程序；Windows 本机仍调用
Windows 原生 `.exe`。不经过 shell。

```bash
docker build -t cc-agent-go:local .
docker run --rm -p 127.0.0.1:8081:8080 \
  -e DEEPSEEK_API_KEY \
  -v /srv/cc-agent-go/workspace:/workspace \
  -v /srv/cc-agent-go/logs:/app/logs \
  cc-agent-go:local
```

上面的端口映射仅供本机手动调试，监听宿主机回环地址。Jenkins 的常规构建
不会部署 WebAgent 或命令执行 API。

网页或 API 传入的 `workingDirectory` 应为容器内绝对路径，例如 `/workspace`。
不要将主机根目录或 Docker socket 挂载进容器。Jenkinsfile 在带 Docker 的
Linux 节点上验证依赖方向、测试、打包并归档二进制及清单，随后构建运行镜像。
流水线在无宿主机端口映射的临时容器里检查三个页面和只读 API，并发送
无效 JSON 验证普通聊天返回 400、流式聊天返回 SSE 错误事件；不会调用模型，
也不会发布 Agent 服务。
服务器约 2 GiB 内存时应让该 Jenkins 节点每次只执行一个镜像构建任务。
Playwright MCP 需要另行提供 Node.js 与浏览器运行环境，当前镜像未安装。

Jenkins 参数 `DeployDemo` 默认关闭。明确选中后，流水线先完成构建和无端口
映射的临时容器检查，再运行 `scripts/deploy_demo.py` 更新现有私有演示容器。
脚本只接受当前占用 `127.0.0.1:18101`、镜像名为 `cc-agent-go:<构建号>` 的容器；
先检查现有主页、三个页面/API 以及无效 JSON 的错误响应，然后读取并私密复制
现有容器的全部环境变量。只有内存 384 MiB、内存加交换空间 768 MiB、CPU
0.5 核、重启策略 `unless-stopped`、无挂载且端口仅绑定回环地址时才允许切换。
新容器的环境、资源限制、重启策略和端口必须与原容器一致，并通过同样的
页面/API 检查。任何检查失败都会重新启动原容器并检查恢复结果。成功后原容器
以 `-rollback-<构建号>` 后缀保留为停止状态。该入口仍应由服务器访问控制保护；
不要把命令执行 API 直接暴露给公网。

打开 `http://localhost:8080/`，填写项目绝对目录并点击“加载项目”。左侧显示
该目录的全部会话和 MCP Server；中间显示会话历史、当前任务和结果；右侧显示
本次 Agent 事件及服务端 JSON 日志。

打开 `http://localhost:8080/harness`，进入 Harness 编排页：左侧 Agent 名单，
中间对话与会话历史，右侧 Agent 进度与记忆。

项目看板：[GitHub Project #1](https://github.com/users/nhh37740-glitch/projects/1/views/1)。
完整版本顺序见 [ROADMAP.md](ROADMAP.md)，实际文件和函数见
[PROJECT_INDEX.md](PROJECT_INDEX.md)。
