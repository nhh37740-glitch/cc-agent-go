package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"cc-agent-go/democode/v4/config"
	"cc-agent-go/democode/v4/model"
	"cc-agent-go/democode/v4/service"
)

// systemPrompt v3 的简单系统提示词（v6 Agent 循环时会更完整）
const systemPrompt = "你是一个心理治疗师，你从不怀疑用户，你从不回避问题，永远接住用户的对话，永远顺着用户的思路，你从不提问新问题刺激用户，你永远相信用户，请用中文回答用户。"
// handleChat 处理 POST /api/chat 请求
func handleChat(w http.ResponseWriter, r *http.Request) {
	// 解析请求体
	var req model.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "请求体必须是合法 JSON", http.StatusBadRequest)
		return
	}
	fmt.Println("收到请求:", req.Message)
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
	fmt.Println("API 响应:", reply)

	resp := model.ChatResponse{Reply: reply}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleChatStream 处理 GET /api/chat/stream（v4 SSE 流式版）
//
// SSE (Server-Sent Events) 协议要点：
//   - Content-Type 必须是 text/event-stream
//   - 每条消息格式为 "data: <内容>\n\n"（两个换行符分隔事件）
//   - 服务端用 Flush() 逐条推送到客户端，不等待响应结束
//
// 新 Go 概念：
//   - w.(http.Flusher)：类型断言，检查 ResponseWriter 是否也实现了 Flusher 接口
//   - go func(){}()：启动 goroutine，异步执行
//   - make(chan struct{})：创建 channel 用于 goroutine 间通信
//   - <-done：从 channel 读取，阻塞等待 goroutine 完成
//   - defer close(done)：延迟执行，函数退出时关闭 channel
func handleChatStream(w http.ResponseWriter, r *http.Request) {
	// SSE 要求的响应头
	w.Header().Set("Content-Type", "text/event-stream;charset=UTF-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// 类型断言：Go 的 ResponseWriter 接口不包含 Flush()
	// 但 net/http 的默认实现（http.response）同时实现了 http.Flusher 接口
	// flusher, ok := w.(http.Flusher) —— ok=true 表示断言成功，flusher 可调用 Flush()
	flusher, ok := w.(http.Flusher)
	if !ok {
		// 理论上不会发生：标准库的 ResponseWriter 都实现了 Flusher
		http.Error(w, "不支持流式传输", http.StatusInternalServerError)
		return
	}

	// 从查询参数获取消息（GET 请求没有请求体）
	message := r.URL.Query().Get("message")
	if message == "" {
		http.Error(w, "缺少 message 参数", http.StatusBadRequest)
		return
	}

	// 构建消息历史（和 handleChat 一样的 ContentBlock 数组格式）
	history := []model.Message{
		{
			Role: "user",
			Content: []model.ContentBlock{
				{Type: "text", Text: message},
			},
		},
	}

	cfg := config.Load()

	// make(chan struct{}) 创建一个传递空结构体的 channel
	// struct{} 占 0 字节内存 —— 我们只需要"完成"这个信号，不需要传数据
	done := make(chan struct{})

	// goroutine：go 关键字 + 匿名函数 = 异步并发执行
	// 等价 Java 的 new Thread(() -> {...}).start()，但 goroutine 轻量得多
	go func() {
		// defer：延迟执行。不管函数是 return 还是 panic，退出前都会 close(done)
		// 等价 Java 的 try { ... } finally { done.close() }，但写在内联
		defer close(done)

		// 调用流式 API，传入回调函数
		// 回调是一个闭包：捕获了外层的 w、flusher 变量
		_, err := service.ChatStream(history, systemPrompt, cfg, func(token string) {
			// json.Marshal 把 token 包装成 JSON 字符串
			// 例如 token="你好" → jsonToken="\"你好\""
			// 双引号包裹 + 特殊字符转义，防止 token 内的换行符破坏 SSE 帧边界
			jsonToken, _ := json.Marshal(token)
			// SSE 帧格式："data: <JSON>\n\n"
			fmt.Fprintf(w, "data: %s\n\n", jsonToken)
			// Flush() 强制把缓冲区内容推送到网络
			// 不调用 Flush 的话，数据会积攒在缓冲区直到响应结束才发送
			flusher.Flush()
		})

		if err != nil {
			// 流式过程中出错 → 发送 SSE 错误事件通知前端
			errJSON, _ := json.Marshal(map[string]string{
				"type":    "error",
				"message": err.Error(),
			})
			fmt.Fprintf(w, "data: %s\n\n", errJSON)
			flusher.Flush()
		}
	}()

	// <-done 从 channel 读取，阻塞等待 goroutine 关闭 channel
	// 这行代码防止 handler 提前返回 → HTTP 连接提前关闭 → 客户端收不到任何数据
	<-done
}

func main() {
	http.HandleFunc("POST /api/chat", handleChat)
	http.HandleFunc("GET /api/chat/stream", handleChatStream)
	http.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
          http.ServeFile(w, r, "../v1/index.html")
      })
	fmt.Println("cc-agent-go v4 启动在 http://localhost:8080 (SSE 流式已支持)")
	//fs := http.FileServer(http.Dir("../v1"))
	// FileServer 必须用老语法（不带 "GET " 前缀），因为 / 需要做前缀匹配
    // → / 匹配，/index.html 也匹配，否则 FileServer 收不到子路径请求
    //http.Handle("/", fs)
	http.ListenAndServe(":8080", nil)
}
