package main

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// ChatRequest 对应前端发来的 JSON：{"message": "..."}
// `json:"message"` 是结构体标签（tag），告诉 json 包：JSON 里的 "message" 字段映射到这个 Message 字段
type ChatRequest struct {
	Message string `json:"message"`
}

// ChatResponse 对应返回给前端的 JSON：{"reply": "..."}
type ChatResponse struct {
	Reply string `json:"reply"`
}

// handleChat 处理 POST /api/chat 请求；解析 JSON 请求体，返回结构化 JSON
func handleChat(w http.ResponseWriter, r *http.Request) {
	var req ChatRequest
	// json.NewDecoder 从请求体（r.Body）逐字节读取 JSON，Decode 把结果填到 req 里
	// &req 是取地址 —— Decode 需要知道"把数据写到哪块内存"
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// 如果请求体不是合法 JSON，返回 400
		http.Error(w, "请求体必须是合法 JSON", http.StatusBadRequest)
		return
	}

	resp := ChatResponse{Reply: "你说的是：" + req.Message}

	w.Header().Set("Content-Type", "application/json")
	// json.NewEncoder 把 resp 序列化为 JSON 写到响应体
	json.NewEncoder(w).Encode(resp)
}

// handleChatStream 处理 GET /api/chat/stream；v2 先返回普通 JSON，流式留到 v4
func handleChatStream(w http.ResponseWriter, r *http.Request) {
	message := r.URL.Query().Get("message")

	resp := ChatResponse{Reply: "你说的是：" + message}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func main() {
	http.HandleFunc("POST /api/chat", handleChat)
	http.HandleFunc("GET /api/chat/stream", handleChatStream)

	fmt.Println("cc-agent-go v2 启动在 http://localhost:8080")
	fmt.Println("  POST /api/chat         — JSON 请求体 → 结构化响应")
	fmt.Println("  GET  /api/chat/stream  — URL 参数 → 结构化响应")
	http.ListenAndServe(":8080", nil)
}
