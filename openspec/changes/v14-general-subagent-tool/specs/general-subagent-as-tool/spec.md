## ADDED Requirements

### Requirement: Register one general SubAgent tool with fixed JSON input
The Go service SHALL register one ordinary tool named `run_subagent` in the existing main Agent `tool.Registry`. Its input SHALL be one JSON object with a required `subAgentTasks` array. Every array element SHALL contain a non-empty string `taskId` and a non-empty string `task`. The JSON Schema SHALL set `minItems` to 1 and `maxItems` to 5.

#### Scenario: Main Agent receives the tool definition
- **WHEN** `service.Run` calls `registry.GetDefinitions()` after service startup
- **THEN** the returned definitions contain `run_subagent` with the required `subAgentTasks` array and its `taskId` and `task` fields

#### Scenario: Main Agent supplies invalid JSON tool arguments
- **WHEN** `registry.Execute("run_subagent", toolArguments)` receives a missing or non-array `subAgentTasks`, an empty array, or an element with a missing, non-string, or whitespace-only `taskId` or `task`
- **THEN** the tool returns an argument error before starting any SubAgent

#### Scenario: Main Agent repeats a task ID
- **WHEN** two elements in `subAgentTasks` contain the same `taskId`
- **THEN** the tool returns an argument error before starting any SubAgent

### Requirement: Read the parallel SubAgent limit from application configuration
`config.Config` SHALL contain `MaximumParallelSubAgents`. `config.Load` SHALL read `MAX_PARALLEL_SUBAGENTS`, use 5 when the environment variable is empty or invalid, and SHALL never return a value greater than 5.

#### Scenario: Service uses the default configuration
- **WHEN** `config.Load` creates the application configuration without an override
- **THEN** `MaximumParallelSubAgents` equals 5

#### Scenario: Tool input exceeds the configured limit
- **WHEN** `subAgentTasks` contains more elements than `MaximumParallelSubAgents`
- **THEN** `run_subagent` returns an argument error before starting any SubAgent

#### Scenario: Environment configuration exceeds the hard upper limit
- **WHEN** `MAX_PARALLEL_SUBAGENTS` contains an integer greater than 5
- **THEN** `config.Load` sets `MaximumParallelSubAgents` to 5

#### Scenario: Environment configuration is invalid
- **WHEN** `MAX_PARALLEL_SUBAGENTS` is not an integer from 1 through 5
- **THEN** `config.Load` sets `MaximumParallelSubAgents` to 5

### Requirement: Start one temporary SubAgent for every supplied task
The `run_subagent` execution function SHALL call `service.RunSubAgentsInParallel` with the validated task list, the current application configuration, and a copied tool registry. `RunSubAgentsInParallel` SHALL start one goroutine for every supplied task and SHALL run all supplied tasks concurrently. Every goroutine SHALL call `RunSubAgent` with that task's exact `task` string.

#### Scenario: Main Agent supplies three tasks and the configured limit is five
- **WHEN** `run_subagent` receives three valid task objects
- **THEN** `RunSubAgentsInParallel` starts three goroutines and each goroutine calls `RunSubAgent` once

#### Scenario: Each SubAgent receives only its own task
- **WHEN** the task with `taskId` equal to `compile-check` contains `task` equal to `读取 Go 文件并列出编译错误`
- **THEN** that SubAgent's first DeepSeek call contains exactly `读取 Go 文件并列出编译错误` as its only user message

#### Scenario: Parent conversation contains earlier messages
- **WHEN** the main Agent conversation has existing history before calling `run_subagent`
- **THEN** no SubAgent copies those messages

### Requirement: Give every SubAgent current ordinary tools without recursive delegation
The main Agent tool registry supplied to `RunSubAgentsInParallel` SHALL be copied when `run_subagent` starts. The copied registry SHALL contain tools registered at that moment, including running MCP Server tools, and SHALL exclude `run_subagent`. Every concurrently running SubAgent SHALL use that copied registry.

