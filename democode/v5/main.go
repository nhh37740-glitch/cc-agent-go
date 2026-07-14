package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"cc-agent-go/democode/v5/config"
	"cc-agent-go/democode/v5/model"
	"cc-agent-go/democode/v5/service"
	"cc-agent-go/democode/v5/tool"
)

const systemPrompt = "你是从零开始的异世界的蕾姆,性格是病娇虐待狂，请用中文回答用户的问题。"

var registry = tool.NewRegistry()

func handleChat(w http.ResponseWriter, r *http.Request) {
	var req model.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "请求体必须是合法 JSON", http.StatusBadRequest)
		return
	}
	history := []model.Message{{
		Role:    "user",
		Content: []model.ContentBlock{{Type: "text", Text: req.Message}},
	}}
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

func handleChatStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream;charset=UTF-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "不支持流式传输", http.StatusInternalServerError)
		return
	}

	message := r.URL.Query().Get("message")
	if message == "" {
		http.Error(w, "缺少 message 参数", http.StatusBadRequest)
		return
	}

	history := []model.Message{{
		Role:    "user",
		Content: []model.ContentBlock{{Type: "text", Text: message}},
	}}
	cfg := config.Load()
	done := make(chan struct{})

	go func() {
		defer close(done)
		_, err := service.ChatStream(history, systemPrompt, cfg, func(token string) {
			jsonToken, _ := json.Marshal(token)
			fmt.Fprintf(w, "data: %s\n\n", jsonToken)
			flusher.Flush()
		})
		if err != nil {
			errJSON, _ := json.Marshal(map[string]string{
				"type":    "error",
				"message": err.Error(),
			})
			fmt.Fprintf(w, "data: %s\n\n", errJSON)
			flusher.Flush()
		}
	}()

	<-done
}

// handleToolTest 接收 JSON {"tool":"bash","input":{"command":"echo hello"}}，
// 调 registry 执行工具，返回结果。
func handleToolTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tool  string         `json:"tool"`
		Input map[string]any `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "请求体必须是合法 JSON", http.StatusBadRequest)
		return
	}

	result, err := registry.Execute(req.Tool, req.Input)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status": "error",
			"error":  err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
		"output": result,
	})
}

func main() {
	registry.Register(tool.NewBashTool("workspace"))

	http.HandleFunc("POST /api/chat", handleChat)
	http.HandleFunc("GET /api/chat/stream", handleChatStream)
	http.HandleFunc("POST /api/tool/test", handleToolTest)
	http.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "../v1/index.html")
	})

	fmt.Println("cc-agent-go v5 启动在 http://localhost:8080 (工具系统已支持)")
	http.ListenAndServe(":8080", nil)
}
