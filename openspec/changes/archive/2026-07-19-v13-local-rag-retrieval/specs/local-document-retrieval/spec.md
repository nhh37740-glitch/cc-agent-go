## ADDED Requirements

### Requirement: Read supported local documents
The Go service SHALL recursively read `.md` and `.txt` files under `workspace/knowledge/`, SHALL ignore unsupported file types, and SHALL keep every returned file path relative to `workspace/knowledge/`.

#### Scenario: Service starts with supported documents
- **WHEN** `workspace/knowledge/` contains `product/guide.md` and `notes.txt`
- **THEN** the document index contains chunks from both files with relative paths `product/guide.md` and `notes.txt`

#### Scenario: Unsupported file is present
- **WHEN** `workspace/knowledge/` also contains `diagram.png`
- **THEN** the document index ignores `diagram.png` and continues loading supported files

#### Scenario: Knowledge directory is empty
- **WHEN** `workspace/knowledge/` contains no supported files
- **THEN** the Go service starts successfully and a later search returns an empty result list

### Requirement: Refresh changed documents without recompiling
The document index SHALL compare supported file paths, modification times, and sizes before each search, and SHALL rebuild changed, added, or deleted document chunks without requiring the Go service to be recompiled or restarted.

#### Scenario: User adds a document after service startup
- **WHEN** the user adds `workspace/knowledge/new-topic.md` and then calls `search_local_documents`
- **THEN** that tool call can return chunks from `new-topic.md`

#### Scenario: User removes a document after service startup
- **WHEN** the user removes a previously indexed document and then calls `search_local_documents`
- **THEN** the removed document cannot appear in the returned results

### Requirement: Preserve source positions while splitting documents
The document index SHALL split supported documents into chunks containing at most 800 Unicode characters with 120 characters of overlap, and SHALL save the relative file path, start line, end line, and exact original text for every chunk.

#### Scenario: Search returns a chunk
- **WHEN** a document chunk is selected as a search result
- **THEN** the result contains the exact chunk text and a start and end line that identify that text in the source file

### Requirement: Rank Chinese and English text locally
The Go service SHALL tokenize lowercase English words and Chinese two-character terms, SHALL calculate BM25 scores with Go standard-library code, and SHALL return the highest-scoring candidate chunks first.

#### Scenario: Relevant text ranks above unrelated text
- **WHEN** a query shares Chinese terms or lowercase English words with one chunk but not another chunk
- **THEN** the matching chunk receives a higher BM25 score than the unrelated chunk

#### Scenario: Query has no matching terms
- **WHEN** none of the indexed chunks contains any query term
- **THEN** the local search returns an empty candidate list instead of unrelated chunks

### Requirement: Rerank candidates with DeepSeek and keep a local fallback
The Go service SHALL send at most 12 BM25 candidates to DeepSeek for relevance ordering, SHALL accept only source IDs present in the candidate list, and SHALL return at most the requested result count. If the rerank call or response parsing fails, the tool SHALL return the leading BM25 candidates and set `rerankApplied` to `false`.

#### Scenario: DeepSeek returns a valid order
- **WHEN** DeepSeek returns valid candidate source IDs in relevance order
- **THEN** the tool returns those candidates in that order and sets `rerankApplied` to `true`

#### Scenario: DeepSeek rerank fails
- **WHEN** the DeepSeek request fails or contains an invalid source ID
- **THEN** the tool returns the leading BM25 candidates and sets `rerankApplied` to `false`

### Requirement: Register one ordinary Agent tool
The Go service SHALL register `search_local_documents` in the existing `tool.Registry`. The tool SHALL accept a non-empty string `query` and an optional integer `resultCount` from 1 through 8, with a default of 5.

#### Scenario: Agent requests document search
- **WHEN** `service.Run` executes `registry.Execute("search_local_documents", toolArguments)` with a valid query
- **THEN** the tool returns JSON containing `query`, `rerankApplied`, and a `results` array

#### Scenario: Tool arguments are invalid
- **WHEN** `query` is empty or `resultCount` is outside 1 through 8
- **THEN** the tool returns an error that `service.Run` places in the next `tool_result`

### Requirement: Return verifiable sources to the model
Each search result SHALL contain `sourceId`, `filePath`, `startLine`, `endLine`, `content`, and `score`. `sourceId` SHALL use the format `[source:<relative-file-path>:<start-line>-<end-line>]`.

#### Scenario: Tool returns a matched paragraph
- **WHEN** a chunk from lines 12 through 18 of `product/guide.md` is returned
- **THEN** its `sourceId` is `[source:product/guide.md:12-18]` and its `content` is the exact indexed text

### Requirement: Cite only sources used in the final answer
The Agent system prompt SHALL instruct the model to copy the supplied `sourceId` after claims that use retrieved document text, and SHALL forbid inventing a source ID that was not returned by `search_local_documents`.

#### Scenario: Agent uses a retrieved statement
- **WHEN** the final answer includes a fact taken from a returned document chunk
- **THEN** the answer includes that chunk's exact `sourceId`

#### Scenario: Agent does not use local documents
- **WHEN** the Agent answers without calling `search_local_documents`
- **THEN** the answer does not claim that a local document was used

### Requirement: Keep document contents out of logs
The Go service SHALL log file counts, chunk counts, operation names, error kinds, and timing values, and SHALL NOT log search queries, document text, rerank prompt text, or model rerank response text.

#### Scenario: Document search completes
- **WHEN** `search_local_documents` finishes successfully or with a rerank fallback
- **THEN** JSON logs contain counts and status but contain neither the query nor document content
