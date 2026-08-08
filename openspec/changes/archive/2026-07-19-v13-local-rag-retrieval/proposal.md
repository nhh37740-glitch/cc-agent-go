## Why

> 归档说明（2026-07-19）：本版本未实现。用户决定跳过 RAG，把学习时间转到 v14 通用型 SubAgent；30 个实施任务均未执行，delta spec 不同步到正式 specs。

当前 Agent 只能依靠模型已有知识和现有工具回答，不能从 `workspace/knowledge/` 中找到用户保存的 `.md`、`.txt` 原文。v13 要增加一个本地资料搜索工具，让 Agent 能把实际命中的文件、行号和原文交给模型，并让最终回答写出实际使用的来源。

## What Changes

- 新增 `workspace/knowledge/` 资料目录，只读取其中的 `.md` 和 `.txt` 文件。
- Go 服务启动时创建本地资料索引；每次搜索前检查文件变化，新增或修改资料不需要重新编译 Go 服务。
- 将资料按行和字符数量分段，保存相对文件名、起始行、结束行和原文。
- 使用 Go 标准库完成中英文搜索词处理和 BM25 初次排序，不安装向量数据库或第三方 Go 包。
- 使用 DeepSeek 对 BM25 候选结果进行重排；重排调用失败时返回 BM25 前几项，并明确写出 `rerankApplied: false`。
- 在现有 `tool.Registry` 注册普通工具 `search_local_documents`。Agent 调用工具时传入 `query` 和 `resultCount`，工具返回来源编号、文件名、行号、原文和分数。
- 更新 Agent 的 system prompt：只有实际使用资料搜索结果时才写来源，并使用 `[source:<文件>:<起始行>-<结束行>]` 格式。
- 不增加 HTTP 路由，不修改网页，不读取 PDF，不做 OCR、文件上传、向量 embedding 或向量数据库。

## Capabilities

### New Capabilities

- `local-document-retrieval`: 读取本地文本资料、分段、BM25 搜索、DeepSeek 重排、注册 Agent 搜索工具，并把可核对的文件位置交给最终回答。

### Modified Capabilities

无。

## Impact

- 新增正式代码目录 `rag/` 和资料目录 `workspace/knowledge/`。
- 修改 `main.go`：创建资料索引，并把 `search_local_documents` 注册到现有 `tool.Registry`。
- 修改 `service/`：增加 DeepSeek 重排函数；现有 `service.Run` 和 `service.RunStream` 函数签名保持不变。
- 修改 system prompt、`ROADMAP.md`、`PROJECT_INDEX.md`、`README.md` 和 `AGENTS.md`，记录 v13 的实际文件和调用顺序。
- `go.mod` 继续保持零第三方依赖；现有聊天和 MCP HTTP 接口保持不变。
