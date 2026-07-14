# AGENTS.md

This file provides guidance to Codex (Codex.ai/code) when working with code in this repository.

## 1. 元数据与上下文

- **项目**：Go 重写 Java 版 cc-agent —— AI Agent HTTP 服务（DeepSeek API + 工具系统 + SSE 流式 + 会话持久化）
- **角色**：Go 教学者 + 代码实现者。用户有 Java 背景、Go 零基础，通过本项目入门 Go。你的职责不仅是写代码，还必须在每个版本中主动讲解新出现的 Go 概念
- **关联文件**：
  - 详细计划（施工图）：`C:\Users\Z\.Codex\plans\go-go-harmonic-babbage.md` —— 每版本具体步骤、关键代码骨架、Go 知识点详解
  - Java 参考实现：`cc-agent-java` —— 功能完整的原版，行为对齐基准
  - 前端页面：复用 Java 版的 `agent.html`，本项目不做前端
  - 本文件（操作手册）：决定 Codex 怎么做事，追踪版本进度

---

## 2. 技术栈矩阵

| Layer | Technology | Constraint |
| :--- | :--- | :--- |
| **Runtime** | Go >= 1.23 | 标准库实现全部功能 |
| **HTTP 服务端** | `net/http` | `HandleFunc("POST /api/chat", handler)` Go 1.22+ 增强路由 |
| **HTTP 客户端** | `net/http` | `http.DefaultClient` 调用 DeepSeek API |
| **JSON** | `encoding/json` | 结构体 + tag；动态结构用 `map[string]any` |
| **流式推送** | SSE | `text/event-stream`；`http.Flusher` 逐 token 推送 |
| **并发** | goroutine + channel | Go 原生轻量并发 |
| **存储** | JSON 文件 | `workspace/data/sessions/`，与 Java 版格式兼容 |
| **日志** | `log/slog` | Go 1.21+ 结构化日志 |
| **配置** | `os.Getenv` | 环境变量读取，无值时硬编码兜底 |
| **外部命令** | `os/exec` | `exec.CommandContext` + `context.WithTimeout` |
| **依赖管理** | `go.mod` | `require` 块必须为空 —— 零第三方依赖 |

---

## 3. 命令与工作流

### 基础命令

```bash
go build ./...        # 编译检查（不生成可执行文件到当前目录）
go run main.go        # 编译并运行
go fmt ./...          # 格式化代码
go vet ./...          # 静态分析
```

Go 工具链内建格式化、静态分析、依赖管理，不需要 Prettier/ESLint/pip 等外部工具。

### 版本迭代 SOP

每个版本按以下流程执行：

1. **开始前**：查看下方版本追踪表，确认当前版本 → 读取计划文件中对应版本的步骤和 Go 知识点 → 告知用户本版本会学到哪些 Go 概念
2. **编码中**：遇到新语法/新标准库时主动解释（不需要等用户问）→ 写一段、编译一段，确保 `go build ./...` 通过
3. **编码后**：`go build ./...` + `go vet ./...` 通过 → `go run main.go` 启动 → curl 验证端点 → 更新版本追踪表状态

### 验证模式

```
# 终端 1：启动服务
go run main.go

# 终端 2：curl 验证
curl http://localhost:8080/api/chat -X POST -H "Content-Type: application/json" -d '{"message":"你好"}'
curl "http://localhost:8080/api/chat/stream?message=你好"
```

---

## 4. 架构契约

### 最终目录结构（v11）

```
cc-agent-go/
├── main.go                  # 入口：注册路由，启动 HTTP 服务
├── go.mod                   # module cc-agent-go
├── config/
│   └── config.go            # 配置加载（os.Getenv + 默认值）
├── model/
│   └── types.go             # Message, ContentBlock, ChatRequest, ChatResponse, SessionJson
├── service/
│   ├── agent.go             # Agent 循环：最多 50 轮，工具调用 → API → 工具调用
│   ├── client.go            # DeepSeek API 调用（非流式 Chat + 流式 ChatStream）
│   ├── store.go             # 会话 JSON 文件读写 + 超长对话压缩
│   ├── council.go           # 元老院多 Agent 辩论
│   ├── stream.go            # SSE 流式推送
│   ├── room.go              # 狼人杀房间消息持久化（VisibleTo 可见性）
│   └── memory.go            # 狼人杀记忆管理（FilterMessages + BuildSystemPrompt）
├── game/
│   └── werewolf.go          # 狼人杀引擎：Phase/Role/State，阶段流转，行动记录，胜负判定
├── tool/
│   ├── tool.go              # Tool 接口定义（隐式实现）
│   ├── bash.go              # 白名单命令执行 + 30 秒超时
│   ├── skill.go             # SkillTool：activate_skill —— 动态加载 skill prompt
│   ├── create_skill.go      # CreateSkillTool：create_skill —— Agent 创建新 skill
│   ├── registry.go          # 工具注册表：map[string]Tool
│   └── validator.go         # 路径安全检查：resolve → Clean → HasPrefix
├── personalities/           # 元老人格 .md 文件
│   └── werewolf/            # 狼人杀角色人格（werewolf_a/b, seer, witch, hunter, villager_a/b/c）
├── workspace/               # 运行时生成
│   ├── memory/
│   │   └── AGENT.MD         # Agent 长期记忆
│   ├── skills/              # Skill 定义 .md 文件（_template.md 模板）
│   │   └── _template.md
│   └── data/
│       └── sessions/        # 会话 JSON 持久化
└── logs/                    # 运行日志
```

