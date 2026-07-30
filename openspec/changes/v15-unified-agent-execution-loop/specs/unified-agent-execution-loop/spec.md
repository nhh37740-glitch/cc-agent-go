## ADDED Requirements

### Requirement: Encapsulate one reusable Agent
The `agent` package SHALL expose an `Agent` type that owns the model caller, reasoning-and-tool loop, ordinary tools, dynamically registered MCP tools, SubAgent terminal-tool behavior, token counter, memory save and compression behavior, and typed event receiver required to execute a task. `Agent` SHALL NOT contain WebAgent HTTP request types, game roles, council speakers, or application names.

#### Scenario: HTTP service uses the Agent
- **WHEN** the WebAgent HTTP handler receives a task
- **THEN** the service creates an Agent task input and execution environment, then calls the same `Agent.Run` used by non-HTTP callers

#### Scenario: A future hosted application uses the Agent
- **WHEN** a werewolf, script-murder, or council host needs one participant to act
- **THEN** the host supplies that participant's working directory, conversation ID, and current task to `Agent.Run` without modifying the `agent` package

### Requirement: Receive the active project directory and conversation ID from the caller
Every `Agent.Run` SHALL receive an `AgentExecutionEnvironment` containing a required absolute `WorkingDirectory` and a required `ConversationID`. The Agent SHALL NOT generate a conversation ID and SHALL NOT read a process-wide workspace directory. An HTTP handler, SubAgent host, game host, or other caller SHALL choose these two values before calling the Agent.

#### Scenario: WebAgent starts a task
- **WHEN** the browser submits `workingDirectory`, `conversationId`, and `message`
- **THEN** `main.handleChatStream` passes those exact working-directory and conversation-ID values through the service to `Agent.Run`

#### Scenario: Game host starts one player turn
- **WHEN** a host assigns working directory `C:/games/werewolf-1` and conversation ID `player-3`
- **THEN** the Agent uses that directory and that conversation ID only for the current run

#### Scenario: Required environment value is missing
- **WHEN** `WorkingDirectory` or `ConversationID` is empty
- **THEN** `Agent.Run` returns a validation error before calling the model or a tool

### Requirement: Use distinct task input types
The `agent` package SHALL define different concrete task types for a visible user task, an internal SubAgent-result continuation, and a hosted Agent task. `Agent.Run` SHALL accept their shared `AgentTaskInput` interface together with `AgentExecutionEnvironment`. It SHALL NOT receive a `saveInputMessage` boolean or infer a task kind from empty fields.

#### Scenario: Visible user task runs
- **WHEN** the service supplies `UserTaskInput`
- **THEN** the Agent records the task and final result in the selected conversation file

#### Scenario: Background continuation runs
- **WHEN** the service supplies `InternalContinuationTaskInput`
- **THEN** the model receives the SubAgent results for this run but the internal input is not recorded as a visible user task

#### Scenario: Hosted participant task runs
- **WHEN** a game or other host supplies `HostedAgentTaskInput`
- **THEN** the Agent records the host task and result under the host-supplied conversation ID

### Requirement: Derive session memory from the project directory
The Agent SHALL derive the active session file as `<WorkingDirectory>/.cc-agent/sessions/<ConversationID>.json`. Conversation IDs SHALL remain restricted to the existing safe character set. The session store SHALL receive the working directory for every load, append, list, and compression operation instead of retaining one global sessions directory.

#### Scenario: Two projects use the same conversation ID
- **WHEN** two runs use conversation ID `player-1` with different working directories
- **THEN** they read and write different `.cc-agent/sessions/player-1.json` files

#### Scenario: Two conversations use one project directory
- **WHEN** two runs use one working directory with conversation IDs `player-1` and `player-2`
- **THEN** they read and write different session files under that project's `.cc-agent/sessions` directory

### Requirement: Send a memory reference instead of the complete stored conversation
The first model call of each `Agent.Run` SHALL NOT append all messages loaded from the session JSON. It SHALL send the current task and an `AgentMemoryReference` containing the active working directory, session file path, project instruction file path, and concrete instructions for using `rg`, `head`, `tail`, or `cat` through the bash tool when old information is needed.

