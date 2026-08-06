# harness-agent-orchestration

## ADDED Requirements

### Requirement: Harness is an Agent holding exactly two tools
The harness SHALL run as an `agent.Agent` whose tool registry contains exactly two tools: `agent` (drives managed agents) and `memory` (reads the harness's own session memory). The harness registry SHALL NOT contain `bash`, `activate_skill`, `create_skill`, `run_subagent`, or any MCP tool — therefore the harness needs the dedicated `memory` tool to read its own memory. The harness SHALL reuse `agent.Agent.Run` and SHALL NOT implement a second model-and-tool loop, and its conversation SHALL receive the same token counting and threshold memory compression as any other agent. The harness system prompt SHALL be loaded from `harness/system_prompt.md` at startup; if the file is missing, the harness routes SHALL NOT be served.

#### Scenario: User asks the harness to do file work
- **WHEN** the harness model decides files must be created or read
- **THEN** its only way to perform the work is delegating through the `agent` tool; reviewing its own earlier decisions uses the `memory` tool

#### Scenario: Harness prompt file is missing
- **WHEN** the server starts without `harness/system_prompt.md`
- **THEN** requests to harness routes return a configuration error naming the missing file

### Requirement: Natural-language agent interaction
The `agent` tool SHALL accept three parameters: `agent` (the agent's name, required), `request` (the complete natural-language content handed to that agent, required unless `forget` is true), and `forget` (optional boolean, default false). A name without a registry entry SHALL create the agent (upsert) with conversation ID `harness-agent-<slug>` before running; the same name SHALL always resolve to the same agent and the same memory. The tool description SHALL document agent capabilities (persistent memory, independent execution, bash/Skill/MCP tools) and the pool limit, but SHALL NOT prescribe a usage order. The Go implementation SHALL NOT parse natural language and SHALL NOT make a second model call. Validation errors SHALL name the missing parameter.

#### Scenario: First request creates the agent
- **WHEN** the harness calls the tool with agent `coder` and a request defining the role
- **THEN** the registry gains `coder` with conversation ID `harness-agent-coder`, the managed agent is launched in a background goroutine with the shared execution prompt and the request text, and the tool result immediately confirms the launch

#### Scenario: Same name, same memory
- **WHEN** the harness later calls the tool with agent `coder` again
- **THEN** the run reuses the existing session file, so the agent can recall its earlier role and tasks through its own memory

#### Scenario: Missing parameter
- **WHEN** the harness calls the tool without `agent`, or without `request` while `forget` is not true
- **THEN** the tool returns an error naming the missing parameter and nothing runs

### Requirement: Agent pool size is limited and dynamically managed
The registry SHALL reject `create` when it already holds the configured maximum number of agents, defaulting to 11 and adjustable with the `MAX_HARNESS_AGENTS` environment variable (invalid values fall back to the default). The rejection error SHALL name the limit, list the current agent names, and point to the remedies (`forget` a name or reuse an existing one). `forget` SHALL free the slot; the agent's session file SHALL remain so that recreating the same name later resumes its memory.

#### Scenario: Pool is full
- **WHEN** the registry already contains 11 agents and the harness calls the tool with a twelfth new name
- **THEN** the tool returns an error naming the limit and the current roster, and no agent is created

#### Scenario: Forget frees a slot
- **WHEN** the harness calls the tool with `forget: true` for one agent and then sends a request to a new name
- **THEN** the slot is freed and the new agent is created successfully

#### Scenario: Forgotten name keeps its memory file
- **WHEN** an agent is forgotten and later created again with the same name
- **THEN** the session file still exists and the recreated agent can recall its earlier memory

### Requirement: Registry and per-agent memory layout
The registry SHALL persist to `<workingDirectory>/.cc-agent/harness/agents.json` with a mutex protecting concurrent access. The registry SHALL store only operational metadata (name, slug, conversation ID, status, last error, task count, timestamps); roles and duties SHALL live in each agent's own session memory, written by its first `request`. The harness conversation ID SHALL be `harness`. Each managed agent conversation ID SHALL be `harness-agent-<slug>` where slug lowercases the name, folds characters outside `[a-z0-9-_]` into `-`, and falls back to `agent-<creationIndex>` when empty. Every load, append, list, and compression of agent memory SHALL reuse `memory.ProjectConversationStore`.

#### Scenario: Server restarts
- **WHEN** the server restarts and the harness calls `list`
- **THEN** the registry shows the previously created agents and a following `task` reuses the same conversation file, preserving earlier memory

#### Scenario: Two projects use the same agent name
- **WHEN** agents named `coder` are created under two different working directories
- **THEN** two separate `agents.json` files and two separate session files exist

### Requirement: MCP usage is guided in natural language
Every managed agent's tool registry SHALL equal the base tools (`bash`, `activate_skill`, `create_skill`) plus all globally registered MCP tools; the Go code SHALL NOT filter MCP tools per agent. On every harness chat request, the server SHALL append three natural-language sections to the harness system prompt: the current agent roster (name, status, task count), the currently running MCP servers and their tool names (or that none are running), and the existing apps. The harness SHALL convey MCP usage guidance to managed agents through `request` text only, and SHALL NOT need a list-style action.

#### Scenario: Harness learns current MCP resources
- **WHEN** the Playwright MCP server is running and the user posts a harness chat message
- **THEN** the harness system prompt for that request contains the `mcp_playwright__*` tool names in natural language

#### Scenario: No MCP server running
- **WHEN** no MCP server is running
- **THEN** the injected section states that no MCP resource is currently available and managed agents receive only the base tools

#### Scenario: Harness sees its roster without a list action
- **WHEN** two agents exist and the user posts a harness chat message
- **THEN** the harness system prompt for that request contains both names with status and task count

### Requirement: Non-blocking launch with a completion queue
The `agent` tool SHALL launch the managed agent in a background goroutine and return immediately with the agent name, conversation ID, and `running` status, so the harness can start several agents in one turn. During execution, typed events SHALL be encoded with a harness-local switch and forwarded to `ConversationEventReceivers` under the managed agent's conversation ID (the event receiver is a call-site parameter, so the `agent` package stays untouched). On completion the goroutine SHALL write the final status (`completed` or `failed`) and the formatted result text into the registry and push the record onto a per-project completion queue (a buffered channel). A single consumer goroutine per project SHALL block on queue receives; for each finished record it SHALL mark the result collected and invoke the harness itself with an `InternalContinuationTaskInput` under the same conversation execution lock, fanning the harness's own events out to the `harness` conversation channel with a harness-local event encoding. When a project runtime is created, the registry SHALL be swept once so that results finished but not collected before a restart are enqueued.

#### Scenario: Web page watches one agent
- **WHEN** a browser opens the existing `GET /api/conversations/{id}/events` endpoint with a managed agent's conversation ID
- **THEN** it receives round, text-delta, tool, and completion events for that agent's conversation only

#### Scenario: Queue consumer collects a finished result
- **WHEN** a managed agent finishes while the user is not chatting
- **THEN** the consumer dequeues the record, invokes the harness with the formatted result, and the harness's report appears on the `harness` conversation channel

#### Scenario: Several agents run concurrently
- **WHEN** the harness calls the `agent` tool for three names in one turn
- **THEN** all three run in background goroutines and the queue consumer delivers their results to the harness one at a time under the conversation lock

#### Scenario: Agent fails
- **WHEN** a managed agent's background run returns an error
- **THEN** the registry records `failed` with the message and the queue consumer delivers the failure to the harness

#### Scenario: Server restarts with uncollected results
- **WHEN** the server restarts while a finished result has not been collected
- **THEN** the next runtime creation for that project sweeps the registry and enqueues the uncollected result

### Requirement: Formatted agent returns and harness memory tool
The managed agent's system prompt (`harness/managed_agent_prompt.md`) SHALL require the final reply to state who the agent is, what task it executed, and what the result is (including key files and numbers), so the checker can consume it directly. The `memory` tool SHALL read the harness's own session memory, accept an optional `recentMessages` parameter (default 20), and accept an optional `pattern` parameter that filters messages with a Go regular expression (rg-style search without an external command). It SHALL return the conversation title, message count, and the matching recent messages.

#### Scenario: Checker consumes a formatted result
- **WHEN** a managed agent completes a task
- **THEN** the registry result text contains the agent's name, the executed task, and the outcome in the required format

#### Scenario: Harness reviews its own memory
- **WHEN** the harness calls `memory` with `recentMessages: 5`
- **THEN** the tool returns the harness conversation title, total message count, and the five most recent messages

#### Scenario: Harness searches its memory
- **WHEN** the harness calls `memory` with `pattern: "council"`
- **THEN** the tool returns only the harness messages matching that pattern

### Requirement: Harness HTTP endpoints
The service SHALL expose `POST /api/harness/chat/stream` (SSE, body: `workingDirectory`, `message`), `GET /api/harness/agents?workingDirectory=`, and `GET /api/harness/agents/{name}/memory?workingDirectory=`. Agent progress and harness background replies SHALL reuse the existing `GET /api/conversations/{id}/events` endpoint; no new event endpoint SHALL be added. `workingDirectory` SHALL be a required absolute path; invalid values return the existing `invalid_request` error shape. The harness chat endpoint SHALL reuse the existing typed-agent-event SSE encoding.

#### Scenario: Harness chat round trip
- **WHEN** the browser posts a message to `/api/harness/chat/stream`
- **THEN** the harness agent runs with conversation ID `harness` and the stream contains the typed agent events followed by the final completion

#### Scenario: Missing workingDirectory
- **WHEN** any harness endpoint is called without an absolute `workingDirectory`
- **THEN** it returns HTTP 400 with `invalid_request`
