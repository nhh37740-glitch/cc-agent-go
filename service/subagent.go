package service

import (
	"errors"
	"fmt"
	"os"

	"cc-agent-go/agent"
	"cc-agent-go/config"
	"cc-agent-go/memory"
	"cc-agent-go/modeltoken"
	"cc-agent-go/tool"
)

const GeneralSubAgentToolName = "run_subagent"
const subAgentMaximumOutputTokens = defaultMaximumOutputTokens
const subAgentMaximumToolResultCharacters = 8000

const generalSubAgentSystemPrompt = `你是主 Agent 创建的临时通用 SubAgent。
只完成 HostedAgentTaskInput 中写明的一个任务。
任务需要工具时必须实际调用工具。
完成后只返回准备交给主 Agent 的任务结果。`

const subAgentStatusCompleted = "completed"
const subAgentStatusFailed = "failed"
const subAgentStatusLimitReached = "limit_reached"

type SubAgentTask struct {
	TaskID        string `json:"taskId"`
	Task          string `json:"task"`
	MaximumRounds int    `json:"maximumRounds"`
}

type SubAgentResult struct {
	TaskID string `json:"taskId"`
	Status string `json:"status"`
	Result string `json:"result"`
	Error  string `json:"error"`
}

type CompletedSubAgentResultsCallback func(
	parentConversationID string,
	subAgentResults []SubAgentResult,
)

type SubAgentRunEnvironment struct {
	WorkingDirectory         string
	ConversationID           string
	ConversationStore        *memory.ProjectConversationStore
	TokenCounter             agent.AgentTokenCounter
	ModelContextWindowTokens int
}

type SubAgentHostEnvironment struct {
	WorkingDirectory         string
	ConversationStore        *memory.ProjectConversationStore
	TokenCounter             agent.AgentTokenCounter
	ModelContextWindowTokens int
}

type indexedSubAgentResult struct {
	InputIndex     int
	SubAgentResult SubAgentResult
}

func RunSubAgentsInBackground(
	parentConversationID string,
	subAgentTasks []SubAgentTask,
	applicationConfig config.Config,
	availableSubAgentTools *tool.Registry,
	completedResultsCallback CompletedSubAgentResultsCallback,
	hostEnvironments ...SubAgentHostEnvironment,
) {
	go func() {
		subAgentResults := RunSubAgentsInParallel(
			subAgentTasks,
			applicationConfig,
			availableSubAgentTools,
			hostEnvironments...,
		)
		if completedResultsCallback != nil {
			completedResultsCallback(parentConversationID, subAgentResults)
		}
	}()
}

func RunSubAgentsInParallel(
	subAgentTasks []SubAgentTask,
	applicationConfig config.Config,
	availableSubAgentTools *tool.Registry,
	hostEnvironments ...SubAgentHostEnvironment,
) []SubAgentResult {
	hostEnvironment, buildHostEnvironmentError :=
		resolveSubAgentHostEnvironment(applicationConfig, hostEnvironments)
	if buildHostEnvironmentError != nil {
		failedResults := make([]SubAgentResult, len(subAgentTasks))
		for taskIndex, subAgentTask := range subAgentTasks {
			failedResults[taskIndex] = SubAgentResult{
				TaskID: subAgentTask.TaskID,
				Status: subAgentStatusFailed,
				Error:  buildHostEnvironmentError.Error(),
			}
		}
		return failedResults
	}

	orderedSubAgentResults := make([]SubAgentResult, len(subAgentTasks))
	completedSubAgentResults := make(chan indexedSubAgentResult, len(subAgentTasks))
	for inputIndex, subAgentTask := range subAgentTasks {
		go func(currentInputIndex int, currentSubAgentTask SubAgentTask) {
			subAgentConversationID := GenerateConversationId()
			subAgentFinalText, runSubAgentError := RunSubAgent(
				currentSubAgentTask.Task,
				currentSubAgentTask.MaximumRounds,
				applicationConfig,
				availableSubAgentTools,
				SubAgentRunEnvironment{
					WorkingDirectory:         hostEnvironment.WorkingDirectory,
					ConversationID:           subAgentConversationID,
					ConversationStore:        hostEnvironment.ConversationStore,
					TokenCounter:             hostEnvironment.TokenCounter,
					ModelContextWindowTokens: hostEnvironment.ModelContextWindowTokens,
				},
			)
			subAgentResult := SubAgentResult{
				TaskID: currentSubAgentTask.TaskID,
				Status: subAgentStatusCompleted,
				Result: subAgentFinalText,
			}
			if runSubAgentError != nil {
				var applicationError *AppError
				if errors.As(runSubAgentError, &applicationError) &&
					applicationError.Kind == ErrorAgentLimit {
					subAgentResult.Status = subAgentStatusLimitReached
				} else {
					subAgentResult.Status = subAgentStatusFailed
				}
				subAgentResult.Error = runSubAgentError.Error()
			}
			completedSubAgentResults <- indexedSubAgentResult{
				InputIndex:     currentInputIndex,
				SubAgentResult: subAgentResult,
			}
		}(inputIndex, subAgentTask)
	}
	for receivedResultCount := 0; receivedResultCount < len(subAgentTasks); receivedResultCount++ {
		completedResult := <-completedSubAgentResults
		orderedSubAgentResults[completedResult.InputIndex] = completedResult.SubAgentResult
	}
	return orderedSubAgentResults
}