#### Scenario: Existing session contains many messages
- **WHEN** a session JSON already contains messages from earlier HTTP requests
- **THEN** the first model request contains the current task and memory reference but does not contain those stored message bodies

#### Scenario: Model needs one earlier fact
- **WHEN** DeepSeek decides the current task depends on an earlier fact
- **THEN** it calls the bash tool with a command such as `rg -n "keyword" .cc-agent/sessions/<conversationId>.json` and only the returned result enters the current run message list

#### Scenario: Model does not need old information
- **WHEN** DeepSeek can complete the task from the current task and project files
- **THEN** no session-memory read tool call is required and old conversation text never enters the API request

### Requirement: Keep current-run tool protocol messages
The Agent SHALL keep the current task, assistant tool-use messages, and corresponding tool-result messages created during the active `Agent.Run` in its in-memory message list until that run finishes or current-run compaction occurs. It SHALL NOT remove a tool-use message while retaining its tool-result message.

#### Scenario: Tool result requires another model round
- **WHEN** DeepSeek requests a tool and the Agent executes it
- **THEN** the next DeepSeek call receives the current task, that assistant tool-use block, and its matching tool-result block

#### Scenario: New HTTP request uses the same conversation ID
- **WHEN** the previous `Agent.Run` has completed and a new request starts
- **THEN** the new first model call receives a memory reference instead of replaying the previous run's in-memory tool protocol messages

### Requirement: Execute tools in the caller-supplied working directory
`tool.Registry.Execute` SHALL receive a `ToolExecutionEnvironment` containing the current working directory and conversation ID. `BashTool` and every file-based tool SHALL resolve relative paths and set command execution from this environment. They SHALL NOT keep a fixed workspace path chosen during process startup.

#### Scenario: Bash tool runs
- **WHEN** the Agent executes bash with working directory `C:/projects/example`
- **THEN** `exec.CommandContext` uses `C:/projects/example` as `cmd.Dir`

#### Scenario: File tool resolves a relative path
- **WHEN** a file-based tool receives relative path `docs/plan.md`
- **THEN** it resolves the path under the current `WorkingDirectory` and rejects a result outside that directory

#### Scenario: MCP tool ignores local files
- **WHEN** the Agent executes an MCP browser tool
- **THEN** the registry still supplies the same execution environment, while the MCP tool may complete without reading its working-directory field

### Requirement: Count tokens with the active model tokenizer
The application SHALL use `github.com/amikos-tech/pure-tokenizers v0.1.5` as one shared Hugging Face tokenizer runtime. Each configured model SHALL provide its own local `tokenizer.json` path and context-window size. The application SHALL create an `AgentTokenCounter` from that model-specific file during service startup and pass it to the Agent. Before a model call the Agent SHALL count the serialized system prompt, current task, memory reference, active-run messages, and tool definitions that will actually be sent. Character multipliers such as Chinese characters times `0.6` SHALL NOT be used as the token count.

#### Scenario: DeepSeek V4 is configured
- **WHEN** the application config selects `deepseek-v4-pro[1m]`
- **THEN** service startup loads the configured local copy of the official DeepSeek V4 `tokenizer.json` and the token counter returns the number of tokenizer IDs produced for the prepared model request

#### Scenario: Another Hugging Face model is configured
- **WHEN** a later model configuration supplies another model name, local `tokenizer.json`, and context-window size
- **THEN** the same `HuggingFaceJSONTokenCounter` implementation loads that file without changing `Agent.Run`

#### Scenario: Tokenizer cannot be initialized
- **WHEN** the native tokenizer library or configured `tokenizer.json` cannot be loaded during service startup
- **THEN** startup returns a concrete tokenizer configuration error and does not fall back to character-count estimation

#### Scenario: DeepSeek returns usage
- **WHEN** a model call completes
- **THEN** the Agent records the provider-returned `InputTokens` and `OutputTokens` as the authoritative actual usage for that call

