package httpapi

import (
	"net/http"

	"cc-agent-go/agent"
	"cc-agent-go/config"
	"cc-agent-go/harness"
	"cc-agent-go/mcp"
	"cc-agent-go/memory"
	"cc-agent-go/service"
	"cc-agent-go/tool"
)

type Dependencies struct {
	LoadConfig                 func() config.Config
	Personalities              map[string]string
	Registry                   *tool.Registry
	MCPServerManager           *mcp.MCPServerManager
	ConversationEventReceivers *service.ConversationEventReceivers
	ConversationExecutionLocks *service.ConversationExecutionLocks
	ConversationRunRegistry    *service.ConversationRunRegistry
	ProjectConversationStore   *memory.ProjectConversationStore
	TokenCounter               agent.AgentTokenCounter
	ModelContextWindowTokens   int
	HarnessPrompts             harness.Prompts
	HarnessPromptsLoadError    error
	ApplicationLogFilePath     string
}

type Server struct {
	loadConfig                 func() config.Config
	personalities              map[string]string
	registry                   *tool.Registry
	mcpServerManager           *mcp.MCPServerManager
	conversationEventReceivers *service.ConversationEventReceivers
	conversationExecutionLocks *service.ConversationExecutionLocks
	conversationRunRegistry    *service.ConversationRunRegistry
	projectConversationStore   *memory.ProjectConversationStore
	tokenCounter               agent.AgentTokenCounter
	modelContextWindowTokens   int
	harnessPrompts             harness.Prompts
	harnessPromptsLoadError    error
	applicationLogFilePath     string
}

func NewServer(dependencies Dependencies) *Server {
	if dependencies.LoadConfig == nil {
		dependencies.LoadConfig = config.Load
	}
	if dependencies.Registry == nil {
		dependencies.Registry = tool.NewRegistry()
	}
	if dependencies.ConversationEventReceivers == nil {
		dependencies.ConversationEventReceivers = service.NewConversationEventReceivers()
	}
	if dependencies.ConversationExecutionLocks == nil {
		dependencies.ConversationExecutionLocks = service.NewConversationExecutionLocks()
	}
	if dependencies.ConversationRunRegistry == nil {
		dependencies.ConversationRunRegistry = service.NewConversationRunRegistry()
	}
	if dependencies.ProjectConversationStore == nil {
		dependencies.ProjectConversationStore = memory.NewProjectConversationStore()
	}
	if dependencies.ApplicationLogFilePath == "" {
		dependencies.ApplicationLogFilePath = "logs/server.jsonl"
	}
	return &Server{
		loadConfig:                 dependencies.LoadConfig,
		personalities:              dependencies.Personalities,
		registry:                   dependencies.Registry,
		mcpServerManager:           dependencies.MCPServerManager,
		conversationEventReceivers: dependencies.ConversationEventReceivers,
		conversationExecutionLocks: dependencies.ConversationExecutionLocks,
		conversationRunRegistry:    dependencies.ConversationRunRegistry,
		projectConversationStore:   dependencies.ProjectConversationStore,
		tokenCounter:               dependencies.TokenCounter,
		modelContextWindowTokens:   dependencies.ModelContextWindowTokens,
		harnessPrompts:             dependencies.HarnessPrompts,
		harnessPromptsLoadError:    dependencies.HarnessPromptsLoadError,
		applicationLogFilePath:     dependencies.ApplicationLogFilePath,
	}
}

func (server *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/chat", server.handleChat)
	mux.HandleFunc("POST /api/chat/stream", server.handleChatStream)
	mux.HandleFunc("GET /api/mcp/servers", server.handleListMCPServers)
	mux.HandleFunc("PUT /api/mcp/servers", server.handleSelectMCPServers)
	mux.HandleFunc("GET /api/conversations", server.handleListConversations)
	mux.HandleFunc("GET /api/conversations/{id}/events", server.handleConversationEvents)
	mux.HandleFunc("GET /api/conversations/{id}", server.handleGetConversation)
	mux.HandleFunc("POST /api/conversations/{id}/stop", server.handleStopConversation)
	mux.HandleFunc("DELETE /api/conversations/{id}", server.handleDeleteConversation)
	mux.HandleFunc("GET /api/logs", server.handleListRecentApplicationLogs)
	mux.HandleFunc("POST /api/council", server.handleCouncil)
	mux.HandleFunc("POST /api/council/stream", server.handleCouncilStream)
	mux.HandleFunc("POST /api/harness/chat/stream", server.handleHarnessChatStream)
	mux.HandleFunc("GET /api/harness/agents", server.handleHarnessAgents)
	mux.HandleFunc("GET /api/harness/agents/{name}/memory", server.handleHarnessAgentMemory)
	mux.HandleFunc("POST /api/harness/agents/{name}/heartbeat", server.handleHarnessAgentHeartbeat)
	mux.HandleFunc("GET /harness", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "harness.html")
	})
	mux.HandleFunc("GET /council", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "council.html")
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, "index.html")
	})
	return mux
}
