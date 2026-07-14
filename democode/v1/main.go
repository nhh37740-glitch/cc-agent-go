package main

import (
	"fmt"
	"net/http"
)

// handleChat 处理 POST /api/chat 请求，返回硬编码 JSON
func handleChat(w http.ResponseWriter, r *http.Request) {
	// 设置响应头，告诉客户端返回的是 JSON
	w.Header().Set("Content-Type", "application/json")
	// 把 JSON 字符串写入响应体
	fmt.Fprint(w, `{"reply": "hello from go"}`)
}

// handleIndex 处理 GET / 请求，返回纯文本
func handleIndex(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, "cc-agent-go is running")
}

func main() {
	// HandleFunc：注册路由处理函数
	// Go 1.22+ 支持 "METHOD /path" 格式的方法匹配
	http.HandleFunc("POST /api/chat", handleChat)
	//http.HandleFunc("GET /", handleIndex)

	fmt.Println("cc-agent-go 启动在 http://localhost:8080")
	// ListenAndServe：阻塞监听端口，第二个参数 nil 表示用默认路由
	// FileServer 自动把目录下的 index.html 作为默认页
    // 浏览器访问 http://localhost:8080 就返回 code_artifact.html
    fs := http.FileServer(http.Dir("democode/v1"))
    // FileServer 必须用老语法（不带 "GET " 前缀），因为 / 需要做前缀匹配
    // → / 匹配，/index.html 也匹配，否则 FileServer 收不到子路径请求
    http.Handle("/", fs)
	http.ListenAndServe(":8080", nil)
}