#### Scenario: Configured model has no matching token counter
- **WHEN** Agent construction cannot obtain a token counter for the configured model
- **THEN** construction fails instead of silently switching to a character-ratio estimate

### Requirement: Separate request-context tokens from stored-memory tokens
The Agent SHALL maintain separate values for `PreparedRequestTokens` and `StoredMemoryTokens`. Stored session text SHALL count toward the memory-file compression threshold but SHALL NOT count toward the current API context unless a tool result copied that text into the active-run messages.

#### Scenario: Large memory file is not read
- **WHEN** the session file contains 80,000 tokens and the current request contains 3,000 tokens
- **THEN** the current request-context count is 3,000 rather than 83,000

#### Scenario: Bash returns a memory excerpt
- **WHEN** a bash tool result copies an excerpt into the current run
- **THEN** that excerpt is included in the next prepared-request token count

### Requirement: Enforce the model context window before each API call
Before each model call, the Agent SHALL verify `PreparedRequestTokens + MaximumOutputTokens <= ModelContextWindowTokens`. If it does not fit, the Agent SHALL compact completed current-run rounds, recount the exact prepared request, and only then call the model. An unmatched active tool-use and tool-result pair SHALL NOT be compacted separately.

#### Scenario: Current run fits
- **WHEN** prepared request tokens plus reserved output tokens do not exceed the model context window
- **THEN** the Agent calls the model without current-run compaction

#### Scenario: Completed tool rounds exceed the window
- **WHEN** completed tool-use and tool-result rounds make the next request too large
- **THEN** the Agent summarizes completed rounds into one current-run summary message, preserves any required active protocol blocks, recounts, and then calls the model

#### Scenario: Compacted request still does not fit
- **WHEN** exact recounting still exceeds the model context window
- **THEN** the Agent returns a concrete context-limit error without sending an oversized request

### Requirement: Limit tool results by tokens
The Agent SHALL use the active model token counter to limit a tool result before adding it to the current-run message list. The fixed 8,000-character limit SHALL be replaced by a configured `MaximumToolResultTokens` limit, and truncation SHALL produce a visible truncation marker.

#### Scenario: Memory search returns a small excerpt
- **WHEN** the tokenized tool result is within the configured limit
- **THEN** the complete result enters the matching tool-result block

#### Scenario: Cat returns a large file
- **WHEN** the tokenized tool result exceeds the configured limit
- **THEN** the Agent keeps only content that fits the token limit and appends a marker instructing DeepSeek to perform a narrower `rg`, `head`, or `tail` read

### Requirement: Save and compact project-scoped session memory
After a run finishes, the Agent SHALL append the selected task, final assistant result, and required execution records to the project-scoped session JSON. It SHALL count the resulting session file with the active model tokenizer. When `StoredMemoryTokens` exceeds `MaximumStoredMemoryTokens`, it SHALL call the model with no tools to summarize old records, archive those records in the same project's `.cc-agent/sessions` directory, save the summary plus recent records, and recount the resulting file.

#### Scenario: Stored memory remains below its limit
- **WHEN** the tokenized session JSON does not exceed `MaximumStoredMemoryTokens`
- **THEN** the Agent saves the appended session without compression

#### Scenario: Stored memory exceeds its limit
- **WHEN** exact tokenizer counting exceeds `MaximumStoredMemoryTokens`
- **THEN** the Agent compresses old records, saves the summary and recent records, and stores the recounted token number in session metadata

#### Scenario: Compression fails
- **WHEN** the compression model call fails
- **THEN** the Agent emits a memory-compression-failed event and preserves the uncompressed session file

#### Scenario: Saving fails after a model result
- **WHEN** a final model result exists but the session file cannot be written
- **THEN** the Agent returns the final model result together with an `AgentMemorySaveFailed` result

### Requirement: Use one Agent.Run reasoning-and-tool loop
The main non-streaming request, main streaming request, background SubAgent-result continuation, SubAgent task, and future hosted applications SHALL use the same `Agent.Run` implementation. Service and application-host functions SHALL NOT copy its model-call, tool-call, token-count, current-run-compaction, memory-save, or memory-compression loop.

