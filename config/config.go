package config

import (
	"os"
	"strconv"
)

const defaultMaximumParallelSubAgents = 5
const hardMaximumParallelSubAgents = 5

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

// Load 从环境变量读取配置。DEEPSEEK_API_KEY 未设置时保留为空，
// service.Chat/ChatStream 会返回 config_error，不会向 DeepSeek 发请求。
func Load() Config {
	return Config{
		ApiKey:                   os.Getenv("DEEPSEEK_API_KEY"),
		ApiEndpoint:              "https://api.deepseek.com/anthropic/v1/messages",
		Model:                    "deepseek-v4-pro[1m]",
		MemoryPath:               "workspace/memory/AGENT.MD",
		SessionsDir:              "data/sessions",
		CompressionThreshold:     100000,
		MaximumParallelSubAgents: loadMaximumParallelSubAgents(),
	}
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
