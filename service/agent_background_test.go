package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cc-agent-go/agent"
	"cc-agent-go/memory"
	"cc-agent-go/model"
	"cc-agent-go/tool"
)

func TestContinueConversationAfterSubAgentsStreamLoadsLatestHistoryAndHidesInternalInput(
	t *testing.T,
) {
	projectWorkingDirectory := t.TempDir()
	t.Chdir(projectWorkingDirectory)
	var receivedDeepSeekRequest recordedDeepSeekRequest
	deepSeekTestServer := httptest.NewServer(http.HandlerFunc(
		func(responseWriter http.ResponseWriter, httpRequest *http.Request) {
			decodeRequestError := json.NewDecoder(httpRequest.Body).Decode(
				&receivedDeepSeekRequest,
			)
			if decodeRequestError != nil {
				t.Errorf("decode DeepSeek request: %v", decodeRequestError)
				return
			}

			responseTextJSON, _ := json.Marshal("后台主 Agent 回复")
			responseWriter.Header().Set(
				"Content-Type",
				"text/event-stream",
			)
			_, _ = fmt.Fprintf(
				responseWriter,
				"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10}}}\n\n"+
					"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%s}}\n\n"+
					"data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":5}}\n\n"+
					"data: {\"type\":\"message_stop\"}\n\n",
				responseTextJSON,
			)
		},
	))
	t.Cleanup(deepSeekTestServer.Close)

	applicationConfig :=
		newSubAgentTestConfig(deepSeekTestServer.URL)
	applicationConfig.CompressionThreshold = 100000
	const conversationID = "background-callback-conversation"

	projectConversationStore := memory.NewProjectConversationStore()
	_, saveExistingConversationError := projectConversationStore.AppendConversationTurn(
		projectWorkingDirectory,
		conversationID,
		[]model.Message{
			{
				Role: "user",
				Content: []model.MessageContentBlock{
					model.TextContentBlock{Text: "原来的用户消息"},
				},
			},
			{
				Role: "assistant",
				Content: []model.MessageContentBlock{
					model.TextContentBlock{Text: "最新的主 Agent 回复"},
				},
			},
		},
		5, 0, 5, 100000,
	)
	if saveExistingConversationError != nil {
		t.Fatalf(
			"save existing conversation: %v",
			saveExistingConversationError,
		)
	}

	var streamedReply strings.Builder
	callbackToolRegistry := tool.NewRegistry()
	registerCallbackToolError := callbackToolRegistry.RegisterFunctionTool(
		"forbidden_callback_tool",
		"回调主 Agent 不应收到的工具",
		map[string]any{"type": "object"},
		func(
			toolArguments map[string]any,
			_ tool.ToolExecutionEnvironment,
		) (string, error) {
			return "不应执行", nil
		},
	)
	if registerCallbackToolError != nil {
		t.Fatalf("register callback tool: %v", registerCallbackToolError)
	}
	internalContinuationTask, encodeInternalTaskError :=
		agent.EncodeInternalContinuationTask([]SubAgentResult{
			{
				TaskID: "research",
				Status: subAgentStatusCompleted,
				Result: "SubAgent 完成的结果",
				Error:  "",
			},
		})
	if encodeInternalTaskError != nil {
		t.Fatal(encodeInternalTaskError)
	}
	tokenCounter, modelContextWindowTokens, loadTokenCounterError :=
		loadConfiguredTokenCounter(applicationConfig.Model)
	if loadTokenCounterError != nil {
		t.Fatal(loadTokenCounterError)
	}
	backgroundReplyResult, continueConversationError := RunAgentTask(
		internalContinuationTask,
		agent.AgentExecutionEnvironment{
			WorkingDirectory: projectWorkingDirectory,
			ConversationID:   conversationID,
		},
		"main agent system prompt",
		applicationConfig,
		tool.NewRegistry(),
		projectConversationStore,
		tokenCounter,
		modelContextWindowTokens,
		AgentRunOptions{
			StreamText: true,
			ReceiveEvent: func(receivedAgentEvent agent.AgentEvent) {
				if textDeltaEvent, isTextDelta :=
					receivedAgentEvent.(agent.AgentTextDeltaEvent); isTextDelta {
					streamedReply.WriteString(textDeltaEvent.Text)
				}
			},
		},
	)
	if continueConversationError != nil {
		t.Fatalf(
			"ContinueConversationAfterSubAgentsStream: %v",
			continueConversationError,
		)
	}
	backgroundReplyText := backgroundReplyResult.FinalText()
	if backgroundReplyText != "后台主 Agent 回复" {
		t.Fatalf("reply = %q", backgroundReplyText)
	}
	if streamedReply.String() != "后台主 Agent 回复" {
		t.Fatalf("streamed reply = %q", streamedReply.String())
	}
	if len(receivedDeepSeekRequest.Tools) != 0 {
		t.Fatalf(
			"callback tool count = %d, want 0",
			len(receivedDeepSeekRequest.Tools),
		)
	}

	if len(receivedDeepSeekRequest.Messages) != 1 {
		t.Fatalf(
			"DeepSeek message count = %d, want 1（未配置 KeepRecentMemoryTokens 时不装配历史）",
			len(receivedDeepSeekRequest.Messages),
		)
	}
	internalSubAgentResultText :=
		firstTextInMessage(receivedDeepSeekRequest.Messages[0])
	if !strings.Contains(
		internalSubAgentResultText,
		"SubAgent 完成的结果",
	) {
		t.Fatalf(
			"internal SubAgent result message = %q",
			internalSubAgentResultText,
		)
	}

	savedConversation, loadSavedMessagesError :=
		projectConversationStore.LoadConversation(
			projectWorkingDirectory,
			conversationID,
		)
	if loadSavedMessagesError != nil {
		t.Fatalf("load saved messages: %v", loadSavedMessagesError)
	}
	if savedConversation == nil || len(savedConversation.Messages) != 3 {
		t.Fatalf(
			"saved message count = %d, want 3",
			len(savedConversation.Messages),
		)
	}
	if savedConversation.Messages[2].Role != "assistant" ||
		firstTextInMessage(savedConversation.Messages[2]) != "后台主 Agent 回复" {
		t.Fatalf(
			"saved final message = %#v",
			savedConversation.Messages[2],
		)
	}
	for _, savedMessage := range savedConversation.Messages {
		if strings.Contains(
			firstTextInMessage(savedMessage),
			"SubAgent 完成的结果",
		) {
			t.Fatal("internal SubAgent result was saved as a visible message")
		}
	}
}

func firstTextInMessage(message model.Message) string {
	for _, messageContentBlock := range message.Content {
		if textContentBlock, isTextContentBlock :=
			messageContentBlock.(model.TextContentBlock); isTextContentBlock {
			return textContentBlock.Text
		}
	}
	return ""
}
