# v11 Development Notes

v11 是当前狼人杀版本，入口为 `democode/v11/main.go`，Web 页面为 `werewolf.html`。本文件记录现有结构、状态机、提示词和维护规则，避免后续把新玩法继续塞进 v11。

## Directory Map

- `main.go`: HTTP 路由、SSE 编排、狼人杀游戏会话、主持人命令解析兜底、局域网地址输出。
- `game/`: 狼人杀纯规则状态机，包含角色分配、阶段推进、夜晚结算、投票、猎人开枪和胜负判断。
- `service/`: DeepSeek/Anthropic Messages API 调用、流式解析、会话存储、房间消息过滤、狼人杀角色提示词组装。
- `model/`: 聊天 API、工具调用、游戏行动等共享数据结构。
- `personalities/werewolf/`: 狼人杀身份人格、公开角色人设和语言提示词。
- `static/`: BGM 和 `i18n-werewolf.json`。
- `werewolf.html`: 狼人杀主界面，负责角色选择、SSE 消费、输入锁定、私密可见性、多语言、移动端布局和 BGM 控制。
- `logs/`、`data/sessions/`: 本地运行时输出，不是核心逻辑。

## Werewolf Flow

标准局为 8 人：

- 2 狼人
- 1 预言家
- 1 女巫
- 1 猎人
- 3 村民

主要流程：

1. `NewWerewolfWithRole` 创建局面，按玩家偏好把人类玩家换到指定真实身份。
2. `PhaseIntro` 公开入场发言。
3. 夜晚狼人阶段，狼人私密讨论并选择袭击目标。
4. 预言家阶段，预言家私密查验一名存活玩家。
5. 女巫阶段，女巫根据夜晚目标选择救人或毒人。
6. 天亮结算死亡，公开夜晚公告。
7. 白天讨论，存活玩家轮流发言。
8. 白天投票，票数最高者出局；若猎人被放逐，可触发开枪。
9. `CheckWin` 判断狼人或好人阵营胜利，否则进入下一夜。

`game.State` 是规则事实来源。Web 上看到的公开人设、角色名字和 UI 文案不能反向决定真实身份。

## LLM Chain

v11 有三类 LLM 使用：

- AI 角色发言：`gameSession.callAgentText` 读取狼人杀身份人格、公开角色人设、语言提示词、当前阶段提示和可见历史。
- 主持人命令解析：`parseActionViaLLM` 优先把自然语言提取为 `{action,target}`，失败后使用本地解析兜底。
- 预言家记忆更新：查验后用 LLM 汇总私有记忆，失败后本地合并。

语言控制来自 `personalities/werewolf/languages/*.md`。新增语言时优先增加提示词文件和前端 i18n，不要把语言规则硬编码进角色逻辑。

## Web Interaction

`GET /werewolf` 返回 `werewolf.html`。

`GET /api/werewolf/stream` 启动一局并返回 SSE。常见 frame：

- `role`: 人类玩家真实身份、阵营、同伴等私有开局信息。
- `state`: 公开玩家状态、阶段、轮次。
- `phase`: 当前阶段、行动类型、候选目标。
- `speech`: 角色或系统发言，可带 `visibleTo` 控制私密可见范围。
- `prompt`: 解锁人类输入，带阶段、候选和私密标记。
- `announce`: 白天、投票、死亡等公开公告。
- `reveal`: 终局公开真实身份。
- `done`: 游戏结束。
- `error`: 流式错误。

`POST /api/werewolf/input` 把人类玩家输入送入当前会话的 `humanInput` channel。前端必须只在收到 `prompt` 后解锁输入，提交后立即锁定，等待下一次 `prompt`。

Web 已包含：

- PC/移动端布局。
- 私密发言可见范围处理。
- 角色选择、狼队私语轮数、温度、语言选择。
- BGM 和本地 i18n。
- 对话区独立滚动。

## Running And Testing

从 `democode/v11` 目录运行：

```powershell
go test ./...
go build
.\v11.exe
```

也可以从仓库根目录运行：

```powershell
go test ./democode/v11/...
go build ./democode/v11
```

默认服务地址：

- `http://localhost:8080/werewolf`
- 启动日志会打印可用于手机访问的 LAN 地址。

## Maintenance Rules

- 不要硬编码角色真实身份。真实身份只来自 `game.State`。
- 不要硬编码狼人同伴或特殊角色结果。通过 `WolfGroup`、`PlayerRole`、`PlayerTeam` 等查询。
- 主持人命令解析优先 LLM，只有失败或无动作时才使用本地兜底。
- 提示词文件优先于代码模板。角色风格、语言约束和公开人设应放在 `personalities/werewolf/`。
- 私密信息必须通过 `visibleTo` 控制，不能写进公开公告或所有玩家共享的 prompt。
- `game/` 保持纯规则层，不引入 HTTP、LLM 或 Web 文案。
- v11 只维护狼人杀。新剧本杀、跑团或其他玩法应新建版本目录。

## Known Limits

- 只有单个全局 `activeGame`，同一进程不支持多房间并发。
- LLM 失败时部分 AI 发言可能为空或依赖本地兜底。
- 命令解析仍可能受模型输出影响，因此所有结构化行动都必须再经过候选集合和规则校验。
- 会话日志和历史数据是本地开发辅助，不是稳定存档格式。

