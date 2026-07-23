package service

import (
	"fmt"

	"cc-agent-go/config"
	"cc-agent-go/model"
	"cc-agent-go/tool"
)

const subAgentMaximumRounds = 12
const subAgentMaximumOutputTokens = 4096
const subAgentMaximumToolResultCharacters = 8000

const generalSubAgentSystemPrompt = `你是主 Agent 创建的临时通用 SubAgent。
只完成用户消息中写明的一个任务，不读取或猜测主 Agent 的其他对话内容。
任务需要使用工具时必须实际调用工具。
完成后只返回准备交给主 Agent 的任务结果。`

const subAgentStatusCompleted = "completed"
const subAgentStatusFailed = "failed"

// SubAgentTask 保存主 Agent 交给一个临时 SubAgent 的任务编号和完整任务。
type SubAgentTask struct {
	TaskID string `json:"taskId"`
	Task   string `json:"task"`
}

// SubAgentResult 保存一个临时 SubAgent 的最终文字或错误。
type SubAgentResult struct {
	TaskID string `json:"taskId"`
	Status string `json:"status"`
	Result string `json:"result"`
	Error  string `json:"error"`
}

// CompletedSubAgentResultsCallback 在同一次工具调用的全部 SubAgent 结束后执行。
type CompletedSubAgentResultsCallback func(
	parentConversationID string,
	subAgentResults []SubAgentResult,
)

type indexedSubAgentResult struct {
	InputIndex     int
	SubAgentResult SubAgentResult
}

// RunSubAgentsInBackground 启动后台 goroutine 后立即返回。
// 后台 goroutine 收齐全部结果以后，只调用一次 completedResultsCallback。
func RunSubAgentsInBackground(
	parentConversationID string,
	subAgentTasks []SubAgentTask,
	applicationConfig config.Config,
	availableSubAgentTools *tool.Registry,
	completedResultsCallback CompletedSubAgentResultsCallback,
) {
	go func() {
		subAgentResults := RunSubAgentsInParallel(
			subAgentTasks,
			applicationConfig,
			availableSubAgentTools,
		)
		if completedResultsCallback != nil {
			completedResultsCallback(
				parentConversationID,
				subAgentResults,
			)
		}
	}()
}

// RunSubAgentsInParallel 为每个任务启动一个 goroutine，并按输入顺序返回结果。
func RunSubAgentsInParallel(
	subAgentTasks []SubAgentTask,
	applicationConfig config.Config,
	availableSubAgentTools *tool.Registry,
) []SubAgentResult {
	orderedSubAgentResults := make([]SubAgentResult, len(subAgentTasks))
	completedSubAgentResults := make(
		chan indexedSubAgentResult,
		len(subAgentTasks),
	)

	for inputIndex, subAgentTask := range subAgentTasks {
		go func(currentInputIndex int, currentSubAgentTask SubAgentTask) {
			subAgentFinalText, runSubAgentError := RunSubAgent(
				currentSubAgentTask.Task,
				applicationConfig,
				availableSubAgentTools,
			)

			subAgentResult := SubAgentResult{
				TaskID: currentSubAgentTask.TaskID,
				Status: subAgentStatusCompleted,
				Result: subAgentFinalText,
				Error:  "",
			}
			if runSubAgentError != nil {
				subAgentResult.Status = subAgentStatusFailed
				subAgentResult.Result = ""
				subAgentResult.Error = runSubAgentError.Error()
			}

			completedSubAgentResults <- indexedSubAgentResult{
				InputIndex:     currentInputIndex,
				SubAgentResult: subAgentResult,
			}
		}(inputIndex, subAgentTask)
	}

	for receivedResultCount := 0; receivedResultCount < len(subAgentTasks); receivedResultCount++ {
		completedSubAgentResult := <-completedSubAgentResults
		orderedSubAgentResults[completedSubAgentResult.InputIndex] =
			completedSubAgentResult.SubAgentResult
	}

	return orderedSubAgentResults
}

// RunSubAgent 使用一份新的临时消息记录完成一个任务。
// 它不读取或保存主 Agent 会话。
func RunSubAgent(
	subAgentTask string,
	applicationConfig config.Config,
	availableSubAgentTools *tool.Registry,
) (string, error) {
	subAgentMessageHistory := []model.Message{{
		Role: "user",
		Content: []model.MessageContentBlock{
			model.TextContentBlock{Text: subAgentTask},
		},
	}}
	subAgentToolDefinitions := availableSubAgentTools.GetDefinitions()

	for subAgentRound := 0; subAgentRound < subAgentMaximumRounds; subAgentRound++ {
		deepSeekResponse, deepSeekCallError := Chat(
			subAgentMessageHistory,
			generalSubAgentSystemPrompt,
			applicationConfig,
			subAgentToolDefinitions,
			subAgentMaximumOutputTokens,
		)
		if deepSeekCallError != nil {
			return "", fmt.Errorf(
				"SubAgent 第 %d 轮 API 调用失败: %w",
				subAgentRound+1,
				deepSeekCallError,
			)
		}

		if len(deepSeekResponse.ToolCalls) == 0 {
			return deepSeekResponse.Text, nil
		}

		subAgentAssistantMessage := model.Message{Role: "assistant"}
		if deepSeekResponse.Text != "" {
			subAgentAssistantMessage.Content = append(
				subAgentAssistantMessage.Content,
				model.TextContentBlock{
					Text: deepSeekResponse.Text,
				},
			)
		}
		for _, subAgentToolCall := range deepSeekResponse.ToolCalls {
			subAgentAssistantMessage.Content = append(
				subAgentAssistantMessage.Content,
				model.ToolUseContentBlock{
					ID:    subAgentToolCall.ID,
					Name:  subAgentToolCall.Name,
					Input: subAgentToolCall.Input,
				},
			)
		}
		subAgentMessageHistory = append(
			subAgentMessageHistory,
			subAgentAssistantMessage,
		)

		subAgentToolResultMessage := model.Message{Role: "user"}
		for _, subAgentToolCall := range deepSeekResponse.ToolCalls {
			subAgentToolResult, executeSubAgentToolError :=
				availableSubAgentTools.Execute(
					subAgentToolCall.Name,
					subAgentToolCall.Input,
				)
			if executeSubAgentToolError != nil {
				subAgentToolResult = fmt.Sprintf(
					"工具执行错误: %v",
					executeSubAgentToolError,
				)
			}

			if len(subAgentToolResult) > subAgentMaximumToolResultCharacters {
				originalToolResultLength := len(subAgentToolResult)
				subAgentToolResult =
					subAgentToolResult[:subAgentMaximumToolResultCharacters] +
						fmt.Sprintf(
							"\n\n[结果过长，已截断。原始长度 %d 字符，显示前 %d 字符]",
							originalToolResultLength,
							subAgentMaximumToolResultCharacters,
						)
			}

			subAgentToolResultMessage.Content = append(
				subAgentToolResultMessage.Content,
				model.ToolResultContentBlock{
					ToolUseID: subAgentToolCall.ID,
					Content:   subAgentToolResult,
				},
			)
		}
		subAgentMessageHistory = append(
			subAgentMessageHistory,
			subAgentToolResultMessage,
		)
	}

	return "", NewAppError(
		ErrorAgentLimit,
		"service.RunSubAgent",
		0,
		fmt.Errorf(
			"达到最大工具调用轮数 %d，SubAgent 仍未给出最终文字",
			subAgentMaximumRounds,
		),
	)
}
