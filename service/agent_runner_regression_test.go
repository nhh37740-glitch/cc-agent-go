package service

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"cc-agent-go/agent"
	"cc-agent-go/config"
	"cc-agent-go/memory"
	"cc-agent-go/tool"
)

// regressionTokenCounter 是回归测试使用的固定 token 计数器。
type regressionTokenCounter struct{}

func (regressionTokenCounter) CountPreparedModelRequest(
	agent.PreparedModelRequest,
) (int, error) {
	return 10, nil
}

func (regressionTokenCounter) CountText(string) (int, error) {
	return 10, nil
}

func (regressionTokenCounter) TruncateText(
	textToTruncate string,
	maximumTokens int,
) (agent.TokenTruncationResult, error) {
	return agent.TokenTruncationResult{
		Text:            textToTruncate,
		OriginalTokens:  10,
		TruncatedTokens: 10,
		WasTruncated:    false,
	}, nil
}

// regressionEventKind 把具体事件类型映射为可读名称，用于断言事件顺序。
func regressionEventKind(receivedEvent agent.AgentEvent) string {
	switch receivedEvent.(type) {
	case agent.AgentMemoryReferenceReadyEvent:
		return "memory_reference_ready"
	case agent.AgentRoundStartedEvent:
		return "round_started"
	case agent.AgentTextDeltaEvent:
		return "text_delta"
	case agent.AgentToolStartedEvent:
		return "tool_started"
	case agent.AgentToolSucceededEvent:
		return "tool_succeeded"
	case agent.AgentToolFailedEvent:
		return "tool_failed"
	case agent.AgentCompletedEvent:
		return "completed"
	default:
		return "other"
	}
}

// TestRunAgentTaskEventOrderRegression 固定 service.RunAgentTask 的
// 模型—工具循环事件顺序、工具执行环境和会话保存结果（v16 任务 1.2）。
// DeepSeek 由 httptest 假服务器扮演：第一次返回工具调用，第二次返回最终文字。
func TestRunAgentTaskEventOrderRegression(t *testing.T) {
	requestCount := 0
	fakeDeepSeekServer := httptest.NewServer(http.HandlerFunc(
		func(responseWriter http.ResponseWriter, httpRequest *http.Request) {
			requestCount++
			responseWriter.Header().Set("Content-Type", "application/json")
			if requestCount == 1 {
				fmt.Fprint(responseWriter, `{"content":[{"type":"tool_use","id":"toolu_1","name":"regression_fake_tool","input":{"x":1}}],"stop_reason":"tool_use","usage":{"input_tokens":11,"output_tokens":5}}`)
				return
			}
			fmt.Fprint(responseWriter, `{"content":[{"type":"text","text":"final answer"}],"stop_reason":"end_turn","usage":{"input_tokens":21,"output_tokens":7}}`)
		},
	))
	defer fakeDeepSeekServer.Close()

	workingDirectory := t.TempDir()
	var receivedToolEnvironment tool.ToolExecutionEnvironment
	availableTools := tool.NewRegistry()
	registerToolError := availableTools.RegisterFunctionTool(
		"regression_fake_tool",
		"回归测试工具",
		map[string]any{"type": "object", "properties": map[string]any{}},
		func(
			toolArguments map[string]any,
			executionEnvironment tool.ToolExecutionEnvironment,
		) (string, error) {
			receivedToolEnvironment = executionEnvironment
			return "fake-result", nil
		},
	)
	if registerToolError != nil {
		t.Fatalf("注册测试工具失败: %v", registerToolError)
	}

	conversationStore := memory.NewProjectConversationStore()
	receivedEventKinds := make([]string, 0, 8)
	runResult, runAgentError := RunAgentTask(
		agent.UserTaskInput{Message: "执行回归任务"},
		agent.AgentExecutionEnvironment{
			WorkingDirectory: workingDirectory,
			ConversationID:   "conv-1",
		},
		"测试系统提示词",
		config.Config{
			ApiKey:               "regression-test-key",
			ApiEndpoint:          fakeDeepSeekServer.URL,
			Model:                "fake-model",
			CompressionThreshold: 100000,
		},
		availableTools,
		conversationStore,
		regressionTokenCounter{},
		1000000,
		AgentRunOptions{
			StreamText: false,
			ReceiveEvent: func(receivedEvent agent.AgentEvent) {
				receivedEventKinds = append(
					receivedEventKinds,
					regressionEventKind(receivedEvent),
				)
			},
		},
	)
	if runAgentError != nil {
		t.Fatalf("RunAgentTask 返回错误: %v", runAgentError)
	}
	if runResult.FinalText() != "final answer" {
		t.Fatalf("最终文字 = %q，期望 %q", runResult.FinalText(), "final answer")
	}
	if requestCount != 2 {
		t.Fatalf("DeepSeek 请求次数 = %d，期望 2", requestCount)
	}

	expectedEventKinds := []string{
		"memory_reference_ready",
		"round_started",
		"tool_started",
		"tool_succeeded",
		"round_started",
		"completed",
	}
	if len(receivedEventKinds) != len(expectedEventKinds) {
		t.Fatalf("事件序列 = %v，期望 %v", receivedEventKinds, expectedEventKinds)
	}
	for eventIndex, expectedKind := range expectedEventKinds {
		if receivedEventKinds[eventIndex] != expectedKind {
			t.Fatalf("事件序列 = %v，期望 %v", receivedEventKinds, expectedEventKinds)
		}
	}

	if receivedToolEnvironment.WorkingDirectory != workingDirectory ||
		receivedToolEnvironment.ConversationID != "conv-1" {
		t.Fatalf("工具执行环境 = %+v，期望目录 %q 会话 %q",
			receivedToolEnvironment, workingDirectory, "conv-1")
	}

	savedConversation, loadConversationError :=
		conversationStore.LoadConversation(workingDirectory, "conv-1")
	if loadConversationError != nil {
		t.Fatalf("读取保存的会话失败: %v", loadConversationError)
	}
	if savedConversation == nil || len(savedConversation.Messages) != 2 {
		t.Fatalf("保存的会话 = %+v，期望 2 条消息（任务 + 最终回复）", savedConversation)
	}
}