func RunSubAgent(
	subAgentTask string,
	maximumRounds int,
	applicationConfig config.Config,
	availableSubAgentTools *tool.Registry,
	runEnvironments ...SubAgentRunEnvironment,
) (string, error) {
	runEnvironment, resolveEnvironmentError :=
		resolveSubAgentRunEnvironment(applicationConfig, runEnvironments)
	if resolveEnvironmentError != nil {
		return "", resolveEnvironmentError
	}
	agentRunResult, runAgentError := RunAgentTask(
		agent.HostedAgentTaskInput{Task: subAgentTask},
		agent.AgentExecutionEnvironment{
			WorkingDirectory: runEnvironment.WorkingDirectory,
			ConversationID:   runEnvironment.ConversationID,
		},
		generalSubAgentSystemPrompt,
		applicationConfig,
		availableSubAgentTools,
		runEnvironment.ConversationStore,
		runEnvironment.TokenCounter,
		runEnvironment.ModelContextWindowTokens,
		AgentRunOptions{MaximumRounds: maximumRounds},
	)
	if runAgentError != nil {
		return "", runAgentError
	}
	if maximumRoundsResult, reachedMaximumRounds :=
		agentRunResult.(agent.AgentMaximumRoundsReachedResult); reachedMaximumRounds {
		return maximumRoundsResult.PartialText, newSubAgentLimitError(maximumRounds, nil)
	}
	return agentRunResult.FinalText(), nil
}

func resolveSubAgentHostEnvironment(
	applicationConfig config.Config,
	hostEnvironments []SubAgentHostEnvironment,
) (SubAgentHostEnvironment, error) {
	if len(hostEnvironments) > 0 {
		return hostEnvironments[0], nil
	}
	workingDirectory, readWorkingDirectoryError := os.Getwd()
	if readWorkingDirectoryError != nil {
		return SubAgentHostEnvironment{}, readWorkingDirectoryError
	}
	tokenCounter, maximumContextTokens, loadTokenCounterError :=
		loadConfiguredTokenCounter(applicationConfig.Model)
	if loadTokenCounterError != nil {
		return SubAgentHostEnvironment{}, loadTokenCounterError
	}
	return SubAgentHostEnvironment{
		WorkingDirectory:         workingDirectory,
		ConversationStore:        memory.NewProjectConversationStore(),
		TokenCounter:             tokenCounter,
		ModelContextWindowTokens: maximumContextTokens,
	}, nil
}

func resolveSubAgentRunEnvironment(
	applicationConfig config.Config,
	runEnvironments []SubAgentRunEnvironment,
) (SubAgentRunEnvironment, error) {
	if len(runEnvironments) > 0 {
		return runEnvironments[0], nil
	}
	hostEnvironment, resolveHostEnvironmentError :=
		resolveSubAgentHostEnvironment(applicationConfig, nil)
	if resolveHostEnvironmentError != nil {
		return SubAgentRunEnvironment{}, resolveHostEnvironmentError
	}
	return SubAgentRunEnvironment{
		WorkingDirectory:         hostEnvironment.WorkingDirectory,
		ConversationID:           GenerateConversationId(),
		ConversationStore:        hostEnvironment.ConversationStore,
		TokenCounter:             hostEnvironment.TokenCounter,
		ModelContextWindowTokens: hostEnvironment.ModelContextWindowTokens,
	}, nil
}

func loadConfiguredTokenCounter(
	modelName string,
) (agent.AgentTokenCounter, int, error) {
	tokenizerConfiguration, loadConfigurationError :=
		config.LoadModelTokenizerConfiguration(modelName)
	if loadConfigurationError != nil {
		return nil, 0, loadConfigurationError
	}
	tokenCounter, createTokenCounterError :=
		modeltoken.NewHuggingFaceJSONTokenCounter(tokenizerConfiguration)
	if createTokenCounterError != nil {
		return nil, 0, createTokenCounterError
	}
	return tokenCounter, tokenizerConfiguration.MaximumContextTokens, nil
}

func newSubAgentLimitError(maximumRounds int, cause error) error {
	limitReason := fmt.Errorf(
		"达到最大工具调用轮数 %d，返回已经获得的部分结果",
		maximumRounds,
	)
	if cause != nil {
		limitReason = fmt.Errorf("%w: %v", limitReason, cause)
	}
	return NewAppError(ErrorAgentLimit, "service.RunSubAgent", 0, limitReason)
}
