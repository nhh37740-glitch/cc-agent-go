# V7 前端更新需求

## 背景

`democode/v1/index.html` 是当前前端页面，为 v1 后端设计。v7 后端新增了会话持久化（conversationId）、会话管理 API、以及全新的 SSE 流式协议。前端需要适配这些变更。

---

## 1. 流式 SSE 协议变更（高优先级）

### 1.1 当前前端行为（v1）

- 发送 `GET /api/chat/stream?message=...`
- 解析 SSE 帧：`data: {"reply": "字"}` → 提取 `.reply` 字段
- 检测 `data: [DONE]` 作为流结束信号

### 1.2 v7 后端实际行为

**请求方式**：`POST /api/chat/stream`，请求体 `{"message": "...", "conversationId": "..."}`（conversationId 可选，空字符串表示新会话）

**SSE 帧序列**：

| 序号 | SSE 帧内容 | 含义 |
|:---|:---|:---|
| 1 | `data: {"type":"conversation_id","conversationId":"uuid"}` | 会话 ID（首帧） |
| 2..N | `data: "字符串token"` | 逐 token 推送（纯 JSON 字符串，非 `{"reply":"..."}` 格式） |
| N+1 | `data: {"type":"done","text":"完整回复文本"}` | 流结束 |
| 异常 | `data: {"type":"error","message":"错误信息"}` | 错误通知 |

**注意**：如果 token 内容以 `{` 开头（即 token 本身是 JSON），后端直接输出原始 JSON：`data: {"key":"value"}`。前端需要根据首字符区分：以 `{` 开头的是 JSON 对象，否则是 JSON 字符串。

### 1.3 需要的变更

#### 1.3.1 请求方式

- 从 `GET + QueryString` 改为 `POST + JSON Body`
- Body 增加 `conversationId` 字段（首次对话传空字符串 `""`）

#### 1.3.2 SSE 解析逻辑重写

```
收到 data: 行后：
  1. JSON.parse(dataStr)
  2. 判断类型：
     - typeof parsed === "string" → 这是 token 文本，追加到累积文本
     - parsed.type === "conversation_id" → 保存 parsed.conversationId，不追加到文本
     - parsed.type === "done" → 流结束，最终文本是 parsed.text（可选，也可用累积文本）
     - parsed.type === "error" → 显示错误信息
  3. 删除旧的 `data: [DONE]` 检测逻辑
```

#### 1.3.3 前端状态新增

- `state.currentConversationId`：当前对话的会话 ID（首次收到 `conversation_id` 帧时设置）
- 后续对话可携带此 ID 实现上下文延续

---

## 2. 非流式 POST /api/chat 变更（中优先级）

### 2.1 当前行为

- Request: `{"message": "..."}`
- Response: `{"reply": "..."}`

### 2.2 v7 行为

- Request: `{"message": "...", "conversationId": "..."}`（conversationId 可选）
- Response: `{"conversationId": "uuid", "reply": "..."}` 

### 2.3 需要的变更

- Request body 增加 `conversationId` 字段
- Response 解析增加 `conversationId` 提取
- 获取到 conversationId 后保存到 `state.currentConversationId`

---

## 3. 会话管理 API 新增（中优先级）

v7 新增三个会话管理端点，前端应增加对应的 UI 交互：

### 3.1 API 清单

| 方法 | 路径 | 请求/响应 |
|:---|:---|:---|
| `GET` | `/api/conversations` | 返回 `[{conversationId, title, updatedAt, currentWindowTokens, ...}]` |
| `GET` | `/api/conversations/{id}` | 返回完整 SessionJson（含 messages 数组） |
| `DELETE` | `/api/conversations/{id}` | 返回 204 No Content |

### 3.2 需要的 UI 变更

#### 3.2.1 会话列表侧边栏

- 左侧或顶部增加"历史会话"入口
- 调用 `GET /api/conversations` 获取列表
- 按 `updatedAt` 降序展示（后端已排好序）
- 每个会话项显示：标题（截断前 20 字）、更新时间、token 用量

#### 3.2.2 加载历史会话

