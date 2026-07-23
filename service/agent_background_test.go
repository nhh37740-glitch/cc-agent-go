package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cc-agent-go/model"
	"cc-agent-go/tool"
)

func TestContinueConversationAfterSubAgentsStreamLoadsLatestHistoryAndHidesInternalInput(
	t *testing.T,
) {
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
	applicationConfig.SessionsDir = t.TempDir()
	applicationConfig.CompressionThreshold = 100000
	conversationStore := NewStore(applicationConfig.SessionsDir)
	const conversationID = "background-callback-conversation"

	saveExistingConversationError := conversationStore.AppendTurn(
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
		5,
		nil,
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
		func(toolArguments map[string]any) (string, error) {
			return "不应执行", nil
		},
	)
	if registerCallbackToolError != nil {
		t.Fatalf("register callback tool: %v", registerCallbackToolError)
	}
	backgroundReplyText, returnedConversationID, continueConversationError :=
		ContinueConversationAfterSubAgentsStream(
			[]SubAgentResult{
				{
					TaskID: "research",
					Status: subAgentStatusCompleted,
					Result: "SubAgent 完成的结果",
					Error:  "",
				},
			},
			conversationID,
			"main agent system prompt",
			applicationConfig,
			callbackToolRegistry,
			conversationStore,
			func(token string) {
				streamedReply.WriteString(token)
			},
		)
	if continueConversationError != nil {
		t.Fatalf(
			"ContinueConversationAfterSubAgentsStream: %v",
			continueConversationError,
		)
	}
	if returnedConversationID != conversationID {
		t.Fatalf(
			"conversation ID = %q, want %q",
			returnedConversationID,
			conversationID,
		)
	}
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

	if len(receivedDeepSeekRequest.Messages) != 3 {
		t.Fatalf(
			"DeepSeek message count = %d, want 3",
			len(receivedDeepSeekRequest.Messages),
		)
	}
	if firstTextInMessage(receivedDeepSeekRequest.Messages[1]) !=
		"最新的主 Agent 回复" {
		t.Fatalf(
			"latest saved message = %q",
			firstTextInMessage(receivedDeepSeekRequest.Messages[1]),
		)
	}
	internalSubAgentResultText :=
		firstTextInMessage(receivedDeepSeekRequest.Messages[2])
	if !strings.Contains(
		internalSubAgentResultText,
		"SubAgent 完成的结果",
	) {
		t.Fatalf(
			"internal SubAgent result message = %q",
			internalSubAgentResultText,
		)
	}

	savedMessages, loadSavedMessagesError :=
		conversationStore.LoadMessages(conversationID)
	if loadSavedMessagesError != nil {
		t.Fatalf("load saved messages: %v", loadSavedMessagesError)
	}
	if len(savedMessages) != 3 {
		t.Fatalf(
			"saved message count = %d, want 3",
			len(savedMessages),
		)
	}
	if savedMessages[2].Role != "assistant" ||
		firstTextInMessage(savedMessages[2]) != "后台主 Agent 回复" {
		t.Fatalf(
			"saved final message = %#v",
			savedMessages[2],
		)
	}
	for _, savedMessage := range savedMessages {
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
