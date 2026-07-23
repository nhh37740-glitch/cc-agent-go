package service

import (
	"strings"
	"testing"
	"unicode/utf8"

	"cc-agent-go/model"
)

func TestFirstUserTextTruncatesAtCompleteUTF8Character(t *testing.T) {
	completeUserText := strings.Repeat("中文", 30)
	messages := []model.Message{
		{
			Role: "user",
			Content: []model.MessageContentBlock{
				model.TextContentBlock{Text: completeUserText},
			},
		},
	}

	generatedTitle := firstUserText(messages)
	expectedTitle := string([]rune(completeUserText)[:40])

	if generatedTitle != expectedTitle {
		t.Fatalf("generated title = %q, want %q", generatedTitle, expectedTitle)
	}
	if !utf8.ValidString(generatedTitle) {
		t.Fatalf("generated title is not valid UTF-8: %q", generatedTitle)
	}
}

func TestReadableSessionTitleRecreatesOldCorruptedTitle(t *testing.T) {
	completeUserText := "必须调用 run_subagent 一次，并发执行两个任务"
	sessionJson := model.SessionJson{
		Title: "必须调用 run_subagent 一次，并��",
		Messages: []model.Message{
			{
				Role: "user",
				Content: []model.MessageContentBlock{
					model.TextContentBlock{Text: completeUserText},
				},
			},
		},
	}

	readableTitle := readableSessionTitle(sessionJson)

	if readableTitle != completeUserText {
		t.Fatalf("readable title = %q, want %q", readableTitle, completeUserText)
	}
}