- 点击历史会话 → 调用 `GET /api/conversations/{id}`
- 将返回的 `messages` 数组渲染到聊天面板
- 设置 `state.currentConversationId = id`

#### 3.2.3 新建/切换会话

- "新建对话"按钮 → 清空聊天面板 + 重置 `conversationId` 为空字符串
- 切换会话 → 保留当前 conversationId 不变（继续在同一会话中对话）

#### 3.2.4 删除会话

- 每个会话项提供删除按钮
- 调用 `DELETE /api/conversations/{id}`
- 删除后刷新列表；如果删除的是当前活跃会话，清空聊天面板

---

## 4. 模拟模式更新（低优先级）

### 4.1 模拟 SSE 流格式

当前模拟模式输出的 SSE 格式：
```
data: {"reply": "字"}
data: [DONE]
```

需要改为 v7 格式：
```
data: {"type":"conversation_id","conversationId":"mock-uuid-..."}
data: "字"
data: {"type":"done","text":"完整模拟文本"}
```

### 4.2 模拟非流式响应格式

当前：`{"reply": "..."}`
改为：`{"conversationId": "mock-uuid-...", "reply": "..."}`

---

## 5. Go 代码示例更新（低优先级）

右侧面板"代码实物对照"中的 Go 后端示例代码需要更新，反映 v7 的实际架构：
- 会话存储（`service/store.go`）
- Agent 循环（`service/agent.go`）
- 新增的会话管理路由（`GET/DELETE /api/conversations`）

---

## 6. 不变量（无需改动）

- `GET /` 路由行为不变
- Tailwind CSS + Marked.js 依赖不变
- 整体 UI 布局结构不变
- 沙箱标签页的 Header/Body 构建逻辑基本不变

---

## 7. 实现优先级建议

| 优先级 | 模块 | 说明 |
|:---|:---|:---|
| **P0** | SSE 流式解析 | 核心功能，不修则流式聊天完全不可用 |
| **P1** | conversationId 传递与保存 | 不修则无法跨请求延续上下文 |
| **P2** | 非流式响应解析 | 不修则 reply 字段提取失败 |
| **P3** | 会话列表 UI | 新增功能，可后续迭代 |
| **P4** | 模拟模式 + 代码示例 | 教学辅助功能 |

---

## 8. 关键技术点

### 8.1 SSE token 帧解析（最关键）

v7 的 token 帧是 `data: "你好"` —— 注意这是一个 **JSON 字符串**（带引号），不是 `data: 你好`（裸文本）。前端 JSON.parse 后得到 Go 的 `"你好"`（即 JavaScript 的 `"你好"` 字符串），直接使用即可。

但如果 token 以 `{` 开头（browser 工具调用 JSON），后端会输出 `data: {"name":"bash",...}`。此时 JSON.parse 得到的是一个对象。代码需要区分这两种情况：

```javascript
const parsed = JSON.parse(dataStr);
if (typeof parsed === "string") {
    // 普通文本 token
    accumulatedText += parsed;
} else if (parsed.type === "conversation_id") {
    state.conversationId = parsed.conversationId;
} else if (parsed.type === "done") {
    // 流结束
} else if (parsed.type === "error") {
    // 错误
} else {
    // 裸 JSON 对象（工具调用等）
    accumulatedText += JSON.stringify(parsed);
}
```

### 8.2 聊天配置面板更新

左侧齿轮面板中的路径选项需要更新：
- 流式路径从 `GET /api/chat/stream?message=...` 改为 `POST /api/chat/stream`
- 请求方法下拉框对流式模式固定为 POST

---

## 9. 验证清单

- [ ] 真机联调模式下，发送消息 → 流式逐字渲染正常
- [ ] conversationId 在首次回复后正确保存
- [ ] 第二次发送消息时携带同一个 conversationId → 后端延续上下文
- [ ] 错误 SSE 帧能正确显示错误提示
- [ ] 会话列表能正确展示
- [ ] 点击历史会话能加载并显示历史消息
- [ ] 删除会话功能正常
- [ ] 模拟模式仍然可用（格式适配 v7）
- [ ] 沙箱手写请求功能正常
