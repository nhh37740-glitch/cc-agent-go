# Refactor baseline

Recorded before implementation on branch `agent/agent-learning-roadmap`.

## Existing behavior captured by tests

- `service/agent_background_test.go`: the background callback loaded the complete
  saved conversation, appended one internal SubAgent-result message, called the
  model with an empty tool table, and saved only the returned assistant message.
- `service/subagent_test.go`: `RunSubAgent` owned a separate model/tool loop,
  forwarded tool success and failure results, used an 8,000-character limit,
  honored task-specific maximum rounds, and preserved partial results.
- `main_test.go`: `run_subagent` copied the shared registry without
  `run_subagent`; dynamically registered MCP tools remained available to child
  work.
- `service/store.go` before deletion: `Store` retained one fixed sessions
  directory, accumulated output tokens, used a configured compression threshold,
  and saved uncompressed records when compression failed.
- `tool/bash.go` before the change: `BashTool.workspace` retained one directory
  selected at process startup.

## Baseline command

`go test ./...` passed before the refactor. The refactor tests now assert the new
project-directory behavior while retaining the same external DeepSeek, MCP,
SubAgent status, error-code, and SSE outcomes.
