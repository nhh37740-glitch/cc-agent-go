FROM golang:1.26.4-bookworm AS build

RUN apt-get update && apt-get install -y --no-install-recommends python3 ripgrep \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG SOURCE_COMMIT
ARG SOURCE_TREE
ARG SOURCE_DIRTY
ENV SOURCE_COMMIT=${SOURCE_COMMIT} SOURCE_TREE=${SOURCE_TREE} SOURCE_DIRTY=${SOURCE_DIRTY}
RUN python3 scripts/release.py

FROM golang:1.26.4-bookworm
RUN apt-get update && apt-get install -y --no-install-recommends python3 ripgrep \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --create-home --uid 10001 agent \
    && mkdir -p /app/logs /workspace \
    && chown -R agent:agent /app /workspace
WORKDIR /app
COPY --from=build /src/dist/cc-agent-go /usr/local/bin/cc-agent-go
COPY --from=build /src/config/model_tokenizers.json /app/config/model_tokenizers.json
COPY --from=build /src/config/mcp_servers.json /app/config/mcp_servers.json
COPY --from=build /src/mcp/protocol/2025-11-25/messages.json /app/mcp/protocol/2025-11-25/messages.json
COPY --from=build /src/harness/system_prompt.md /app/harness/system_prompt.md
COPY --from=build /src/harness/managed_agent_prompt.md /app/harness/managed_agent_prompt.md
COPY --from=build /src/tokenizers /app/tokenizers
COPY --from=build /src/personalities /app/personalities
COPY --from=build /src/index.html /src/harness.html /src/council.html /app/
ENV HOME=/home/agent GOTELEMETRY=off
USER agent
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s \
  CMD curl --fail --silent http://127.0.0.1:8080/ > /dev/null || exit 1
CMD ["cc-agent-go"]
