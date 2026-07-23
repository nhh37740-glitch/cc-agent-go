## Context

当前 `main.go` 创建全局 `registry = tool.NewRegistry()`。`main()` 把 Bash、Skill、CreateSkill 和 MCP 工具注册到这个工具表。`service.Run` 和 `service.RunStream` 每轮把 `registry.GetDefinitions()` 交给 DeepSeek；模型返回工具名称和参数后，两者调用 `registry.Execute(toolName, toolArguments)`，并把执行结果写入下一轮 `tool_result`。

v13 继续使用这条已经存在的执行顺序。它不增加新的聊天接口，也不把资料搜索直接写入 `service.Run`。它只向同一个 `tool.Registry` 增加一个普通工具 `search_local_documents`。

项目必须继续使用 Go 标准库，`go.mod` 不增加第三方依赖。资料只来自 `workspace/knowledge/`，日志不能记录用户问题或资料正文。

## Goals / Non-Goals

**Goals:**

- 用户把 `.md` 或 `.txt` 放入 `workspace/knowledge/` 后，Agent 能搜索实际原文。
- 每项结果都包含相对文件名、起始行、结束行和原文，最终回答可以让用户回到原文件核对。
- 使用 BM25 取得候选结果，再用 DeepSeek 按当前问题重排；DeepSeek 重排失败时仍返回本地搜索结果。
- 文件新增、修改或删除后，下一次搜索能读取变化，不需要重新编译或重启 Go 服务。
- 所有函数和变量名称说明其保存的内容或执行的动作。

**Non-Goals:**

- 不读取 PDF、Word、图片或网页，不做 OCR 和文件上传。
- 不生成 embedding，不安装向量数据库，不增加第三方 Go 包。
- 不增加新的 HTTP 路由或网页控件。
- 不保证模型一定采用每个搜索结果；只要求模型引用它实际采用的结果。
- 不把 v13 扩展成独立的 Agent 自动评测版本。

## Decisions

### 1. 把资料搜索注册成现有普通工具

`main.go` 新增：

```go
func registerLocalDocumentSearchTool(
    documentIndex *rag.DocumentIndex,
    applicationConfig config.Config,
) error
```

`main()` 的具体执行顺序：

1. `applicationConfig := config.Load()` 取得资料目录和分段参数。
2. `localDocumentIndex, createDocumentIndexError := rag.NewDocumentIndex(...)` 创建索引对象。
3. `localDocumentIndex.Refresh()` 第一次读取资料。
4. `registerLocalDocumentSearchTool(localDocumentIndex, applicationConfig)` 注册工具。
5. 现有 `service.Run(..., registry, ...)` 和 `service.RunStream(..., registry, ...)` 继续接收同一个工具表。

`registerLocalDocumentSearchTool` 调用现有 `registry.RegisterFunctionTool`，传入工具名称、说明、JSON Schema 和执行函数。执行函数保存 `localDocumentIndex` 和 `applicationConfig`，以后 `registry.Execute("search_local_documents", toolArguments)` 调用这个函数。这里不把 `registry` 保存到 `rag.DocumentIndex` 中，也不让 `rag` 包依赖 `tool` 包。

选择普通工具而不是新增 HTTP 路由，因为当前 Agent 已经能选择和执行 `tool.Registry` 中的工具。另一个做法是在每次聊天前强制搜索所有用户问题；没有采用，因为普通闲聊也会产生额外搜索和 DeepSeek 重排调用。

### 2. `rag/` 只负责读取、分段和本地搜索

新增文件和函数：

- `rag/types.go`
  - `DocumentChunk`：保存 `SourceID`、`FilePath`、`StartLine`、`EndLine`、`Content` 和本地搜索所需的词频。
  - `SearchResult`：保存准备交给重排函数和工具结果的来源字段与 `Score`。
- `rag/chunker.go`
  - `SplitDocumentIntoChunks(relativeFilePath string, documentText string, maximumChunkRuneCount int, overlapRuneCount int) []DocumentChunk`
  - 默认每段最多 800 个 Unicode 字符，相邻段重复 120 个字符；函数同时计算每段对应的起始行和结束行。
- `rag/tokenizer.go`
  - `TokenizeSearchText(text string) []string`
  - 英文转小写后按单词保存；连续中文生成两个汉字组成的搜索词。
- `rag/bm25.go`
  - `RankDocumentChunksByBM25(queryTokens []string, documentChunks []DocumentChunk, candidateCount int) []SearchResult`
  - 只返回分数大于 0 的结果，按分数从高到低排序，分数相同按 `SourceID` 排序，保证相同输入得到相同顺序。
- `rag/index.go`
  - `NewDocumentIndex(knowledgeDirectory string, maximumChunkRuneCount int, overlapRuneCount int) (*DocumentIndex, error)`
  - `(*DocumentIndex).Refresh() error`
  - `(*DocumentIndex).RefreshIfFilesChanged() error`
  - `(*DocumentIndex).Search(query string, candidateCount int) ([]SearchResult, error)`
  - `Search` 先调用 `RefreshIfFilesChanged`，再调用 `TokenizeSearchText` 和 `RankDocumentChunksByBM25`。
- `rag/tool_result.go`
  - `BuildDocumentSearchToolResult(query string, results []SearchResult, rerankApplied bool) (string, error)`
  - 使用 `encoding/json` 返回固定字段，不拼接无法解析的自由文本。

`DocumentIndex` 用 `sync.RWMutex` 保护内存中的文件状态和分段。`Refresh` 使用 `filepath.WalkDir` 递归读取 `.md`、`.txt`，用绝对路径、`filepath.Clean` 和 `filepath.Rel` 确认文件仍在 `workspace/knowledge/` 内。空目录是正常状态。

