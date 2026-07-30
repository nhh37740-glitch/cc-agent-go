package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"cc-agent-go/model"
)

var safeConversationIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)

type ProjectConversationStore struct {
	conversationLocksMutex sync.Mutex
	conversationLocks      map[string]*sync.Mutex
}

func NewProjectConversationStore() *ProjectConversationStore {
	return &ProjectConversationStore{
		conversationLocks: make(map[string]*sync.Mutex),
	}
}

func (conversationStore *ProjectConversationStore) SessionFilePath(
	workingDirectory string,
	conversationID string,
) (string, error) {
	if !filepath.IsAbs(workingDirectory) {
		return "", fmt.Errorf("WorkingDirectory 必须是绝对路径")
	}
	if !safeConversationIDPattern.MatchString(conversationID) {
		return "", fmt.Errorf("ConversationID 只允许字母、数字、点、下划线和连字符，长度必须为 1 到 100")
	}
	sessionsDirectory := filepath.Join(workingDirectory, ".cc-agent", "sessions")
	sessionFilePath := filepath.Join(sessionsDirectory, conversationID+".json")
	cleanSessionsDirectory := filepath.Clean(sessionsDirectory)
	cleanSessionFilePath := filepath.Clean(sessionFilePath)
	if filepath.Dir(cleanSessionFilePath) != cleanSessionsDirectory {
		return "", fmt.Errorf("ConversationID 生成的文件不在项目 sessions 目录内")
	}
	return cleanSessionFilePath, nil
}

func (conversationStore *ProjectConversationStore) LoadConversation(
	workingDirectory string,
	conversationID string,
) (*model.SessionJson, error) {
	sessionFilePath, buildSessionFilePathError :=
		conversationStore.SessionFilePath(workingDirectory, conversationID)
	if buildSessionFilePathError != nil {
		return nil, buildSessionFilePathError
	}
	conversationLock := conversationStore.lockFor(sessionFilePath)
	conversationLock.Lock()
	defer conversationLock.Unlock()
	return readConversationFile(sessionFilePath)
}

func (conversationStore *ProjectConversationStore) SaveConversation(
	workingDirectory string,
	conversation *model.SessionJson,
) error {
	sessionFilePath, buildSessionFilePathError := conversationStore.SessionFilePath(
		workingDirectory,
		conversation.ConversationId,
	)
	if buildSessionFilePathError != nil {
		return buildSessionFilePathError
	}
	conversationLock := conversationStore.lockFor(sessionFilePath)
	conversationLock.Lock()
	defer conversationLock.Unlock()
	return writeConversationFile(sessionFilePath, conversation)
}

func (conversationStore *ProjectConversationStore) AppendConversationTurn(
	workingDirectory string,
	conversationID string,
	newMessages []model.Message,
	lastInputTokens int,
	lastOutputTokens int,
	storedMemoryTokens int,
	contextWindowLimitTokens int,
) (*model.SessionJson, error) {
	sessionFilePath, buildSessionFilePathError :=
		conversationStore.SessionFilePath(workingDirectory, conversationID)
	if buildSessionFilePathError != nil {
		return nil, buildSessionFilePathError
	}
	conversationLock := conversationStore.lockFor(sessionFilePath)
	conversationLock.Lock()
	defer conversationLock.Unlock()

	existingConversation, readConversationError := readConversationFile(sessionFilePath)
	if readConversationError != nil {
		return nil, readConversationError
	}
	now := float64(time.Now().UnixNano()) / 1e9
	if existingConversation == nil {
		existingConversation = &model.SessionJson{
			ConversationId:           conversationID,
			Title:                    firstUserText(newMessages),
			ContextWindowLimitTokens: contextWindowLimitTokens,
			CreatedAt:                now,
			Messages:                 []model.Message{},
		}
		if existingConversation.Title == "" {
			existingConversation.Title = "未命名会话"
		}
	}
	existingConversation.Messages = append(existingConversation.Messages, newMessages...)
	existingConversation.LastInputTokens = lastInputTokens
	existingConversation.LastOutputTokens = lastOutputTokens
	existingConversation.RunningTotalTokens += lastInputTokens + lastOutputTokens
	existingConversation.StoredMemoryTokens = storedMemoryTokens
	existingConversation.ContextWindowLimitTokens = contextWindowLimitTokens
	existingConversation.UpdatedAt = now
	if writeConversationError := writeConversationFile(
		sessionFilePath,
		existingConversation,
	); writeConversationError != nil {
		return nil, writeConversationError
	}
	return existingConversation, nil
}

