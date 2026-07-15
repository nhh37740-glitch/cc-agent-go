# cc-agent-go

## 项目目的

用 Go 重写 Java 版 `cc-agent`，在功能逐步对齐的过程中学习 Go，并构建一个可验证、可追踪的 AI Agent HTTP 服务。

本项目面向有 Java 背景、正在入门 Go 的开发者：每个版本只引入一组新的 Go 概念，代码必须可以编译、运行和验证。

## 当前进度

- 已完成：Demo v0–v10；v10 核心 Agent 已迁移至仓库根目录
- 当前主线：v11 Agent Trace、结构化日志、错误分类与运行回放
- 后续路线：Eval → Guardrails/HITL → MCP → Durable Workflow → A2A
- 狼人杀实验仅保留在 `feature/v11-werewolf` 分支，不进入主线 Agent 服务
- 项目看板：[cc-agent-go Project](https://github.com/users/nhh37740-glitch/projects/1/views/1)
- 详细路线：[ROADMAP.md](ROADMAP.md)

## 技术约束

- Go 标准库实现，不引入第三方依赖
- `net/http` 提供 HTTP 与 SSE 服务
- JSON 文件保存会话数据
- `log/slog` 输出结构化日志

详细的协作与教学规则见 [AGENTS.md](AGENTS.md)。
