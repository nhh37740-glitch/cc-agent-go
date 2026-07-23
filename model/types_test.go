package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestToolUseContentBlockAlwaysWritesInput(t *testing.T) {
	toolUseContentBlockJSON, encodeToolUseError := json.Marshal(
		ToolUseContentBlock{
			ID:   "tool-1",
			Name: "mcp_playwright__browser_snapshot",
		},
	)
	if encodeToolUseError != nil {
		t.Fatalf("encode tool use content block: %v", encodeToolUseError)
	}

	if !strings.Contains(string(toolUseContentBlockJSON), `"input":{}`) {
		t.Fatalf(
			"tool use JSON must contain empty input object: %s",
			toolUseContentBlockJSON,
		)
	}
}

func TestTextContentBlockDoesNotWriteInput(t *testing.T) {
	textContentBlockJSON, encodeTextError := json.Marshal(
		TextContentBlock{Text: "hello"},
	)
	if encodeTextError != nil {
		t.Fatalf("encode text content block: %v", encodeTextError)
	}

	if strings.Contains(string(textContentBlockJSON), `"input"`) {
		t.Fatalf(
			"text JSON must not contain input: %s",
			textContentBlockJSON,
		)
	}
}

func TestMessageDecodesConcreteContentBlockTypes(t *testing.T) {
	messageJSON := []byte(`{
		"role":"assistant",
		"content":[
			{"type":"text","text":"opening browser"},
			{"type":"tool_use","id":"tool-1","name":"browser_snapshot","input":{}}
		]
	}`)

	var decodedMessage Message
	if decodeMessageError := json.Unmarshal(
		messageJSON,
		&decodedMessage,
	); decodeMessageError != nil {
		t.Fatalf("decode message: %v", decodeMessageError)
	}

	if _, isTextContentBlock :=
		decodedMessage.Content[0].(TextContentBlock); !isTextContentBlock {
		t.Fatalf(
			"first content type = %T, want TextContentBlock",
			decodedMessage.Content[0],
		)
	}
	if _, isToolUseContentBlock :=
		decodedMessage.Content[1].(ToolUseContentBlock); !isToolUseContentBlock {
		t.Fatalf(
			"second content type = %T, want ToolUseContentBlock",
			decodedMessage.Content[1],
		)
	}
}