没有采用“每次搜索重新读取所有文件”，因为文件没有变化时不需要重复分段。没有采用持久化索引文件，因为当前资料量和版本目标只需要内存索引，并且文件变化后可以重新生成。

### 3. BM25 先取 12 项，DeepSeek 再选最终结果

新增 `service/rerank.go`：

```go
func RerankDocumentSearchResults(
    query string,
    candidateResults []rag.SearchResult,
    resultCount int,
    applicationConfig config.Config,
) ([]rag.SearchResult, error)
```

该函数把问题、候选 `sourceId` 和候选原文组成一次 DeepSeek 请求，然后调用现有 `service.Chat(messages, systemPrompt, applicationConfig, nil, 1024)`。这次调用不传工具。DeepSeek 只返回按相关程度排列的 `sourceId` JSON 数组。函数使用 `encoding/json` 解包，删除重复 ID，拒绝候选列表以外的 ID，再按 `resultCount` 截取。

`search_local_documents` 执行函数的具体顺序：

1. 从 `toolArguments` 读取非空字符串 `query`。
2. 从 `toolArguments` 读取 `resultCount`；未提供时使用 5，只接受 1 到 8。
3. 调用 `localDocumentIndex.Search(query, 12)` 得到最多 12 个 BM25 候选结果。
4. 候选为空时直接返回空 `results`，不调用 DeepSeek。
5. 调用 `service.RerankDocumentSearchResults(query, candidateResults, resultCount, applicationConfig)`。
6. 重排成功时使用 DeepSeek 顺序并写 `rerankApplied: true`。
7. 重排失败时写 WARN 日志，取 BM25 前 `resultCount` 项并写 `rerankApplied: false`。日志只写候选数量、错误类别和操作名称。
8. 调用 `rag.BuildDocumentSearchToolResult`，把 JSON 字符串返回 `registry.Execute`。
9. `service.Run` 或 `service.RunStream` 把该 JSON 字符串写入下一轮 `tool_result`。

重排失败不作为整个工具失败返回，因为 BM25 已经产生可用结果。资料读取、参数错误和本地搜索错误仍返回 `error`，现有 Agent 循环会把错误文字交给下一轮模型。

### 4. 来源编号直接来自文件位置

每个分段的 `SourceID` 固定为：

```text
[source:<相对文件路径>:<起始行>-<结束行>]
```

例如 `workspace/knowledge/product/guide.md` 第 12 到 18 行生成 `[source:product/guide.md:12-18]`。`BuildDocumentSearchToolResult` 返回：

```json
{
  "query": "退款期限",
  "rerankApplied": true,
  "results": [
    {
      "sourceId": "[source:product/guide.md:12-18]",
      "filePath": "product/guide.md",
      "startLine": 12,
      "endLine": 18,
      "content": "实际原文",
      "score": 4.27
    }
  ]
}
```

`main.go` 的 `systemPromptBase` 增加明确规则：使用资料原文形成回答时，复制对应的 `sourceId`；没有调用资料工具时不声称读取过资料；不得编造工具结果中不存在的来源编号。

没有只返回文件名，因为同一个文件可能很长，用户需要行号才能核对实际原文。

### 5. 参数放入现有 `config.Config`

`config/config.go` 的 `Config` 增加：

- `KnowledgeDirectory: "workspace/knowledge"`
- `RAGMaximumChunkRuneCount: 800`
- `RAGChunkOverlapRuneCount: 120`
- `RAGCandidateCount: 12`
- `RAGDefaultResultCount: 5`
- `RAGMaximumResultCount: 8`

v13 先把这些值集中放在 `config.Load()` 的返回值中，不新增另一份 JSON 配置。它们是 Go 服务自己的固定运行参数，不是用户需要在网页选择的外部 Server 列表。

## Risks / Trade-offs

- [中文两个汉字搜索词不能理解同义词] → DeepSeek 重排只能调整已被 BM25 找到的候选；任务中准备明确的同义表达样例，记录这是 v13 的能力边界。
- [800/120 的分段参数不适合所有资料] → 参数集中保存在 `config.Config`，验证时比较至少两组分段大小和返回数量，并把结果记录到 v13 文档。
- [DeepSeek 重排增加一次模型调用和延迟] → 只在存在 BM25 候选时调用；失败立即使用本地顺序，不阻断 Agent 回答。
- [模型可能漏写来源编号] → system prompt 明确引用格式，并用一份固定资料执行一次实际聊天检查；不增加独立评测系统。
- [文件在读取过程中被修改] → 本次搜索使用已经完整读取并生成的内存分段；下一次搜索再次检查文件状态。

## Migration Plan

1. 新增 `workspace/knowledge/.gitkeep`，不移动现有 `workspace/` 文件。
2. 新增 `rag/`、`service/rerank.go`，修改 `config/config.go` 和 `main.go`。
3. 更新 `PROJECT_INDEX.md` 后运行 `go fmt ./...`、`go build ./...`、`go vet ./...`、`go test ./...`。
4. 放入一份不含隐私的固定 `.md` 资料，执行 `search_local_documents` 和一次实际聊天，检查原文、行号、重排标志和最终来源编号。
5. 删除验证资料或只保留公开样例；明确排除 `workspace/银河争霸战.txt`。
6. 回滚时删除 v13 新文件，并撤销 `main.go`、`config/config.go` 和文档修改；现有 HTTP、MCP 和工具循环不需要数据迁移。

## Open Questions

无。实现开始前由用户在 Plannotator 中检查并批准本计划；实现中不扩大到 PDF、向量数据库或新网页。