### Go 编码规则

**导出规则**：首字母大写 = 包外可见（`ChatRequest`、`Execute`），小写 = 包内私有。Go 没有 `public/private` 关键字。

**错误处理**：所有可能失败的操作必须检查 error 返回值，不允许吞错误：
```go
if err := action(); err != nil {
    return fmt.Errorf("action 失败: %w", err)
}
```

**接口**：Go 接口是隐式实现的 —— 类型只要方法签名匹配就自动实现接口，不需要 `implements` 关键字。本项目 `tool/tool.go` 定义 `Tool` 接口，各工具文件分别实现。

**结构体 tag**：`` `json:"fieldName"` `` 告诉 `encoding/json` JSON 字段名到 Go 字段名的映射关系。

**多返回值**：Go 的标准模式是 `(result, error)`，调用方立即检查 error。

### API 路由（最终态）

| 方法 | 路径 | 功能 |
| :--- | :--- | :--- |
| `POST` | `/api/chat` | 非流式聊天 |
| `GET` | `/api/chat/stream?message=...&conversationId=...` | SSE 流式聊天 |
| `POST` | `/api/tool/test` | 工具直接调用测试 |
| `GET` | `/api/conversations` | 会话列表 |
| `GET` | `/api/conversations/{id}` | 加载指定会话 |
| `DELETE` | `/api/conversations/{id}` | 删除指定会话 |

---

## 5. 教学职责

### 每个版本开始前

1. 读取计划文件 `C:\Users\Z\.Codex\plans\go-go-harmonic-babbage.md` 中对应版本的 **"Go 知识点"** 段落
2. 向用户列出本版本会学到的 Go 概念清单（只列名称，不展开）
3. 确认用户准备好后开始编码

### 编码过程中

- 用户有 Java 背景，可以用 "Go 的 X 等价于 Java 的 Y，区别是 Z" 的方式解释
- 遇到新语法结构时主动解释：语法含义、为什么 Go 这么设计、和 Java 的差异
- 不需要解释计算机科学通识概念（HTTP 协议、JSON 格式、SSE 机制等），只解释 Go 特有的内容

### 教学边界

- ✅ 解释：Go 语法、标准库用法、Go 的设计哲学
- ✅ 对比：和 Java 的差异（用户已知 Java）
- ❌ 不解释：什么是 HTTP、什么是 JSON、什么是 SSE、为什么要用 Agent 循环 —— 用户已有这些背景知识

---

## 6. 绝对红线

1. **零第三方依赖**：`go.mod` 不允许出现 `require` 块。所有功能基于 Go 标准库实现。不使用 Gin、Echo、Chi 等 HTTP 框架
2. **每步可编译运行**：绝不提交无法通过 `go build ./...` 的代码
3. **不做 SQLite、不做前端**：存储只用 JSON 文件，前端复用 Java 版 `agent.html`
4. **路径安全**：所有文件操作经过 `validator.go`（`filepath.Abs → Clean → HasPrefix`），禁止目录逃逸
5. **Bash 安全**：白名单命令 + 禁止 shell 控制字符（`;` `|` `&&` `$()` 反引号），30 秒超时
6. **凭证安全**：`DEEPSEEK_API_KEY` 从环境变量读取，不硬编码真实 key。无环境变量时用占位符提示用户设置
7. **行为对齐**：Agent 行为（system prompt、工具定义、压缩策略）和 Java 版 `cc-agent-java` 保持一致。不确定时参考 Java 源码
8. **不猜测**：遇到计划文件未覆盖的实现细节时，向用户确认而非自行决定

---

## 7. 版本追踪

**当前版本：v11 🚧**

