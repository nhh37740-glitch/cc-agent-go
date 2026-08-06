package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const defaultMaximumParallelSubAgents = 5
const hardMaximumParallelSubAgents = 5
const defaultMaximumSubAgentRounds = 50
const defaultMaximumHarnessAgents = 11
const defaultLocalConfigFilePath = "config/local.json"

type localConfigFile struct {
	DeepSeekAPIKey string `json:"deepseekApiKey"`
}

// Config 存放 API 调用和运行时配置
type Config struct {
	ApiKey                   string
	ApiEndpoint              string
	Model                    string
	CompressionThreshold     int // token 压缩阈值，默认 100000（和 Java 版一致）
	MaximumParallelSubAgents int // 同一次 run_subagent 最多并行执行的 SubAgent 数量
	MaximumSubAgentRounds    int // run_subagent 单项任务允许填写的最高轮数
	MaximumHarnessAgents     int // Harness 被管理 Agent 池容量上限
}

// Load 读取运行配置。DEEPSEEK_API_KEY 环境变量优先；
// 环境变量为空时读取 config/local.json。
func Load() Config {
	return Config{
		ApiKey:                   loadDeepSeekAPIKey(),
		ApiEndpoint:              "https://api.deepseek.com/anthropic/v1/messages",
		Model:                    "deepseek-v4-pro[1m]",
		CompressionThreshold:     100000,
		MaximumParallelSubAgents: loadMaximumParallelSubAgents(),
		MaximumSubAgentRounds:    loadMaximumSubAgentRounds(),
		MaximumHarnessAgents:     loadMaximumHarnessAgents(),
	}
}

func loadDeepSeekAPIKey() string {
	deepSeekAPIKeyFromEnvironment :=
		strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	if deepSeekAPIKeyFromEnvironment != "" {
		return deepSeekAPIKeyFromEnvironment
	}

	localConfigFilePath :=
		strings.TrimSpace(os.Getenv("CC_AGENT_LOCAL_CONFIG"))
	if localConfigFilePath == "" {
		localConfigFilePath = defaultLocalConfigFilePath
	}

	localConfigJSON, readLocalConfigError :=
		os.ReadFile(localConfigFilePath)
	if os.IsNotExist(readLocalConfigError) &&
		strings.TrimSpace(os.Getenv("CC_AGENT_LOCAL_CONFIG")) == "" {
		_, currentSourceFile, _, callerInformationAvailable := runtime.Caller(0)
		if callerInformationAvailable {
			localConfigFilePath = filepath.Join(
				filepath.Dir(currentSourceFile),
				"local.json",
			)
			localConfigJSON, readLocalConfigError =
				os.ReadFile(localConfigFilePath)
		}
	}
	if readLocalConfigError != nil {
		return ""
	}

	var decodedLocalConfig localConfigFile
	decodeLocalConfigError :=
		json.Unmarshal(localConfigJSON, &decodedLocalConfig)
	if decodeLocalConfigError != nil {
		return ""
	}

	return strings.TrimSpace(decodedLocalConfig.DeepSeekAPIKey)
}

func loadMaximumParallelSubAgents() int {
	configuredMaximumParallelSubAgents := os.Getenv("MAX_PARALLEL_SUBAGENTS")
	if configuredMaximumParallelSubAgents == "" {
		return defaultMaximumParallelSubAgents
	}

	maximumParallelSubAgents, convertConfigurationError :=
		strconv.Atoi(configuredMaximumParallelSubAgents)
	if convertConfigurationError != nil ||
		maximumParallelSubAgents < 1 ||
		maximumParallelSubAgents > hardMaximumParallelSubAgents {
		return defaultMaximumParallelSubAgents
	}

	return maximumParallelSubAgents
}

func loadMaximumSubAgentRounds() int {
	configuredMaximumSubAgentRounds :=
		os.Getenv("MAXIMUM_SUBAGENT_ROUNDS")
	if configuredMaximumSubAgentRounds == "" {
		return defaultMaximumSubAgentRounds
	}

	maximumSubAgentRounds, convertConfigurationError :=
		strconv.Atoi(configuredMaximumSubAgentRounds)
	if convertConfigurationError != nil || maximumSubAgentRounds < 1 {
		return defaultMaximumSubAgentRounds
	}

	return maximumSubAgentRounds
}

func loadMaximumHarnessAgents() int {
	configuredMaximumHarnessAgents :=
		os.Getenv("MAX_HARNESS_AGENTS")
	if configuredMaximumHarnessAgents == "" {
		return defaultMaximumHarnessAgents
	}

	maximumHarnessAgents, convertConfigurationError :=
		strconv.Atoi(configuredMaximumHarnessAgents)
	if convertConfigurationError != nil || maximumHarnessAgents < 1 {
		return defaultMaximumHarnessAgents
	}

	return maximumHarnessAgents
}
