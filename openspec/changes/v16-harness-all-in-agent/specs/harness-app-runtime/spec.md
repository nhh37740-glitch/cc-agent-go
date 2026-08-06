# harness-app-runtime

## ADDED Requirements

### Requirement: Declarative application configuration
An application SHALL be a directory `<workingDirectory>/.cc-agent/apps/<appSlug>/` containing an `app.json` and a static web page. `app.json` SHALL declare `name`, `displayName`, `webui`, a `flow` of type `round_robin` with `rounds`, and a `participants` list where each participant declares `name`, `displayName`, `rolePrompt`, `conversationId`, and `maximumRounds`. Participant conversation IDs SHALL follow `app-<appSlug>-participant-<participantSlug>`. The Go code SHALL NOT contain any application name; every application behavior SHALL come from its configuration.

#### Scenario: Coding agent scaffolds an app
- **WHEN** a managed coding agent writes `apps/council/app.json` and `apps/council/index.html` through its bash tool
- **THEN** `GET /api/harness/apps` lists the app with its display name and participant count without any Go change

#### Scenario: Invalid app.json
- **WHEN** an `app.json` misses a participant's `rolePrompt` or declares an unknown flow type
- **THEN** loading that app returns a validation error naming the invalid field

### Requirement: Round-robin app runner
The app runner SHALL accept a `topic` input and execute `rounds` rounds; in each round every participant runs in list order via `host.RunParticipantTurn` with that participant's conversation ID, role prompt, filtered MCP tools, and a task composed of the flow rule text, the topic, and the transcript so far. The runner SHALL emit `app_round`, `app_speech` (participant, round, text), `app_completed`, and `app_failed` events to `ConversationEventReceivers` under channel `app-<appSlug>`. Only one run per app SHALL execute at a time.

#### Scenario: Three-elders debate
- **WHEN** an app declares three participants and two rounds and the user posts a topic
- **THEN** six participant runs execute in order and the event stream contains six `app_speech` events followed by `app_completed`

#### Scenario: Concurrent run rejected
- **WHEN** a second run is started while the first is still running
- **THEN** `POST /api/apps/{name}/run` returns a conflict error and no new participant run starts

### Requirement: App HTTP endpoints and static page
The service SHALL expose `GET /api/harness/apps?workingDirectory=`, `GET /api/apps/{name}?workingDirectory=` (parsed configuration), `POST /api/apps/{name}/run?workingDirectory=` (body: `topic`; starts the runner in a background goroutine and returns immediately), `GET /api/apps/{name}/events?workingDirectory=` (SSE from channel `app-<appSlug>`), and `GET /apps/{name}/?workingDirectory=` serving the configured static page from the app directory. App names SHALL be validated as safe slugs and every resolved path SHALL remain inside the app's directory.

#### Scenario: User opens the generated council page
- **WHEN** the browser opens `/apps/council/?workingDirectory=<dir>`, posts a topic, and listens on `/api/apps/council/events`
- **THEN** the page displays each elder's speech as it is emitted and shows completion at the end

#### Scenario: Path traversal attempt
- **WHEN** an app name contains `..` or an absolute path segment
- **THEN** the endpoint returns `invalid_request` and no file outside the apps directory is read

### Requirement: Apps use agents as their backend
Every participant turn SHALL run through the same `agent.Agent.Run` used by the WebAgent and the harness, with the participant's own session file persisting memory across runs. App participant tool registries SHALL follow the same base-plus-global-MCP composition as managed harness agents, with role prompts guiding tool usage.

#### Scenario: Elder remembers earlier debates
- **WHEN** the same council app runs twice in one project
- **THEN** each participant's second run can read its earlier session file via its memory reference, because its conversation ID is stable
