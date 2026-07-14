package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"cc-agent-go/democode/v7/config"
	"cc-agent-go/democode/v7/model"
	"cc-agent-go/democode/v7/service"
	"cc-agent-go/democode/v7/tool"
)

// 基础系统提示词（v6.5 内容）
const systemPromptBase = "你是一个 AI 助手。你可以使用 bash 工具执行 shell 命令来操作文件。当用户要求创建文件、读文件、执行命令时，你必须调用 bash 工具，不要只用文字说明。"

// AGENT.MD 记忆规则（和 Java agent.xml 中的 memory 规则一致）
const memoryRule = `AGENT.MD 长期记忆规则：
memory/AGENT.MD 是项目中必须要存在的长期记忆文件。每轮任务中，如果出现未来会反复有用的用户偏好、项目规则、当前任务状态或重要实现决策，你应该主动用 bash 工具创建或更新 memory/AGENT.MD。
更新方式：使用 bash 工具执行 echo "内容" >> ../../memory/AGENT.MD 追加内容。`

// buildSystemPrompt 拼装完整系统提示词：基础提示词 + 记忆规则 + 当前记忆内容。
func buildSystemPrompt(memoryContent string) string {
	var sb strings.Builder
	sb.WriteString(systemPromptBase)
	sb.WriteString("\n\n")
	sb.WriteString(memoryRule)
	if memoryContent != "" {
		sb.WriteString("\n\n当前 memory/AGENT.MD 内容：\n")
		sb.WriteString(memoryContent)
	}
	return sb.String()
}

var registry = tool.NewRegistry()

func handleChat(w http.ResponseWriter, r *http.Request) {
	var req model.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "请求体必须是合法 JSON", http.StatusBadRequest)
		return
	}

	cfg := config.Load()
	store := service.NewStore(cfg.SessionsDir)
	memoryContent, _ := service.LoadMemory(cfg.MemoryPath)
	systemPrompt := buildSystemPrompt(memoryContent)

	reply, convId, err := service.Run(req.Message, req.ConversationId,
		systemPrompt, cfg, registry, store)
	if err != nil {
		http.Error(w, fmt.Sprintf("Agent 调用失败: %v", err), http.StatusInternalServerError)
		return
	}

	resp := model.ChatResponse{ConversationId: convId, Reply: reply}
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

	var req model.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fmt.Fprintf(w, "data: {\"type\":\"error\",\"message\":\"请求体必须是合法 JSON\"}\n\n")
		flusher.Flush()
		return
	}

	// 预先生成 conversationId，作为首帧 SSE 发送
	conversationId := req.ConversationId
	if conversationId == "" {
		conversationId = service.GenerateConversationId()
	}

	// 首帧：conversation_id
	convIdJSON, _ := json.Marshal(map[string]string{
		"type":           "conversation_id",
		"conversationId": conversationId,
	})
	fmt.Fprintf(w, "data: %s\n\n", convIdJSON)
	flusher.Flush()

	cfg := config.Load()
	store := service.NewStore(cfg.SessionsDir)
	memoryContent, _ := service.LoadMemory(cfg.MemoryPath)
	systemPrompt := buildSystemPrompt(memoryContent)

	done := make(chan struct{})

	go func() {
		defer close(done)
		reply, _, err := service.RunStream(req.Message, conversationId,
			systemPrompt, cfg, registry, store,
			func(token string) {
				if len(token) > 0 && token[0] == '{' {
					fmt.Fprintf(w, "data: %s\n\n", token)
				} else {
					jsonToken, _ := json.Marshal(token)
					fmt.Fprintf(w, "data: %s\n\n", jsonToken)
				}
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
		if reply != "" {
			doneJSON, _ := json.Marshal(map[string]string{
				"type": "done",
				"text": reply,
			})
			fmt.Fprintf(w, "data: %s\n\n", doneJSON)
			flusher.Flush()
		}
	}()

	<-done
}

// handleListConversations 列出所有会话（GET /api/conversations）
func handleListConversations(w http.ResponseWriter, r *http.Request) {
	cfg := config.Load()
	store := service.NewStore(cfg.SessionsDir)
	summaries, err := store.ListConversations()
	if err != nil {
		http.Error(w, fmt.Sprintf("读取会话列表失败: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(summaries)
}

// handleGetConversation 加载指定会话（GET /api/conversations/{id}）
// r.PathValue("id") 是 Go 1.22+ 内置路径参数提取，{id} 是通配符。
func handleGetConversation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cfg := config.Load()
	store := service.NewStore(cfg.SessionsDir)
	session, err := store.LoadConversation(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if session == nil {
		http.Error(w, "会话不存在", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(session)
}

// handleDeleteConversation 删除指定会话（DELETE /api/conversations/{id}）
func handleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cfg := config.Load()
	store := service.NewStore(cfg.SessionsDir)
	if err := store.DeleteConversation(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func main() {
	registry.Register(tool.NewBashTool("workspace"))

	http.HandleFunc("POST /api/chat", handleChat)
	http.HandleFunc("POST /api/chat/stream", handleChatStream)
	http.HandleFunc("GET /api/conversations", handleListConversations)
	http.HandleFunc("GET /api/conversations/{id}", handleGetConversation)
	http.HandleFunc("DELETE /api/conversations/{id}", handleDeleteConversation)
	http.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "../v1/index.html")
	})

	fmt.Println("cc-agent-go v7 启动在 http://localhost:8080 (会话持久化 + Memory 加载)")
	http.ListenAndServe(":8080", nil)
}
