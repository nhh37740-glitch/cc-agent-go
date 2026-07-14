package config

import "os"

// Config 存放 API 调用和运行时配置。
type Config struct {
	ApiKey               string
	ApiEndpoint          string
	Model                string
	MemoryPath           string
	SessionsDir          string
	CompressionThreshold int
}

// Load 从环境变量读取配置。API Key 不写入源码，运行前请设置
// DEEPSEEK_API_KEY。
func Load() Config {
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		apiKey = "请设置 DEEPSEEK_API_KEY 环境变量"
	}

	return Config{
		ApiKey:               apiKey,
		ApiEndpoint:          "https://api.deepseek.com/anthropic/v1/messages",
		Model:                "deepseek-v4-pro[1m]",
		MemoryPath:           "workspace/memory/AGENT.MD",
		SessionsDir:          "data/sessions",
		CompressionThreshold: 100000,
	}
}
