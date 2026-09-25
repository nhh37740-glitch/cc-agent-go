package service

import (
	"context"
	"os"
	"testing"

	"cc-agent-go/agent"
	"cc-agent-go/config"
	"cc-agent-go/model"
	"cc-agent-go/modeltoken"
)

func TestLocalDeepSeekTokenizerAndProviderUsageForFixedRequest(t *testing.T) {
	if os.Getenv("RUN_DEEPSEEK_TOKEN_INTEGRATION") != "1" {
		t.Skip("set RUN_DEEPSEEK_TOKEN_INTEGRATION=1 to call DeepSeek")
	}
	applicationConfig := config.Load()
	tokenizerConfiguration, loadTokenizerConfigurationError :=
		config.LoadModelTokenizerConfiguration(applicationConfig.Model)
	if loadTokenizerConfigurationError != nil {
		t.Fatal(loadTokenizerConfigurationError)
	}
	tokenCounter, createTokenCounterError :=
		modeltoken.NewHuggingFaceJSONTokenCounter(tokenizerConfiguration)
	if createTokenCounterError != nil {
		t.Fatal(createTokenCounterError)
	}
	defer tokenCounter.Close()

	fixedSystemPrompt := "Reply with exactly: TOKEN_CHECK_OK"
	fixedMessages := []model.Message{{
		Role: "user",
		Content: []model.MessageContentBlock{
			model.TextContentBlock{Text: "token usage fixture 2026-07-30"},
		},
	}}
	localPreparedTokens, countPreparedRequestError :=
		tokenCounter.CountPreparedModelRequest(agent.PreparedModelRequest{
			ModelName:           applicationConfig.Model,
			MaximumOutputTokens: 32,
			SystemPrompt:        fixedSystemPrompt,
			Messages:            fixedMessages,
		})
	if countPreparedRequestError != nil {
		t.Fatal(countPreparedRequestError)
	}
	providerResponse, callProviderError := Chat(
		context.Background(),
		fixedMessages,
		fixedSystemPrompt,
		applicationConfig,
		nil,
		32,
	)
	if callProviderError != nil {
		t.Fatal(callProviderError)
	}
	t.Logf(
		"localPreparedTokens=%d providerInputTokens=%d measuredDifference=%d",
		localPreparedTokens,
		providerResponse.InputTokens,
		providerResponse.InputTokens-localPreparedTokens,
	)
	if providerResponse.InputTokens < 1 {
		t.Fatal("DeepSeek usage.input_tokens was not returned")
	}
}
