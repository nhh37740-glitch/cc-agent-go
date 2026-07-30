package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"cc-agent-go/model"
)

func TestSameConversationIDUsesDifferentFilesInTwoProjects(t *testing.T) {
	firstWorkingDirectory := t.TempDir()
	secondWorkingDirectory := t.TempDir()
	conversationStore := NewProjectConversationStore()
	for _, workingDirectory := range []string{
		firstWorkingDirectory,
		secondWorkingDirectory,
	} {
		_, saveConversationError := conversationStore.AppendConversationTurn(
			workingDirectory,
			"player-1",
			[]model.Message{{
				Role: "user",
				Content: []model.MessageContentBlock{
					model.TextContentBlock{Text: workingDirectory},
				},
			}},
			1, 1, 1, 1000,
		)
		if saveConversationError != nil {
			t.Fatal(saveConversationError)
		}
	}
	firstSessionJSON, _ := os.ReadFile(filepath.Join(
		firstWorkingDirectory, ".cc-agent", "sessions", "player-1.json",
	))
	secondSessionJSON, _ := os.ReadFile(filepath.Join(
		secondWorkingDirectory, ".cc-agent", "sessions", "player-1.json",
	))
	if string(firstSessionJSON) == string(secondSessionJSON) {
		t.Fatal("两个项目目录的同名会话文件内容相同")
	}
}

func TestFirstUserTextTruncatesAtCompleteUTF8Character(t *testing.T) {
	completeUserText := strings.Repeat("中文", 30)
	generatedTitle := firstUserText([]model.Message{{
		Role: "user",
		Content: []model.MessageContentBlock{
			model.TextContentBlock{Text: completeUserText},
		},
	}})
	expectedTitle := string([]rune(completeUserText)[:50])
	if generatedTitle != expectedTitle || !utf8.ValidString(generatedTitle) {
		t.Fatalf("generated title = %q, want %q", generatedTitle, expectedTitle)
	}
}

func TestConversationIDCannotEscapeProjectSessionsDirectory(t *testing.T) {
	conversationStore := NewProjectConversationStore()
	_, buildSessionPathError := conversationStore.SessionFilePath(
		t.TempDir(),
		"../../outside",
	)
	if buildSessionPathError == nil {
		t.Fatal("path traversal conversation ID was accepted")
	}
}
