package agent

import "cc-agent-go/model"

type PreparedModelRequest struct {
	ModelName           string           `json:"model"`
	MaximumOutputTokens int              `json:"max_tokens"`
	SystemPrompt        string           `json:"system"`
	Messages            []model.Message  `json:"messages"`
	ToolDefinitions     []map[string]any `json:"tools,omitempty"`
}

type TokenTruncationResult struct {
	Text            string
	OriginalTokens  int
	TruncatedTokens int
	WasTruncated    bool
}

type AgentTokenCounter interface {
	CountPreparedModelRequest(preparedModelRequest PreparedModelRequest) (int, error)
	CountText(textToCount string) (int, error)
	TruncateText(textToTruncate string, maximumTokens int) (TokenTruncationResult, error)
}