func (conversationStore *ProjectConversationStore) SaveStoredMemoryTokens(
	workingDirectory string,
	conversationID string,
	storedMemoryTokens int,
) error {
	sessionFilePath, buildSessionFilePathError :=
		conversationStore.SessionFilePath(workingDirectory, conversationID)
	if buildSessionFilePathError != nil {
		return buildSessionFilePathError
	}
	conversationLock := conversationStore.lockFor(sessionFilePath)
	conversationLock.Lock()
	defer conversationLock.Unlock()
	conversation, readConversationError := readConversationFile(sessionFilePath)
	if readConversationError != nil {
		return readConversationError
	}
	if conversation == nil {
		return fmt.Errorf("会话文件不存在: %s", conversationID)
	}
	conversation.StoredMemoryTokens = storedMemoryTokens
	return writeConversationFile(sessionFilePath, conversation)
}

func (conversationStore *ProjectConversationStore) ReadSessionJSON(
	workingDirectory string,
	conversationID string,
) ([]byte, error) {
	sessionFilePath, buildSessionFilePathError :=
		conversationStore.SessionFilePath(workingDirectory, conversationID)
	if buildSessionFilePathError != nil {
		return nil, buildSessionFilePathError
	}
	return os.ReadFile(sessionFilePath)
}

func (conversationStore *ProjectConversationStore) ArchiveMessages(
	workingDirectory string,
	conversationID string,
	messages []model.Message,
) error {
	sessionFilePath, buildSessionFilePathError :=
		conversationStore.SessionFilePath(workingDirectory, conversationID)
	if buildSessionFilePathError != nil {
		return buildSessionFilePathError
	}
	archiveFilePath := strings.TrimSuffix(sessionFilePath, ".json") + ".archive.json"
	archiveMessages := []model.Message{}
	if existingArchiveJSON, readArchiveError := os.ReadFile(archiveFilePath); readArchiveError == nil {
		_ = json.Unmarshal(existingArchiveJSON, &archiveMessages)
	}
	archiveMessages = append(archiveMessages, messages...)
	archiveJSON, encodeArchiveError := json.MarshalIndent(archiveMessages, "", "  ")
	if encodeArchiveError != nil {
		return fmt.Errorf("编码会话归档失败: %w", encodeArchiveError)
	}
	return os.WriteFile(archiveFilePath, append(archiveJSON, '\n'), 0644)
}

