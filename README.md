# cc-agent-go

## 项目目的

用 Go 重写 Java 版 `cc-agent`，在功能逐步对齐的过程中学习 Go，并构建一个可验证的 AI Agent HTTP 服务。

本项目面向有 Java 背景、正在入门 Go 的开发者：每个版本只引入一组新的 Go 概念，代码必须可以编译、运行和验证。

## 当前进度

- 已完成：Demo v0–v10；v10 核心 Agent 已迁移至仓库根目录
- 已完成：v11 结构化日志、错误分类与统一错误返回
- 已完成：v12 可配置的 MCP Client、Playwright MCP 和动态工具注册
- 已跳过：v13 RAG 与资料检索（未实现，计划已归档）
- 已完成：v14 通用型 SubAgent 工具（固定 JSON 输入输出、配置并行数、最多 5 个；真实 DeepSeek 与 Playwright MCP 检查通过）
- 当前主线：v15 后台 SubAgent 回调；已完成长短连接分离，取消、重试和恢复尚未实现
- 后续路线：长任务恢复 → A2A
- 狼人杀实验仅保留在 `feature/v11-werewolf` 分支，不进入主线 Agent 服务
- 项目看板：[cc-agent-go Project](https://github.com/users/nhh37740-glitch/projects/1/views/1)
- 详细路线：[ROADMAP.md](ROADMAP.md)
- v15 当前实施计划：[OpenSpec v15-background-subagent-callback](openspec/changes/v15-background-subagent-callback/tasks.md)

## 技术约束

- Go 标准库实现，不引入第三方依赖
- `net/http` 提供 HTTP 与 SSE 服务
- JSON 文件保存会话数据
- `log/slog` 输出结构化日志
- Go 标准库实现 MCP 2025-11-25 STDIO Client
- MCP Server 列表由 `config/mcp_servers.json` 维护

## 服务日志

Go 服务每次启动时自动创建 `logs/server.jsonl`。每条 `slog` JSON 同时写入 stderr 和该文件。日志不记录用户正文、模型回复、工具参数或 API Key。

## 后台 SubAgent 回复

`POST /api/chat/stream` 只处理一条用户消息，回复完成后固定关闭。网页同时为当前会话建立 `GET /api/conversations/{conversationId}/events` 长连接。`run_subagent` 启动后台任务后立即返回；全部 SubAgent完成时，Go 回调主 Agent，并通过会话事件长连接推送主 Agent的新回复。

## DeepSeek Key 配置

正式服务按下面的顺序读取 DeepSeek Key：

1. 读取 `DEEPSEEK_API_KEY` 环境变量。
2. 环境变量为空时，读取 `config/local.json` 的 `deepseekApiKey`。

本地文件格式参考 `config/local.example.json`。真实的 `config/local.json` 已加入 `.gitignore`，不会提交到 GitHub。需要从其他路径读取时，可用 `CC_AGENT_LOCAL_CONFIG` 环境变量指定文件路径。

## MCP Server 配置

`config/mcp_servers.json` 保存 MCP Server 名称、命令和参数。增加一台 STDIO MCP Server 时编辑这个 JSON 并重启 Go 服务，不需要修改或重新编译 Go 代码。

MCP 标准消息位于 `mcp/protocol/2025-11-25/messages.json`。同目录 `schema.json` 来自 [MCP 官方 2025-11-25 Schema](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/2025-11-25/schema/2025-11-25/schema.json)。

当前配置包含 Playwright MCP `0.0.78`，使用 `--image-responses omit`，不向模型返回截图。

详细的协作与教学规则见 [AGENTS.md](AGENTS.md)。
