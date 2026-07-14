# 元老院前端需求

## 背景

v9 后端提供 `POST /api/council/stream` SSE 流式辩论端点 + `POST /api/council` 非流式端点 + `GET /council` 静态页面。

前端页面需独立设计（和 v1 教学沙箱风格不同），古典罗马主题。

---

## 1. SSE 流式协议

### 请求

```json
POST /api/council/stream
{"topic": "议题文本", "maxRounds": 3, "interruption": ""}
```

### SSE 帧序列

| 序号 | 帧 | 含义 |
|:---|:---|:---|
| 1 | `data: {"type":"topic","text":"议题文本"}` | 辩论议题 |
| 2 | `data: {"type":"round","round":1}` | 第 N 轮开始 |
| 3 | `data: {"type":"speech","round":1,"agent":"加图","text":"..."}` | 某人发言 |
| ... | （重复 2-3，按轮次 × agent 数） | |
| N | `data: {"type":"done"}` | 辩论结束 |

---

## 2. 页面布局

```
┌──────────────────────────────────────────────────┐
│  🏛️ 元老院 · Senatus Populusque Romanus       │
├──────────────────────────────────────────────────┤
│  议题输入区                                      │
│  [输入议题________________________________]      │
│  轮数: [3▼]  [🏛️ 召集元老]                     │
├──────────────────────────┬───────────────────────┤
│  辩论记录（实时滚动）     │  公民发言区           │
│                          │  [输入插话________]   │
│  ▬▬ 第 1 轮 ▬▬          │  [📢 向元老院陈情]   │
│  ┌─ 🏷️ 加图 ──────────┐│                       │
│  │ 传统是罗马的根基...  ││  统计                  │
│  └─────────────────────┘│  token: 4500          │
│  ┌─ 🏷️ 凯撒 ──────────┐│  轮数: 2/3            │
│  │ 改革势在必行...      ││                       │
│  └─────────────────────┘│                       │
│  ┌─ 🏷️ 西塞罗 ────────┐│                       │
│  │ 让我们求同存异...    ││                       │
│  └─────────────────────┘│                       │
│  ▬▬ 第 2 轮 ▬▬          │                       │
│  ...                     │                       │
├──────────────────────────┴───────────────────────┤
│  状态: 辩论中 / 辩论结束                          │
└──────────────────────────────────────────────────┘
```

---

## 3. 技术实现要点

### 3.1 SSE 帧解析

```javascript
// 逐行解析 SSE data: 帧
const parsed = JSON.parse(dataStr);
switch (parsed.type) {
  case "topic":  // 显示议题标题
  case "round":  // 渲染轮次分隔
  case "speech": // 渲染发言气泡（按 agent 名区分颜色）
  case "done":   // 辩论结束
}
```

### 3.2 发言气泡颜色映射

预定义调色板，按 agent 名分配（同名总是同色）：

```javascript
const agentColors = [
  { bg: 'bg-rose-50', border: 'border-rose-200', text: 'text-rose-800', badge: 'bg-rose-100 text-rose-700' },
  { bg: 'bg-amber-50', border: 'border-amber-200', text: 'text-amber-800', badge: 'bg-amber-100 text-amber-700' },
  { bg: 'bg-sky-50',   border: 'border-sky-200',   text: 'text-sky-800',   badge: 'bg-sky-100 text-sky-700' },
  // ...循环使用
];
const color = agentColors[index % agentColors.length];
```

### 3.3 插话流程

1. 辩论进行中，右侧输入插话内容 → 点"陈情"
2. 发新请求 `POST /api/council/stream`，`interruption` 带内容
3. 当前轮次完成后 agent 们看到插话
4. 页面在辩论记录中显示 "【公民】" 插话气泡

### 3.4 视觉风格

- **大理石主题**：象牙白底（`bg-stone-50`），金色/青铜色装饰线
- **字体**：标题用 Georgia / serif 衬线字体
- **卡片**：白色背景 + 细微阴影 + 左侧彩色边条区分发言者
- 不依赖 marked.js（元老发言不需要 Markdown）
- 技术栈：纯 HTML + Tailwind CDN + 原生 JS

---

## 4. 文件位置

`democode/v9/index.html`