#### Scenario: Source contains one core loop
- **WHEN** the implementation is inspected
- **THEN** `Agent.Run` contains request preparation, exact token counting, model calling, tool execution, next-round decisions, current-run compaction, final result creation, and memory completion

### Requirement: Execute ordinary, MCP, and SubAgent tools through one registry
An Agent SHALL receive one registry containing ordinary tools, dynamically registered MCP tools, and any caller-installed `run_subagent` tool. `Agent.Run` SHALL use registry definitions for model calls and `Registry.Execute` with the current tool execution environment. Adding an MCP Server or hosted application SHALL NOT add a name-specific branch to `Agent.Run`.

#### Scenario: DeepSeek requests an MCP tool
- **WHEN** a returned tool name belongs to a configured MCP Server
- **THEN** `Agent.Run` calls the registered execution function with the tool arguments and current execution environment

#### Scenario: DeepSeek requests run_subagent
- **WHEN** the registered `run_subagent` tool is returned
- **THEN** its external execution function chooses child Agent execution environments and starts those child Agents

### Requirement: Keep application rules outside the Agent
Werewolf, script-murder, council, and other hosted applications SHALL own their participants, roles, turns, shared state, and application-specific prompts outside the `agent` package. They SHALL call `Agent.Run` for an individual participant action and SHALL NOT add application mode fields or application-specific condition branches to `Agent`.

#### Scenario: Werewolf host runs a night action
- **WHEN** the host needs one player to choose an action
- **THEN** the host constructs the task, working directory, and player conversation ID and calls the unchanged Agent

#### Scenario: Council host runs one speech
- **WHEN** the host needs one council member to speak
- **THEN** the host constructs the speech task, project directory, and member conversation ID and calls the unchanged Agent

#### Scenario: New application is added
- **WHEN** another application is implemented after v15
- **THEN** it adds an application package and Agent calls without modifying files under `agent/`

### Requirement: Emit distinct typed Agent events
The Agent SHALL emit different concrete event types for round start, text delta, tool start, tool success, tool failure, memory reference ready, current-run compaction, stored-memory compression, memory-save failure, and completion. Success and failure SHALL NOT share one event with optional fields.

#### Scenario: Memory reference is ready
- **WHEN** the Agent has resolved the working directory and session path
- **THEN** it emits an event containing the conversation ID and relative session path but no stored message body

#### Scenario: Tool execution succeeds
- **WHEN** `Registry.Execute` returns a result without an error
- **THEN** the receiver obtains started and succeeded events with the same tool-use ID and tool name

### Requirement: Encode SSE by Agent event type
`main.handleChatStream` and background-reply functions SHALL select SSE JSON by inspecting concrete Agent event types. They SHALL NOT inspect the first character of model text.

#### Scenario: Model text begins with an opening brace
- **WHEN** DeepSeek streams `{` as ordinary text
- **THEN** the SSE writer JSON-encodes it as text

### Requirement: Accept project directory and conversation ID in WebAgent
The browser WebAgent task form and chat HTTP request types SHALL contain `workingDirectory`, `conversationId`, and the current task message. The page SHALL show the active values and typed Agent events. It SHALL NOT present historical conversation cards, alternating chat bubbles, or a continuous chatbot feed.

#### Scenario: User starts a browser task
- **WHEN** the user submits a project directory, conversation ID, and task
- **THEN** the HTTP handler starts one Agent run with those exact values and the page displays its events and final result

### Requirement: Keep the change scope limited
The change SHALL NOT implement a new werewolf, script-murder, or council application, SHALL NOT modify current `RunCouncil`, and SHALL NOT add cancellation, retry, checkpoint, restart recovery, A2A, WebSocket, or MCP Server concurrency control.

#### Scenario: Completed scope is reviewed
- **WHEN** implementation files are inspected
- **THEN** changes are limited to reusable Agent execution, caller-supplied project and session environment, on-demand memory retrieval, model-accurate token accounting, memory and current-run compaction, tool execution environment, typed events, and the WebAgent task form
