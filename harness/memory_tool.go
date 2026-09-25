package harness

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"cc-agent-go/model"
	"cc-agent-go/tool"
)

const defaultRecentMessages = 20

// harnessMemoryTool 让 Harness 读取自己的会话记忆。Harness 没有 bash，
// 无法像普通 Agent 那样用 rg 等命令读自己的会话文件，因此需要专门工具。
type harnessMemoryTool struct {
	runtime *Runtime
}

func newHarnessMemoryTool(runtime *Runtime) *harnessMemoryTool {
	return &harnessMemoryTool{runtime: runtime}
}

func (harnessMemoryTool) Name() string { return "memory" }

func (harnessMemoryTool) Description() string {
	return `读取你自己（Harness）的会话记忆。
recentMessages 指定返回最近多少条消息（默认 20）。
pattern 是 Go 正则表达式，只保留内容匹配的消息，效果等同 rg 搜索但不依赖外部命令。
返回会话标题、消息总数和匹配的消息。`
}

func (harnessMemoryTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"recentMessages": map[string]any{
				"type":        "integer",
				"description": "返回最近多少条消息，默认 20",
			},
			"pattern": map[string]any{
				"type":        "string",
				"description": "Go 正则表达式；提供时只返回内容匹配的消息",
			},
		},
	}
}

type rememberedMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

func (memoryToolForHarness *harnessMemoryTool) Execute(
	toolArguments map[string]any,
	executionEnvironment tool.ToolExecutionEnvironment,
) (string, error) {
	recentMessages := defaultRecentMessages
	if rawRecentMessages, present := toolArguments["recentMessages"]; present {
		numericRecentMessages, isNumber := rawRecentMessages.(float64)
		if !isNumber || numericRecentMessages < 1 {
			return "", fmt.Errorf("recentMessages 必须是大于等于 1 的整数")
		}
		recentMessages = int(numericRecentMessages)
	}

	var messagePattern *regexp.Regexp
	if rawPattern, present := toolArguments["pattern"]; present {
		patternText, isString := rawPattern.(string)
		if !isString {
			return "", fmt.Errorf("pattern 必须是字符串")
		}
		compiledPattern, compileError := regexp.Compile(patternText)
		if compileError != nil {
			return "", fmt.Errorf("pattern 正则无效: %w", compileError)
		}
		messagePattern = compiledPattern
	}

	sessionJSON, loadConversationError :=
		memoryToolForHarness.runtime.dependencies.ConversationStore.LoadConversation(
			memoryToolForHarness.runtime.workingDirectory,
			HarnessConversationID,
		)
	if loadConversationError != nil {
		return "", fmt.Errorf("读取 Harness 会话记忆失败: %w", loadConversationError)
	}
	// 会话文件尚未创建时 LoadConversation 返回 (nil, nil)，按空会话处理。
	if sessionJSON == nil {
		memoryResultForEmptySession, encodeError := json.Marshal(map[string]any{
			"conversationId": HarnessConversationID,
			"title":          "",
			"messageCount":   0,
			"returnedCount":  0,
			"messages":       []rememberedMessage{},
			"note":           "Harness 会话文件尚未创建，当前没有可检索的记忆",
		})
		if encodeError != nil {
			return "", fmt.Errorf("编码空记忆结果失败: %w", encodeError)
		}
		return string(memoryResultForEmptySession), nil
	}

	allMessages := sessionJSON.Messages
	startIndex := len(allMessages) - recentMessages
	if startIndex < 0 {
		startIndex = 0
	}
	matchedMessages := make([]rememberedMessage, 0, recentMessages)
	for _, message := range allMessages[startIndex:] {
		messageText := extractMessageText(message)
		if messagePattern != nil && !messagePattern.MatchString(messageText) {
			continue
		}
		matchedMessages = append(matchedMessages, rememberedMessage{
			Role: message.Role,
			Text: messageText,
		})
	}

	memoryResult, encodeError := json.Marshal(map[string]any{
		"conversationId": sessionJSON.ConversationId,
		"title":          sessionJSON.Title,
		"messageCount":   len(allMessages),
		"returnedCount":  len(matchedMessages),
		"messages":       matchedMessages,
	})
	if encodeError != nil {
		return "", fmt.Errorf("编码记忆结果失败: %w", encodeError)
	}
	return string(memoryResult), nil
}

// extractMessageText 把一条消息的全部内容块拼成一段文字，
// 工具块用占位说明保留可搜索的痕迹。
func extractMessageText(message model.Message) string {
	textParts := make([]string, 0, len(message.Content))
	for _, contentBlock := range message.Content {
		switch concreteContentBlock := contentBlock.(type) {
		case model.TextContentBlock:
			textParts = append(textParts, concreteContentBlock.Text)
		case model.ToolUseContentBlock:
			textParts = append(
				textParts,
				fmt.Sprintf("[调用工具 %s]", concreteContentBlock.Name),
			)
		case model.ToolResultContentBlock:
			textParts = append(
				textParts,
				"[工具结果] "+concreteContentBlock.Content,
			)
		}
	}
	return strings.Join(textParts, "\n")
}
