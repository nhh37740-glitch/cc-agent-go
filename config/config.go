package config

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
)

const defaultMaximumParallelSubAgents = 5
const hardMaximumParallelSubAgents = 5
const defaultLocalConfigFilePath = "config/local.json"

type localConfigFile struct {
	DeepSeekAPIKey string `json:"deepseekApiKey"`
}

// Config 存放 API 调用和运行时配置
type Config struct {
	ApiKey                   string
	ApiEndpoint              string
	Model                    string
	MemoryPath               string // AGENT.MD 文件路径，默认 "../../memory/AGENT.MD"
	SessionsDir              string // 会话 JSON 存储目录，默认 "workspace/data/sessions"
	CompressionThreshold     int    // token 压缩阈值，默认 100000（和 Java 版一致）
	MaximumParallelSubAgents int    // 同一次 run_subagent 最多并行执行的 SubAgent 数量
}

// Load 读取运行配置。DEEPSEEK_API_KEY 环境变量优先；
// 环境变量为空时读取 config/local.json。
func Load() Config {
	return Config{
		ApiKey:                   loadDeepSeekAPIKey(),
		ApiEndpoint:              "https://api.deepseek.com/anthropic/v1/messages",
		Model:                    "deepseek-v4-pro[1m]",
		MemoryPath:               "workspace/memory/AGENT.MD",
		SessionsDir:              "data/sessions",
		CompressionThreshold:     100000,
		MaximumParallelSubAgents: loadMaximumParallelSubAgents(),
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
