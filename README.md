# cc-agent-go

## 项目目的

用 Go 重写 Java 版 `cc-agent`，在功能逐步对齐的过程中学习 Go，并构建一个可验证、可追踪的 AI Agent HTTP 服务。

本项目面向有 Java 背景、正在入门 Go 的开发者：每个版本只引入一组新的 Go 概念，代码必须可以编译、运行和验证。

## 当前进度

- 已完成：Demo v0–v10
- 进行中：v11 狼人杀聊天室，以及将硬编码行动重构为结构化 `tool_use`
- 项目看板：[cc-agent-go Project](https://github.com/users/nhh37740-glitch/projects/1/views/1)

## 技术约束

- Go 标准库实现，不引入第三方依赖
- `net/http` 提供 HTTP 与 SSE 服务
- JSON 文件保存会话数据
- `log/slog` 输出结构化日志

详细的协作与教学规则见 [AGENTS.md](AGENTS.md)。