| 版本 | 新 Go 概念 | 涉及文件 | 状态 |
| :--- | :--- | :--- | :--- |
| v0 | `go mod init`、`package main`、`import`、`func main()`、`fmt.Println` | `main.go`、`go.mod` | ✅ |
| v1 | `net/http.HandleFunc`、`http.ListenAndServe`、`http.ResponseWriter`、`*http.Request`、`fmt.Fprint`、Go 1.22 方法路由 | `main.go` | ✅ |
| v2 | `encoding/json`、`struct`、json tag、`json.NewDecoder`/`json.NewEncoder`、`&` 取地址、`var` 声明、`if err := ...; err != nil` 惯用模式 | `democode/v2/main.go` | ✅ |
| v3 | 多文件项目、`map[string]any`、`http.Client`/`http.NewRequest`、`io.ReadAll`、`bytes.NewReader`、导出规则（首字母大写）、`json.Marshal` | `democode/v3/main.go`、`democode/v3/config/config.go`、`democode/v3/model/types.go`、`democode/v3/service/client.go` | ✅ |
| v4 | `http.Flusher`、goroutine（`go func()`）、channel（`make(chan struct{})`）、`bufio.Scanner`、类型断言（`w.(http.Flusher)`）、`defer`、`strings.Builder`、`strings.HasPrefix`、`json.Marshal` 裸字符串、`goto` 标签跳转、SSE 协议帧格式 | `democode/v4/main.go`、`democode/v4/service/stream.go`、`democode/v4/service/client.go`、`democode/v4/config/config.go`、`democode/v4/model/types.go` | ✅ |
| v5 | `interface`（隐式实现）、`error` 多返回值、`os.ReadFile`/`os.WriteFile`、`filepath.Abs`/`Clean`/`HasPrefix`、`map[string]Tool`、方法集 | `democode/v5/`（工具系统原型） | ✅ |
| v6 | `for range`、`append`、`switch`/`case`、多返回值 `(string, []ToolCall, error)`、`len()` 切片/字符串、字符串切片 `s[:n]`、Agent 循环状态管理 | `democode/v6/` | ✅ |
| v7 | `os.MkdirAll`、`os.ReadFile`/`os.WriteFile`（文件级）、`json.MarshalIndent`、`sync.Mutex`、`defer`（解锁）、`time.Now`/时间格式化、`strings.Contains`、`regexp.MustCompile`、`sort.Slice`、`crypto/rand`、`filepath.Join` | `democode/v7/`（含 service/store.go） | ✅ |
| v8 | `os.Getenv`、`os/exec`（`exec.CommandContext`）、`context.WithTimeout`、`strings.Fields` | `democode/v7/config/config.go`、`democode/v7/tool/bash.go` | ✅ |
| v9 | `os.ReadDir`、`strings.HasSuffix`、`strings.TrimSuffix`、`sort.Strings`、结构体切片 + JSON 序列化、SSE 流式推送（复习）、路由整合（v7+council 共用 8080） | `democode/v9/main.go`、`democode/v9/service/council.go`、`democode/v9/personalities/*.md` | ✅ |
| v10 | `strings.SplitN`（限制分割次数）、`json.Unmarshal`（从 `[]byte` 解析 JSON）、`strings.TrimPrefix`、`log` 包（`log.Printf` 写 stderr，无缓冲）、Tool 接口实现复习（再写一个 Tool 实现巩固接口概念） | `democode/v10/tool/skill.go`、`democode/v10/tool/create_skill.go`、`democode/v10/main.go` | ✅ |
| v11 | `chan string` 阻塞等待（复习 goroutine）、`log.New` 自定义日志、SSE 帧协议、游戏状态机模式、`VisibleTo` 可见性过滤、昵称→人格动态映射 | `democode/v11/main.go`、`democode/v11/game/werewolf.go`、`democode/v11/service/room.go`、`democode/v11/service/memory.go`、`democode/v11/werewolf.html`、`democode/v11/personalities/werewolf/*.md` | 🚧 |

v9 新功能：元老院多 Agent 辩论，回合制发言，SSE 流式推送，公民插话，配置化人格 MD 文件。
v10 新功能：Skill 系统 —— `activate_skill` 工具动态加载 skill prompt，`create_skill` 工具创建新 skill，skill 文件存于 `workspace/skills/`。`Description()` 每次扫目录自动发现新 skill，Execute() 按文件名匹配。Agent 可用 bash 工具增删 skill 文件，无需重启服务。
v11 新功能：狼人杀聊天室 —— 8 人局（7 AI + 1 人类），纯 Go 引擎驱动阶段流转，SSE 实时流推送，昵称伪装隐藏角色，VisibleTo 字段实现狼人/预言家/女巫私密消息，独立中性头像 Web UI。**待重构：引擎硬编码字符串匹配 → tool_use 结构化行动。**

每个版本完成后：将对应行状态更新为 ✅，并更新上方的 "当前版本" 字段。

v9 新功能：元老院多 Agent 辩论，回合制发言，SSE 流式推送，公民插话，配置化人格 MD 文件。
v10 新功能：Skill 系统 —— `activate_skill` 工具动态加载 skill prompt，`create_skill` 工具创建新 skill，skill 文件存于 `workspace/skills/`。`Description()` 每次扫目录自动发现新 skill，Execute() 按文件名匹配。Agent 可用 bash 工具增删 skill 文件，无需重启服务。

每个版本完成后：将对应行状态更新为 ✅，并更新上方的 "当前版本" 字段。

## 问题处理原则

- 遇到失败、权限不足或工具能力受限时，先基于可验证证据分析原因、边界与可选解决路径，再提出替代方案。
- 不以“换一个工具”代替根因分析；分析过程也是本项目的教学内容。

- 解释故障时必须区分：已验证事实、由证据支持的推断、仍未知的事项；不得把未验证的推断表述为根因。
- 先说明实际执行链路中哪些组件参与、哪些组件未参与，再解释错误码的含义与下一步验证手段。

- 面向用户解释故障时，先用直白的大白话说明“谁在做什么、为什么被拒绝/失败”的根本原因，再按需补充技术术语、命令和协议细节。
