package modeltoken

import (
	"testing"

	"cc-agent-go/config"
)

// Expected counts were generated with transformers.AutoTokenizer from
// deepseek-ai/DeepSeek-V4-Pro at revision
// b5968e9190ef611bbf34a7229255be88a0e937c1 using add_special_tokens=false.
func TestHuggingFaceJSONTokenCounterMatchesOfficialAutoTokenizerFixtures(
	t *testing.T,
) {
	tokenizerConfiguration, loadConfigurationError :=
		config.LoadModelTokenizerConfiguration("deepseek-v4-pro[1m]")
	if loadConfigurationError != nil {
		t.Fatal(loadConfigurationError)
	}
	tokenCounter, createTokenCounterError :=
		NewHuggingFaceJSONTokenCounter(tokenizerConfiguration)
	if createTokenCounterError != nil {
		t.Fatal(createTokenCounterError)
	}
	defer tokenCounter.Close()

	for _, fixture := range []struct {
		name           string
		text           string
		expectedTokens int
	}{
		{"Chinese", "你好，世界", 3},
		{"English", "hello world", 2},
		{"JSON tool", `{"name":"bash","input":{"command":"rg -n TODO ."}}`, 16},
		{"special token", "<｜tool▁calls▁begin｜>", 1},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			actualTokens, countTextError := tokenCounter.CountText(fixture.text)
			if countTextError != nil {
				t.Fatal(countTextError)
			}
			if actualTokens != fixture.expectedTokens {
				t.Fatalf("tokens=%d want=%d", actualTokens, fixture.expectedTokens)
			}
		})
	}
}
