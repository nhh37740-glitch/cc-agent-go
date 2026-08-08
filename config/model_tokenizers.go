package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const defaultModelTokenizerConfigurationFile = "config/model_tokenizers.json"

type ModelTokenizerConfiguration struct {
	TokenizerType        string `json:"tokenizerType"`
	TokenizerFile        string `json:"tokenizerFile"`
	MaximumContextTokens int    `json:"maximumContextTokens"`
	Source               string `json:"source"`
	Revision             string `json:"revision"`
	SHA256               string `json:"sha256"`
}

func LoadModelTokenizerConfiguration(
	modelName string,
) (ModelTokenizerConfiguration, error) {
	configurationFilePath := defaultModelTokenizerConfigurationFile
	modelTokenizerConfigurationJSON, readConfigurationError :=
		os.ReadFile(configurationFilePath)
	if os.IsNotExist(readConfigurationError) {
		_, currentSourceFile, _, callerInformationAvailable := runtime.Caller(0)
		if callerInformationAvailable {
			configurationFilePath = filepath.Join(
				filepath.Dir(currentSourceFile),
				"model_tokenizers.json",
			)
			modelTokenizerConfigurationJSON, readConfigurationError =
				os.ReadFile(configurationFilePath)
		}
	}
	if readConfigurationError != nil {
		return ModelTokenizerConfiguration{}, fmt.Errorf(
			"读取模型 tokenizer 配置失败: %w",
			readConfigurationError,
		)
	}
	modelConfigurations := map[string]ModelTokenizerConfiguration{}
	if decodeConfigurationError := json.Unmarshal(
		modelTokenizerConfigurationJSON,
		&modelConfigurations,
	); decodeConfigurationError != nil {
		return ModelTokenizerConfiguration{}, fmt.Errorf(
			"解包模型 tokenizer 配置失败: %w",
			decodeConfigurationError,
		)
	}
	modelConfiguration, modelExists := modelConfigurations[modelName]
	if !modelExists {
		return ModelTokenizerConfiguration{}, fmt.Errorf(
			"模型 %q 没有 tokenizer 配置",
			modelName,
		)
	}
	if modelConfiguration.TokenizerType != "huggingface_json" ||
		modelConfiguration.TokenizerFile == "" ||
		modelConfiguration.MaximumContextTokens < 1 {
		return ModelTokenizerConfiguration{}, fmt.Errorf(
			"模型 %q 的 tokenizer 配置不完整",
			modelName,
		)
	}
	if !filepath.IsAbs(modelConfiguration.TokenizerFile) {
		repositoryRoot := filepath.Dir(filepath.Dir(configurationFilePath))
		modelConfiguration.TokenizerFile = filepath.Join(
			repositoryRoot,
			modelConfiguration.TokenizerFile,
		)
	}
	return modelConfiguration, nil
}
