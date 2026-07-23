## ADDED Requirements

### Requirement: Start SubAgents without waiting for final results
The `run_subagent` tool SHALL start the supplied SubAgent tasks in goroutines and SHALL immediately return JSON containing every accepted `taskId` with `status` equal to `running`. The tool SHALL NOT wait for `RunSubAgent` to return before returning its own tool result.

#### Scenario: Main Agent starts two SubAgents
- **WHEN** the main Agent calls `run_subagent` with two valid tasks
- **THEN** the tool returns both task IDs with `status: "running"` while both SubAgents continue executing

### Requirement: Let the calling LLM choose each SubAgent task round limit
Every item in `subAgentTasks` SHALL contain a positive integer `maximumRounds`. The service SHALL pass that task-specific value to `RunSubAgent` and SHALL stop that SubAgent only after the selected number of DeepSeek and tool-processing rounds. The service SHALL NOT use one fixed round count for every SubAgent task.

#### Scenario: Browser task requests more rounds
- **WHEN** the main Agent supplies `maximumRounds: 30` for a browser-search task
- **THEN** that SubAgent may execute up to 30 rounds before returning `agent_limit_reached`

### Requirement: Reject a task round limit above the configured maximum
The Go configuration SHALL expose the highest allowed SubAgent round count. `run_subagent` SHALL reject a task whose `maximumRounds` is less than 1 or greater than that configured maximum.

#### Scenario: Main Agent requests too many rounds
- **WHEN** the configured maximum is 50 and a task supplies `maximumRounds: 80`
- **THEN** `run_subagent` returns a validation error before starting any SubAgent from that tool call

### Requirement: Call the main Agent after all started SubAgents finish
The background SubAgent function SHALL call one completion callback after every task from the same `run_subagent` call has completed or failed. The callback SHALL receive the parent `conversationId` and the complete ordered `SubAgentResult` array.

#### Scenario: SubAgents finish after the original chat reply
- **WHEN** all SubAgents from one tool call have produced results
- **THEN** the completion callback receives those results without requiring another model-selected tool call or another user message

### Requirement: Continue the latest parent conversation with SubAgent results
The completion callback SHALL wait until any current main Agent execution for the same `conversationId` has finished, load the latest saved conversation messages, append the completed SubAgent results for the next DeepSeek call, and execute the existing main Agent tool loop. The internal SubAgent-result message SHALL NOT be saved as a visible user message; the resulting main Agent assistant reply SHALL be saved.

#### Scenario: User sends another message while a SubAgent runs
- **WHEN** the main Agent has answered the new user message before the SubAgent completes
- **THEN** the completion callback loads that new exchange before calling DeepSeek

### Requirement: Keep chat SSE short and conversation event SSE long
`POST /api/chat/stream` SHALL finish and close after its one main Agent reply. `GET /api/conversations/{id}/events` SHALL keep running until the browser disconnects. Neither handler SHALL decide its connection lifetime by checking whether a SubAgent was started.

#### Scenario: Chat starts a background SubAgent
- **WHEN** `POST /api/chat/stream` finishes the main Agent reply that started the SubAgent
- **THEN** that response closes while the independent conversation event SSE remains available

### Requirement: Publish background main Agent replies to connected web pages
The service SHALL keep the event channels registered for each `conversationId`. A SubAgent completion callback SHALL publish a reply-start event, token events, and a reply-completed event containing the complete final text. Every web page currently connected to that conversation SHALL receive those events.

#### Scenario: Background reply completes
- **WHEN** the main Agent finishes processing SubAgent results
- **THEN** the connected web page displays one completed main Agent message with the complete reply text

### Requirement: Remove disconnected web event receivers
The conversation event handler SHALL stop when `request.Context().Done()` is closed and SHALL remove its channel from the channels registered for that `conversationId`.

#### Scenario: User leaves a conversation page
- **WHEN** the browser closes the conversation event SSE
- **THEN** the Go service removes that receiver and does not send later events to it

### Requirement: Keep the first background-callback scope limited
This change SHALL NOT add a task query tool, cancellation, retry, checkpoint, process restart recovery, or MCP Server concurrency control.

#### Scenario: Service tool definitions are listed
- **WHEN** the main Agent receives its tool definitions
- **THEN** no `get_subagent_results` tool is present