#### Scenario: MCP tool was registered before delegation
- **WHEN** an MCP tool is present in the main Agent registry before `run_subagent` executes
- **THEN** every SubAgent tool definition includes that MCP tool

#### Scenario: SubAgent tool definitions are created
- **WHEN** `RunSubAgent` calls `availableSubAgentTools.GetDefinitions()`
- **THEN** the result does not contain `run_subagent`

#### Scenario: Main registry changes after copying
- **WHEN** the main registry adds or removes a tool after the SubAgent registry has been copied
- **THEN** the already-running SubAgents continue using the existing copied registry

### Requirement: Execute each SubAgent DeepSeek and tool loop
`RunSubAgent` SHALL call the existing `service.Chat` with its own temporary history, the general SubAgent system prompt, current DeepSeek configuration, copied tool definitions, and a maximum response size of 4096 tokens. It SHALL execute returned tool calls, append their results, and continue until DeepSeek returns no tool calls or 12 rounds have run.

#### Scenario: SubAgent returns text without a tool call
- **WHEN** DeepSeek returns final text and no tool call
- **THEN** `RunSubAgent` returns that text to `RunSubAgentsInParallel`

#### Scenario: SubAgent requests an allowed tool
- **WHEN** DeepSeek returns a tool call for a tool in the copied registry
- **THEN** `RunSubAgent` calls `availableSubAgentTools.Execute`, appends the result as `tool_result`, and makes the next DeepSeek call

#### Scenario: SubAgent tool execution fails
- **WHEN** an allowed tool returns an error
- **THEN** `RunSubAgent` writes the error text into that tool's `tool_result` and continues the next DeepSeek round

#### Scenario: SubAgent reaches 12 rounds
- **WHEN** DeepSeek still requests tools after the twelfth SubAgent round
- **THEN** `RunSubAgent` returns an `agent_limit_reached` error for that task

### Requirement: Return every SubAgent result in fixed JSON output
The `run_subagent` tool SHALL return one JSON object containing `maximumParallelSubAgents` and `results`. The `results` array SHALL preserve the order of `subAgentTasks`. Every result SHALL contain `taskId`, `status`, `result`, and `error`. A successful task SHALL use `status` equal to `completed`, its final text in `result`, and an empty `error`. A failed task SHALL use `status` equal to `failed`, an empty `result`, and its error text in `error`.

#### Scenario: Two SubAgents finish in a different order
- **WHEN** the second supplied task finishes before the first supplied task
- **THEN** the returned `results` array still places the first task's result before the second task's result

#### Scenario: One SubAgent fails while another succeeds
- **WHEN** one `RunSubAgent` call returns an error and another returns final text
- **THEN** the JSON contains one `failed` result and one `completed` result without discarding either result

#### Scenario: Non-streaming main Agent receives the JSON
- **WHEN** `service.Run` executes `run_subagent`
- **THEN** its next DeepSeek call contains the complete JSON string in the matching `tool_result`

#### Scenario: Streaming main Agent receives the JSON
- **WHEN** `service.RunStream` executes `run_subagent`
- **THEN** all SubAgents finish internally and its next DeepSeek call contains the complete JSON string in the matching `tool_result`

### Requirement: Do not persist separate SubAgent conversations
`RunSubAgent` SHALL NOT create a `service.Store`, conversation ID, or session JSON file. The main Agent SHALL persist only its normal user message and final assistant answer according to the existing behavior.

#### Scenario: Multiple SubAgents finish
- **WHEN** `RunSubAgentsInParallel` finishes all supplied tasks
- **THEN** `workspace/data/sessions/` contains no new session created for any SubAgent task

### Requirement: Keep the v14 scope limited to temporary general SubAgents
The v14 implementation SHALL NOT add a separate router, named specialist Agents, handoff, new HTTP routes, new web controls, background tasks, task cancellation, or SubAgents that remain alive after `run_subagent` returns.

#### Scenario: Service starts after v14 implementation
- **WHEN** the main Agent registry is inspected
- **THEN** v14 has added only the general `run_subagent` Agent tool and no fixed-role Agent tools
