## 1. 配置和资料类型

- [ ] 1.1 在 `config/config.go` 的 `Config` 和 `Load()` 中增加资料目录、800 字分段、120 字重复、12 个候选、默认 5 个结果和最多 8 个结果的配置值
- [ ] 1.2 新建 `rag/types.go`，定义保存文件位置和原文的 `DocumentChunk`，以及交给重排函数的 `SearchResult`
- [ ] 1.3 新建 `workspace/knowledge/.gitkeep`，确认索引只读取该目录内的 `.md` 和 `.txt`

## 2. 读取文件并分段

- [ ] 2.1 在 `rag/chunker.go` 实现 `SplitDocumentIntoChunks`，按 Unicode 字符数量分段并计算每段的起始行和结束行
- [ ] 2.2 在 `rag/index.go` 实现 `NewDocumentIndex` 和 `Refresh`，递归读取支持的文件并拒绝 `workspace/knowledge/` 之外的路径
- [ ] 2.3 在 `rag/index.go` 实现 `RefreshIfFilesChanged`，根据相对路径、修改时间和文件大小更新新增、修改和删除的资料
- [ ] 2.4 为分段行号、空目录、忽略其他文件类型和文件变化刷新增加 Go 测试，并运行对应包测试

## 3. BM25 本地搜索

- [ ] 3.1 在 `rag/tokenizer.go` 实现 `TokenizeSearchText`，生成小写英文单词和中文两个汉字组成的搜索词
- [ ] 3.2 在 `rag/bm25.go` 实现 `RankDocumentChunksByBM25`，只返回分数大于 0 的分段，并按分数和 `SourceID` 产生固定顺序
- [ ] 3.3 在 `rag/index.go` 实现 `DocumentIndex.Search`，先刷新变化，再把问题交给分词函数和 BM25 函数
- [ ] 3.4 为中英文命中、无匹配结果、最多 12 个候选和相同输入的固定顺序增加 Go 测试，并运行对应包测试

## 4. DeepSeek 重排和失败回退

- [ ] 4.1 在 `service/rerank.go` 实现 `RerankDocumentSearchResults`，调用现有 `service.Chat(..., nil, 1024)` 并解包 DeepSeek 返回的 `sourceId` JSON 数组
- [ ] 4.2 检查重排结果，只接受候选列表中存在且不重复的 `sourceId`，然后按 `resultCount` 截取
- [ ] 4.3 实现重排失败回退：保留 BM25 前几项并把 `rerankApplied` 设置为 `false`，日志不写问题、资料原文或模型回复
- [ ] 4.4 为有效重排顺序、未知来源编号、无效 JSON 和回退结果增加 Go 测试，并运行对应包测试

## 5. 注册 `search_local_documents`

- [ ] 5.1 在 `rag/tool_result.go` 实现 `BuildDocumentSearchToolResult`，返回 `query`、`rerankApplied` 和包含来源位置的 `results` JSON
- [ ] 5.2 在 `main.go` 实现 `registerLocalDocumentSearchTool(documentIndex, applicationConfig)`，校验非空 `query` 和 1 到 8 的 `resultCount`
- [ ] 5.3 在 `main()` 创建并首次刷新 `DocumentIndex`，再把 `search_local_documents` 的定义、参数和执行函数注册到现有全局 `registry`
- [ ] 5.4 修改 `systemPromptBase`：使用资料内容时复制工具返回的 `sourceId`，未调用工具时不声称读取资料，不编造来源编号
- [ ] 5.5 通过 `registry.Execute("search_local_documents", toolArguments)` 检查完整 JSON 结果以及参数错误返回值

## 6. 实际运行和结果检查

- [ ] 6.1 准备一份不含隐私的固定 `.md` 样例，验证搜索结果的 `content`、`filePath`、`startLine`、`endLine` 和原文件一致
- [ ] 6.2 使用有效 `DEEPSEEK_API_KEY` 完成一次聊天，确认 Agent 调用资料工具后在最终回答中写出实际返回的 `sourceId`
- [ ] 6.3 模拟 DeepSeek 重排失败，确认工具仍返回 BM25 结果并写 `rerankApplied: false`
- [ ] 6.4 比较至少两组分段大小和两组结果数量，把查询、参数、命中位置、耗时和观察结果写入 `docs/v13-rag-experiment.md`
- [ ] 6.5 检查 JSON 日志只含操作名、文件数、分段数、耗时和错误类别，不含问题、资料正文、重排提示词或模型回复

## 7. 文档、完整检查和发布

- [ ] 7.1 更新 `PROJECT_INDEX.md`，写出 v13 实际新增文件、函数、调用者、参数、返回值和执行顺序
- [ ] 7.2 更新 `README.md`、`ROADMAP.md` 和 `AGENTS.md`，只在代码和实际运行检查完成后把 v13 标为完成并把主线切到 v14
- [ ] 7.3 运行 `go fmt ./...`、`go build ./...`、`go vet ./...` 和 `go test ./...`，任何一项失败都先修复再继续
- [ ] 7.4 检查暂存文件不含 API Key、私人资料或 `workspace/银河争霸战.txt`，提交并推送 `agent/agent-learning-roadmap`
- [ ] 7.5 推送成功后更新 Draft PR #3 的标题和正文，并把 GitHub Project #1 的 v13 状态从 `In Progress` 改为 `Done`
