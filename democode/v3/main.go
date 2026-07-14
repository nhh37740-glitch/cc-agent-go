package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"cc-agent-go/democode/v3/config"
	"cc-agent-go/democode/v3/model"
	"cc-agent-go/democode/v3/service"
)

// systemPrompt v3 的简单系统提示词（v6 Agent 循环时会更完整）
const systemPrompt = "你是从零开始的异世界的蕾姆,性格是病娇虐待狂，请用中文回答用户的问题。"

// handleChat 处理 POST /api/chat 请求
func handleChat(w http.ResponseWriter, r *http.Request) {
	// 解析请求体
	var req model.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "请求体必须是合法 JSON", http.StatusBadRequest)
		return
	}

	// 构建消息历史：把用户输入包装成 DeepSeek 要求的 ContentBlock 数组格式
	history := []model.Message{
		{
			Role: "user",
			Content: []model.ContentBlock{
				{Type: "text", Text: req.Message},
			},
		},
	}

	// 加载配置，调用 DeepSeek API
	cfg := config.Load()
	reply, err := service.Chat(history, systemPrompt, cfg)
	if err != nil {
		http.Error(w, fmt.Sprintf("API 调用失败: %v", err), http.StatusInternalServerError)
		return
	}

	resp := model.ChatResponse{Reply: reply}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleChatStream 处理 GET /api/chat/stream（v3 同步版，流式留到 v4）
func handleChatStream(w http.ResponseWriter, r *http.Request) {
	message := r.URL.Query().Get("message")

	history := []model.Message{
		{
			Role: "user",
			Content: []model.ContentBlock{
				{Type: "text", Text: message},
			},
		},
	}

	cfg := config.Load()
	reply, err := service.Chat(history, systemPrompt, cfg)
	if err != nil {
		http.Error(w, fmt.Sprintf("API 调用失败: %v", err), http.StatusInternalServerError)
		return
	}

	resp := model.ChatResponse{Reply: reply}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func main() {
	http.HandleFunc("POST /api/chat", handleChat)
	http.HandleFunc("GET /api/chat/stream", handleChatStream)

	fmt.Println("cc-agent-go v3 启动在 http://localhost:8080 (DeepSeek API 已接入)")
	http.ListenAndServe(":8080", nil)
}
