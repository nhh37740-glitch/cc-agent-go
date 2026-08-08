package modeltoken

import (
	"encoding/json"
	"fmt"

	"cc-agent-go/agent"
	"cc-agent-go/config"
	tokenizers "github.com/amikos-tech/pure-tokenizers"
)

type HuggingFaceJSONTokenCounter struct {
	loadedTokenizer *tokenizers.Tokenizer
}

func NewHuggingFaceJSONTokenCounter(
	modelTokenizerConfiguration config.ModelTokenizerConfiguration,
) (*HuggingFaceJSONTokenCounter, error) {
	loadedTokenizer, loadTokenizerError :=
		tokenizers.FromFile(modelTokenizerConfiguration.TokenizerFile)
	if loadTokenizerError != nil {
		return nil, fmt.Errorf("加载 Hugging Face tokenizer 文件失败: %w", loadTokenizerError)
	}
	return &HuggingFaceJSONTokenCounter{loadedTokenizer: loadedTokenizer}, nil
}

func (tokenCounter *HuggingFaceJSONTokenCounter) Close() error {
	return tokenCounter.loadedTokenizer.Close()
}

func (tokenCounter *HuggingFaceJSONTokenCounter) CountText(
	textToCount string,
) (int, error) {
	encodedText, encodeTextError := tokenCounter.loadedTokenizer.Encode(textToCount)
	if encodeTextError != nil {
		return 0, fmt.Errorf("tokenizer 编码文字失败: %w", encodeTextError)
	}
	return len(encodedText.IDs), nil
}

func (tokenCounter *HuggingFaceJSONTokenCounter) CountPreparedModelRequest(
	preparedModelRequest agent.PreparedModelRequest,
) (int, error) {
	requestJSON, encodeRequestError := json.Marshal(preparedModelRequest)
	if encodeRequestError != nil {
		return 0, fmt.Errorf("编码待发送模型请求失败: %w", encodeRequestError)
	}
	return tokenCounter.CountText(string(requestJSON))
}

func (tokenCounter *HuggingFaceJSONTokenCounter) TruncateText(
	textToTruncate string,
	maximumTokens int,
) (agent.TokenTruncationResult, error) {
	originalTokens, countOriginalTextError := tokenCounter.CountText(textToTruncate)
	if countOriginalTextError != nil {
		return agent.TokenTruncationResult{}, countOriginalTextError
	}
	if originalTokens <= maximumTokens {
		return agent.TokenTruncationResult{
			Text:            textToTruncate,
			OriginalTokens:  originalTokens,
			TruncatedTokens: originalTokens,
			WasTruncated:    false,
		}, nil
	}

	textRunes := []rune(textToTruncate)
	leftRuneIndex := 0
	rightRuneIndex := len(textRunes)
	for leftRuneIndex < rightRuneIndex {
		middleRuneIndex := (leftRuneIndex + rightRuneIndex + 1) / 2
		middleTokens, countMiddleTextError :=
			tokenCounter.CountText(string(textRunes[:middleRuneIndex]))
		if countMiddleTextError != nil {
			return agent.TokenTruncationResult{}, countMiddleTextError
		}
		if middleTokens <= maximumTokens {
			leftRuneIndex = middleRuneIndex
		} else {
			rightRuneIndex = middleRuneIndex - 1
		}
	}
	truncatedText := string(textRunes[:leftRuneIndex])
	truncatedTokens, countTruncatedTextError := tokenCounter.CountText(truncatedText)
	if countTruncatedTextError != nil {
		return agent.TokenTruncationResult{}, countTruncatedTextError
	}
	return agent.TokenTruncationResult{
		Text:            truncatedText,
		OriginalTokens:  originalTokens,
		TruncatedTokens: truncatedTokens,
		WasTruncated:    true,
	}, nil
}