func (conversationStore *ProjectConversationStore) ListConversations(
	workingDirectory string,
) ([]model.ConversationSummary, error) {
	sessionsDirectory := filepath.Join(workingDirectory, ".cc-agent", "sessions")
	sessionFiles, readSessionsDirectoryError := os.ReadDir(sessionsDirectory)
	if os.IsNotExist(readSessionsDirectoryError) {
		return []model.ConversationSummary{}, nil
	}
	if readSessionsDirectoryError != nil {
		return nil, readSessionsDirectoryError
	}
	conversationSummaries := []model.ConversationSummary{}
	for _, sessionFile := range sessionFiles {
		if sessionFile.IsDir() ||
			!strings.HasSuffix(sessionFile.Name(), ".json") ||
			strings.HasSuffix(sessionFile.Name(), ".archive.json") {
			continue
		}
		conversation, readConversationError :=
			readConversationFile(filepath.Join(sessionsDirectory, sessionFile.Name()))
		if readConversationError != nil || conversation == nil {
			continue
		}
		conversationSummaries = append(conversationSummaries, model.ConversationSummary{
			ConversationId:           conversation.ConversationId,
			Title:                    conversation.Title,
			UpdatedAt:                conversation.UpdatedAt,
			ContextWindowLimitTokens: conversation.ContextWindowLimitTokens,
			CurrentInputTokens:       conversation.LastInputTokens,
			CurrentOutputTokens:      conversation.LastOutputTokens,
			CurrentTotalTokens:       conversation.RunningTotalTokens,
			CurrentWindowTokens:      conversation.StoredMemoryTokens,
		})
	}
	sort.Slice(conversationSummaries, func(leftIndex int, rightIndex int) bool {
		return conversationSummaries[leftIndex].UpdatedAt >
			conversationSummaries[rightIndex].UpdatedAt
	})
	return conversationSummaries, nil
}

func (conversationStore *ProjectConversationStore) DeleteConversation(
	workingDirectory string,
	conversationID string,
) error {
	sessionFilePath, buildSessionFilePathError :=
		conversationStore.SessionFilePath(workingDirectory, conversationID)
	if buildSessionFilePathError != nil {
		return buildSessionFilePathError
	}
	deleteConversationError := os.Remove(sessionFilePath)
	if os.IsNotExist(deleteConversationError) {
		return nil
	}
	return deleteConversationError
}

func (conversationStore *ProjectConversationStore) lockFor(
	sessionFilePath string,
) *sync.Mutex {
	conversationStore.conversationLocksMutex.Lock()
	defer conversationStore.conversationLocksMutex.Unlock()
	if existingLock := conversationStore.conversationLocks[sessionFilePath]; existingLock != nil {
		return existingLock
	}
	newConversationLock := &sync.Mutex{}
	conversationStore.conversationLocks[sessionFilePath] = newConversationLock
	return newConversationLock
}

func readConversationFile(sessionFilePath string) (*model.SessionJson, error) {
	sessionJSON, readSessionError := os.ReadFile(sessionFilePath)
	if os.IsNotExist(readSessionError) {
		return nil, nil
	}
	if readSessionError != nil {
		return nil, fmt.Errorf("读取会话文件失败: %w", readSessionError)
	}
	var conversation model.SessionJson
	if decodeSessionError := json.Unmarshal(sessionJSON, &conversation); decodeSessionError != nil {
		return nil, fmt.Errorf("解包会话 JSON 失败: %w", decodeSessionError)
	}
	return &conversation, nil
}

func writeConversationFile(
	sessionFilePath string,
	conversation *model.SessionJson,
) error {
	if createSessionsDirectoryError := os.MkdirAll(
		filepath.Dir(sessionFilePath),
		0755,
	); createSessionsDirectoryError != nil {
		return fmt.Errorf("创建项目会话目录失败: %w", createSessionsDirectoryError)
	}
	sessionJSON, encodeSessionError := json.MarshalIndent(conversation, "", "  ")
	if encodeSessionError != nil {
		return fmt.Errorf("编码会话 JSON 失败: %w", encodeSessionError)
	}
	if writeSessionError := os.WriteFile(
		sessionFilePath,
		append(sessionJSON, '\n'),
		0644,
	); writeSessionError != nil {
		return fmt.Errorf("写入会话文件失败: %w", writeSessionError)
	}
	return nil
}

func firstUserText(messages []model.Message) string {
	for _, message := range messages {
		if message.Role != "user" {
			continue
		}
		for _, contentBlock := range message.Content {
			if textContentBlock, isText := contentBlock.(model.TextContentBlock); isText {
				titleRunes := []rune(textContentBlock.Text)
				if len(titleRunes) > 50 {
					titleRunes = titleRunes[:50]
				}
				return string(titleRunes)
			}
		}
	}
	return ""
}
