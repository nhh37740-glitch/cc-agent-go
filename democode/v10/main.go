package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"cc-agent-go/democode/v10/config"
	"cc-agent-go/democode/v10/model"
	"cc-agent-go/democode/v10/service"
	"cc-agent-go/democode/v10/tool"
)

// ============================================================================
// 人格加载
// ============================================================================

// loadPersonalities 扫描 personalities/ 目录下所有 .md 文件，
// 返回 map[agent名]文件内容。agent 名 = 文件名去掉 .md 后缀。
func loadPersonalities(dir string) (map[string]string, error) {
	result := make(map[string]string)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("读取人格目录失败: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			fmt.Printf("读取人格文件 %s 失败: %v\n", name, err)
			continue
		}
		agentName := strings.TrimSuffix(name, ".md")
		result[agentName] = strings.TrimSpace(string(data))
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("personalities/ 目录下没有 .md 文件")
	}
	return result, nil
}

var personalities map[string]string

// ============================================================================
// System Prompt
// ============================================================================
// System Prompt + Memory
// ============================================================================

const memoryRule = `## memory/AGENT.MD 长期记忆规则

memory/AGENT.MD 是你的长期记忆文件。如果用户在对话中表达了会反复使用的偏好、项目规则、任务状态或重要决策，你必须主动用 bash 工具创建或更新该文件。
更新方式：bash 执行 echo "内容" >> memory/AGENT.MD
（bash 工具的工作目录已经是 workspace/，所以直接写 memory/AGENT.MD 即可）`

const systemPromptBase = `你是一个 AI 助手。你可以使用 bash 工具执行 shell 命令来操作文件。
当用户要求创建文件、读文件、执行命令时，你必须调用 bash 工具，不要只用文字说明。
你需要结合对话历史中的上下文来理解用户的追问和省略表达。
如果 activate_skill 工具列出的技能和用户需求匹配，先调用 activate_skill 激活技能再回答。`

// loadMemory 读取 memory/AGENT.MD（相对于工作目录 workspace/）。
func loadMemory() string {
	data, err := os.ReadFile("workspace/memory/AGENT.MD")
	if err != nil {
		return ""
	}
	return string(data)
}

// buildSystemPrompt 拼装完整提示词：基础提示词 + 记忆规则 + 当前记忆内容。
func buildSystemPrompt() string {
	var sb strings.Builder
	sb.WriteString(systemPromptBase)
	sb.WriteString("\n\n")
	sb.WriteString(memoryRule)
	mem := loadMemory()
	if mem != "" {
		sb.WriteString("\n\n当前 workspace/memory/AGENT.MD 内容：\n")
		sb.WriteString(mem)
	}
	return sb.String()
}

var registry = tool.NewRegistry()

// ============================================================================
// Chat 路由
// ============================================================================

func handleChat(w http.ResponseWriter, r *http.Request) {
	var req model.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "请求体必须是合法 JSON", http.StatusBadRequest)
		return
	}
	cfg := config.Load()
	store := service.NewStore(cfg.SessionsDir)

	reply, convId, err := service.Run(req.Message, req.ConversationId,
		buildSystemPrompt(), cfg, registry, store)
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
	convIdJSON, _ := json.Marshal(map[string]string{
		"type":           "conversation_id",
		"conversationId": conversationId,
	})
	fmt.Fprintf(w, "data: %s\n\n", convIdJSON)
	flusher.Flush()

	cfg := config.Load()
	store := service.NewStore(cfg.SessionsDir)

	done := make(chan struct{})
	go func() {
		defer close(done)
		reply, _, err := service.RunStream(req.Message, conversationId,
			buildSystemPrompt(), cfg, registry, store,
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
			errJSON, _ := json.Marshal(map[string]string{"type": "error", "message": err.Error()})
			fmt.Fprintf(w, "data: %s\n\n", errJSON)
			flusher.Flush()
		}
		if reply != "" {
			doneJSON, _ := json.Marshal(map[string]string{"type": "done", "text": reply})
			fmt.Fprintf(w, "data: %s\n\n", doneJSON)
			flusher.Flush()
		}
	}()
	<-done
}

// ============================================================================
// 会话管理路由
// ============================================================================

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

// ============================================================================
// 元老院路由
// ============================================================================

