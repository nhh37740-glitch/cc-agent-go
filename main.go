package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"cc-agent-go/config"
	"cc-agent-go/model"
	"cc-agent-go/service"
	"cc-agent-go/tool"
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
			slog.Warn("读取人格文件失败",
				"component", "startup",
				"operation", "loadPersonalities",
				"file_name", name,
				"error", err)
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

func publicError(err error) (int, model.ErrorResponse) {
	status := http.StatusInternalServerError
	resp := model.ErrorResponse{
		Code:    string(service.ErrorInternal),
		Message: "服务内部错误。",
	}

	var appErr *service.AppError
	if !errors.As(err, &appErr) {
		return status, resp
	}

	resp.Code = string(appErr.Kind)
	resp.ProviderStatus = appErr.ProviderStatus
	switch appErr.Kind {
	case service.ErrorInvalidRequest:
		status = http.StatusBadRequest
		resp.Message = "请求参数无效。"
	case service.ErrorConfig:
		status = http.StatusServiceUnavailable
		resp.Message = "服务端未配置 DEEPSEEK_API_KEY。"
	case service.ErrorProviderAuth:
		status = http.StatusBadGateway
		resp.Message = "DeepSeek API 鉴权失败，请检查服务端 DEEPSEEK_API_KEY。"
	case service.ErrorProviderRateLimit:
		status = http.StatusServiceUnavailable
		resp.Message = "DeepSeek 请求过于频繁，请稍后重试。"
	case service.ErrorProvider, service.ErrorProviderResponseInvalid:
		status = http.StatusBadGateway
		resp.Message = "DeepSeek 服务返回异常。"
	case service.ErrorNetwork:
		status = http.StatusBadGateway
		resp.Message = "无法连接 DeepSeek 服务。"
	case service.ErrorNetworkTimeout:
		status = http.StatusGatewayTimeout
		resp.Message = "连接 DeepSeek 服务超时。"
	case service.ErrorStorageRead:
		status = http.StatusInternalServerError
		resp.Message = "读取会话数据失败。"
	case service.ErrorStorageWrite:
		status = http.StatusInternalServerError
		resp.Message = "写入会话数据失败。"
	case service.ErrorAgentLimit:
		status = http.StatusInternalServerError
		resp.Message = "Agent 达到最大工具调用轮数。"
	}
	return status, resp
}

func logAPIError(operation string, conversationID string, err error, resp model.ErrorResponse) {
	args := []any{
		"component", "http",
		"operation", operation,
		"error_kind", resp.Code,
		"error", err,
	}
	if conversationID != "" {
		args = append(args, "conversation_id", conversationID)
	}
	if resp.ProviderStatus != 0 {
		args = append(args, "provider_status", resp.ProviderStatus)
	}
	slog.Error("请求处理失败", args...)
}

func writeAPIError(w http.ResponseWriter, operation string, conversationID string, err error) {
	status, resp := publicError(err)
	logAPIError(operation, conversationID, err, resp)
	w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	w.WriteHeader(status)
	if encodeErr := json.NewEncoder(w).Encode(resp); encodeErr != nil {
		slog.Error("错误响应 JSON 写入失败",
			"component", "http",
			"operation", operation,
			"error_kind", service.ErrorInternal,
			"error", encodeErr)
	}
}

func writeSSEError(w http.ResponseWriter, flusher http.Flusher, operation string,
	conversationID string, err error) {
	_, resp := publicError(err)
	logAPIError(operation, conversationID, err, resp)
	frame := map[string]any{
		"type":    "error",
		"code":    resp.Code,
		"message": resp.Message,
	}
	if resp.ProviderStatus != 0 {
		frame["providerStatus"] = resp.ProviderStatus
	}
	data, marshalErr := json.Marshal(frame)
	if marshalErr != nil {
		slog.Error("SSE 错误事件序列化失败",
			"component", "http",
			"operation", operation,
			"error_kind", service.ErrorInternal,
			"error", marshalErr)
		return
	}
	if _, writeErr := fmt.Fprintf(w, "data: %s\n\n", data); writeErr != nil {
		slog.Error("SSE 错误事件写入失败",
			"component", "http",
			"operation", operation,
			"error_kind", service.ErrorInternal,
			"error", writeErr)
		return
	}
	flusher.Flush()
}

func invalidRequestError(operation string, err error) *service.AppError {
	return service.NewAppError(service.ErrorInvalidRequest, operation, 0, err)
}

// ============================================================================
// Chat 路由
// ============================================================================

func handleChat(w http.ResponseWriter, r *http.Request) {
	var req model.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, "handleChat.decode", "",
			invalidRequestError("handleChat.decode", err))
		return
	}
	cfg := config.Load()
	store := service.NewStore(cfg.SessionsDir)

	reply, convId, err := service.Run(req.Message, req.ConversationId,
		buildSystemPrompt(), cfg, registry, store)
	if err != nil {
		writeAPIError(w, "handleChat.run", convId, err)
		return
	}
	resp := model.ChatResponse{ConversationId: convId, Reply: reply}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("聊天响应 JSON 写入失败",
			"component", "http",
			"operation", "handleChat.encode",
			"conversation_id", convId,
			"error_kind", service.ErrorInternal,
			"error", err)
	}
}

func handleChatStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream;charset=UTF-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, "handleChatStream.flusher", "",
			service.NewAppError(service.ErrorInternal, "handleChatStream.flusher", 0,
				fmt.Errorf("ResponseWriter 不支持 http.Flusher")))
		return
	}
	var req model.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeSSEError(w, flusher, "handleChatStream.decode", "",
			invalidRequestError("handleChatStream.decode", err))
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
			writeSSEError(w, flusher, "handleChatStream.run", conversationId, err)
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
		writeAPIError(w, "handleListConversations.list", "",
			service.NewAppError(service.ErrorStorageRead,
				"store.ListConversations", 0, err))
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
		writeAPIError(w, "handleGetConversation.load", id,
			service.NewAppError(service.ErrorStorageRead,
				"store.LoadConversation", 0, err))
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
		writeAPIError(w, "handleDeleteConversation.delete", id,
			service.NewAppError(service.ErrorStorageWrite,
				"store.DeleteConversation", 0, err))
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
		writeAPIError(w, "handleCouncil.decode", "",
			invalidRequestError("handleCouncil.decode", err))
		return
	}
	if req.Topic == "" {
		writeAPIError(w, "handleCouncil.validate", "",
			invalidRequestError("handleCouncil.validate", fmt.Errorf("topic 不能为空")))
		return
	}
	if req.MaxRounds < 1 {
		req.MaxRounds = 3
	}
	cfg := config.Load()
	transcript, totalTokens, err := service.RunCouncil(
		req.Topic, req.MaxRounds, req.Interruption, personalities, cfg)
	if err != nil {
		writeAPIError(w, "handleCouncil.run", "", err)
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
		writeAPIError(w, "handleCouncilStream.flusher", "",
			service.NewAppError(service.ErrorInternal, "handleCouncilStream.flusher", 0,
				fmt.Errorf("ResponseWriter 不支持 http.Flusher")))
		return
	}
	var req service.CouncilRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeSSEError(w, flusher, "handleCouncilStream.decode", "",
			invalidRequestError("handleCouncilStream.decode", err))
		return
	}
	if req.Topic == "" {
		writeSSEError(w, flusher, "handleCouncilStream.validate", "",
			invalidRequestError("handleCouncilStream.validate", fmt.Errorf("topic 不能为空")))
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
					writeSSEError(w, flusher, "handleCouncilStream.chat", "", err)
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
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))

	var err error
	personalities, err = loadPersonalities("personalities")
	if err != nil {
		slog.Error("加载人格失败",
			"component", "startup",
			"operation", "loadPersonalities",
			"error_kind", service.ErrorConfig,
			"error", err)
		os.Exit(1)
	}
	names := make([]string, 0, len(personalities))
	for name := range personalities {
		names = append(names, name)
	}
	sort.Strings(names)
	slog.Info("人格加载完成",
		"component", "startup",
		"operation", "loadPersonalities",
		"personality_count", len(names),
		"personality_names", strings.Join(names, ", "))

	if err := registry.Register(tool.NewBashTool("workspace")); err != nil {
		slog.Error("工具注册失败", "component", "startup", "tool_name", "bash", "error", err)
		os.Exit(1)
	}
	if err := registry.Register(tool.NewSkillTool("workspace/skills")); err != nil {
		slog.Error("工具注册失败", "component", "startup", "tool_name", "activate_skill", "error", err)
		os.Exit(1)
	}
	if err := registry.Register(tool.NewCreateSkillTool("workspace/skills")); err != nil {
		slog.Error("工具注册失败", "component", "startup", "tool_name", "create_skill", "error", err)
		os.Exit(1)
	}

	http.HandleFunc("POST /api/chat", handleChat)
	http.HandleFunc("POST /api/chat/stream", handleChatStream)
	http.HandleFunc("GET /api/conversations", handleListConversations)
	http.HandleFunc("GET /api/conversations/{id}", handleGetConversation)
	http.HandleFunc("DELETE /api/conversations/{id}", handleDeleteConversation)
	http.HandleFunc("POST /api/council", handleCouncil)
	http.HandleFunc("POST /api/council/stream", handleCouncilStream)
	http.HandleFunc("GET /council", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "council.html")
	})
	http.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "index.html")
	})

	slog.Info("cc-agent-go v11 启动",
		"component", "startup",
		"address", "http://localhost:8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		slog.Error("HTTP 服务退出",
			"component", "startup",
			"operation", "http.ListenAndServe",
			"error_kind", service.ErrorInternal,
			"error", err)
	}
}