func handleCouncil(w http.ResponseWriter, r *http.Request) {
	var req service.CouncilRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "请求体必须是合法 JSON", http.StatusBadRequest)
		return
	}
	if req.Topic == "" {
		http.Error(w, "topic 不能为空", http.StatusBadRequest)
		return
	}
	if req.MaxRounds < 1 {
		req.MaxRounds = 3
	}
	cfg := config.Load()
	transcript, totalTokens, err := service.RunCouncil(
		req.Topic, req.MaxRounds, req.Interruption, personalities, cfg)
	if err != nil {
		http.Error(w, fmt.Sprintf("辩论失败: %v", err), http.StatusInternalServerError)
		return
	}
	resp := service.CouncilResponse{
		Topic:       req.Topic,
		Rounds:      req.MaxRounds,
		Transcript:  transcript,
		TotalTokens: totalTokens,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func handleCouncilStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream;charset=UTF-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "不支持流式传输", http.StatusInternalServerError)
		return
	}
	var req service.CouncilRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fmt.Fprintf(w, "data: {\"type\":\"error\",\"message\":\"请求体必须是合法 JSON\"}\n\n")
		flusher.Flush()
		return
	}
	if req.Topic == "" {
		fmt.Fprintf(w, "data: {\"type\":\"error\",\"message\":\"topic 不能为空\"}\n\n")
		flusher.Flush()
		return
	}
	if req.MaxRounds < 1 {
		req.MaxRounds = 3
	}

	cfg := config.Load()
	topicFrame, _ := json.Marshal(map[string]string{"type": "topic", "text": req.Topic})
	fmt.Fprintf(w, "data: %s\n\n", topicFrame)
	flusher.Flush()

	done := make(chan struct{})
	go func() {
		defer close(done)
		names := make([]string, 0, len(personalities))
		for name := range personalities {
			names = append(names, name)
		}
		sort.Strings(names)

		history := []model.Message{
			{Role: "user", Content: []model.ContentBlock{
				{Type: "text", Text: "【元老院议题】" + req.Topic},
			}},
		}
		if req.Interruption != "" {
			history = append(history, model.Message{
				Role: "user",
				Content: []model.ContentBlock{
					{Type: "text", Text: "【公民插话】" + req.Interruption},
				},
			})
		}

		for round := 1; round <= req.MaxRounds; round++ {
			roundFrame, _ := json.Marshal(map[string]any{"type": "round", "round": round})
			fmt.Fprintf(w, "data: %s\n\n", roundFrame)
			flusher.Flush()

			for _, name := range names {
				messages := make([]model.Message, len(history))
				copy(messages, history)
				resp, err := service.Chat(messages, personalities[name], cfg, nil, 4096)
				if err != nil {
					errFrame, _ := json.Marshal(map[string]string{
						"type": "error", "message": fmt.Sprintf("%s 发言失败: %v", name, err),
					})
					fmt.Fprintf(w, "data: %s\n\n", errFrame)
					flusher.Flush()
					return
				}
				history = append(history, model.Message{
					Role:    "assistant",
					Content: []model.ContentBlock{{Type: "text", Text: "【" + name + "】" + resp.Text}},
				})
				speechFrame, _ := json.Marshal(map[string]any{
					"type": "speech", "round": round, "agent": name, "text": resp.Text,
				})
				fmt.Fprintf(w, "data: %s\n\n", speechFrame)
				flusher.Flush()
			}
		}
		doneFrame, _ := json.Marshal(map[string]string{"type": "done"})
		fmt.Fprintf(w, "data: %s\n\n", doneFrame)
		flusher.Flush()
	}()
	<-done
}

// ============================================================================
// main
// ============================================================================

func main() {
	var err error
	personalities, err = loadPersonalities("personalities")
	if err != nil {
		fmt.Printf("加载人格失败: %v\n", err)
		fmt.Println("请在 personalities/ 下放置 .md 人格文件")
		os.Exit(1)
	}
	names := make([]string, 0, len(personalities))
	for name := range personalities {
		names = append(names, name)
	}
	sort.Strings(names)
	fmt.Printf("已加载 %d 位元老: %s\n", len(names), strings.Join(names, ", "))

	registry.Register(tool.NewBashTool("workspace"))
	registry.Register(tool.NewSkillTool("workspace/skills"))
		registry.Register(tool.NewCreateSkillTool("workspace/skills"))

	http.HandleFunc("POST /api/chat", handleChat)
	http.HandleFunc("POST /api/chat/stream", handleChatStream)
	http.HandleFunc("GET /api/conversations", handleListConversations)
	http.HandleFunc("GET /api/conversations/{id}", handleGetConversation)
	http.HandleFunc("DELETE /api/conversations/{id}", handleDeleteConversation)
	http.HandleFunc("POST /api/council", handleCouncil)
	http.HandleFunc("POST /api/council/stream", handleCouncilStream)
	http.HandleFunc("GET /council", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "index.html")
	})
	http.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "../v1/index.html")
	})

	fmt.Println("cc-agent-go v10 🔧 Skill 系统 启动在 http://localhost:8080")
	fmt.Printf("  辩论室: http://localhost:8080/council\n")
	fmt.Printf("  教学沙箱: http://localhost:8080/\n")
	http.ListenAndServe(":8080", nil)
}
