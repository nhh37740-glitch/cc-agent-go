package httpapi

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cc-agent-go/service"
)

type RecentApplicationLogsJSON struct {
	Logs []json.RawMessage `json:"logs"`
}

// handleStopConversation 取消指定会话当前正在执行的 Agent.Run。
// 路径：POST /api/conversations/{id}/stop
func (server *Server) handleStopConversation(
	responseWriter http.ResponseWriter,
	httpRequest *http.Request,
) {
	conversationID := strings.TrimSpace(httpRequest.PathValue("id"))
	if conversationID == "" {
		server.writeAPIError(responseWriter, "handleStopConversation.validate", "",
			server.invalidRequestError("handleStopConversation.validate",
				fmt.Errorf("conversationId 不能为空")))
		return
	}
	cancelled := server.conversationRunRegistry.CancelRun(conversationID)
	responseWriter.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(responseWriter).Encode(map[string]any{
		"conversationId": conversationID,
		"cancelled":      cancelled,
	})
}

func (server *Server) handleConversationEvents(
	responseWriter http.ResponseWriter,
	request *http.Request,
) {
	conversationID := request.PathValue("id")
	if strings.TrimSpace(conversationID) == "" {
		server.writeAPIError(
			responseWriter,
			"handleConversationEvents.conversationID",
			"",
			server.invalidRequestError(
				"handleConversationEvents.conversationID",
				fmt.Errorf("缺少 conversationId"),
			),
		)
		return
	}

	responseWriter.Header().Set(
		"Content-Type",
		"text/event-stream;charset=UTF-8",
	)
	responseWriter.Header().Set("Cache-Control", "no-cache")
	responseWriter.Header().Set("Connection", "keep-alive")
	responseWriter.Header().Set("X-Accel-Buffering", "no")

	responseWriterFlusher, supportsFlush :=
		responseWriter.(http.Flusher)
	if !supportsFlush {
		server.writeAPIError(
			responseWriter,
			"handleConversationEvents.flusher",
			conversationID,
			service.NewAppError(
				service.ErrorInternal,
				"handleConversationEvents.flusher",
				0,
				fmt.Errorf("ResponseWriter 不支持 http.Flusher"),
			),
		)
		return
	}

	conversationEventChannel :=
		server.conversationEventReceivers.AddReceiver(conversationID)
	defer server.conversationEventReceivers.RemoveReceiver(
		conversationID,
		conversationEventChannel,
	)

	fmt.Fprint(responseWriter, ": connected\n\n")
	responseWriterFlusher.Flush()

	keepAliveTicker := time.NewTicker(15 * time.Second)
	defer keepAliveTicker.Stop()

	for {
		select {
		case eventJSON := <-conversationEventChannel:
			fmt.Fprintf(responseWriter, "data: %s\n\n", eventJSON)
			responseWriterFlusher.Flush()

		case <-keepAliveTicker.C:
			fmt.Fprint(responseWriter, ": keep-alive\n\n")
			responseWriterFlusher.Flush()

		case <-request.Context().Done():
			return
		}
	}
}

func (server *Server) handleListConversations(w http.ResponseWriter, r *http.Request) {
	workingDirectory := r.URL.Query().Get("workingDirectory")
	if !filepath.IsAbs(workingDirectory) {
		server.writeAPIError(w, "handleListConversations.validate", "",
			server.invalidRequestError("handleListConversations.validate",
				fmt.Errorf("workingDirectory 必须是绝对路径")))
		return
	}
	summaries, err := server.projectConversationStore.ListConversations(workingDirectory)
	if err != nil {
		server.writeAPIError(w, "handleListConversations.list", "",
			service.NewAppError(service.ErrorStorageRead,
				"store.ListConversations", 0, err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(summaries)
}

func (server *Server) handleGetConversation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	workingDirectory := r.URL.Query().Get("workingDirectory")
	session, err := server.projectConversationStore.LoadConversation(workingDirectory, id)
	if err != nil {
		server.writeAPIError(w, "handleGetConversation.load", id,
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

func (server *Server) handleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	workingDirectory := r.URL.Query().Get("workingDirectory")
	if err := server.projectConversationStore.DeleteConversation(workingDirectory, id); err != nil {
		server.writeAPIError(w, "handleDeleteConversation.delete", id,
			service.NewAppError(service.ErrorStorageWrite,
				"store.DeleteConversation", 0, err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (server *Server) handleListRecentApplicationLogs(
	responseWriter http.ResponseWriter,
	httpRequest *http.Request,
) {
	const defaultMaximumLogEntries = 200
	const hardMaximumLogEntries = 500

	maximumLogEntries := defaultMaximumLogEntries
	if requestedLimit := strings.TrimSpace(
		httpRequest.URL.Query().Get("limit"),
	); requestedLimit != "" {
		convertedLimit, convertLimitError := strconv.Atoi(requestedLimit)
		if convertLimitError != nil ||
			convertedLimit < 1 ||
			convertedLimit > hardMaximumLogEntries {
			server.writeAPIError(
				responseWriter,
				"handleListRecentApplicationLogs.validateLimit",
				"",
				server.invalidRequestError(
					"handleListRecentApplicationLogs.validateLimit",
					fmt.Errorf("limit 必须是 1 到 %d 的整数", hardMaximumLogEntries),
				),
			)
			return
		}
		maximumLogEntries = convertedLimit
	}

	recentApplicationLogs, readLogsError :=
		server.readRecentApplicationLogs(server.applicationLogFilePath, maximumLogEntries)
	if readLogsError != nil {
		server.writeAPIError(
			responseWriter,
			"handleListRecentApplicationLogs.read",
			"",
			service.NewAppError(
				service.ErrorStorageRead,
				"handleListRecentApplicationLogs.read",
				0,
				readLogsError,
			),
		)
		return
	}

	responseWriter.Header().Set("Content-Type", "application/json;charset=UTF-8")
	if encodeLogsError := json.NewEncoder(responseWriter).Encode(
		RecentApplicationLogsJSON{Logs: recentApplicationLogs},
	); encodeLogsError != nil {
		slog.Error(
			"最近日志 JSON 写入失败",
			"component", "http",
			"operation", "handleListRecentApplicationLogs.encode",
			"error_kind", service.ErrorInternal,
			"error", encodeLogsError,
		)
	}
}

func (server *Server) readRecentApplicationLogs(
	logFilePath string,
	maximumLogEntries int,
) ([]json.RawMessage, error) {
	if maximumLogEntries < 1 {
		return nil, fmt.Errorf("maximumLogEntries 必须大于 0")
	}
	applicationLogFile, openLogFileError := os.Open(logFilePath)
	if os.IsNotExist(openLogFileError) {
		return []json.RawMessage{}, nil
	}
	if openLogFileError != nil {
		return nil, fmt.Errorf("打开日志文件失败: %w", openLogFileError)
	}
	defer applicationLogFile.Close()

	recentApplicationLogs := make([]json.RawMessage, 0, maximumLogEntries)
	logLineScanner := bufio.NewScanner(applicationLogFile)
	logLineScanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for logLineScanner.Scan() {
		logLine := append([]byte(nil), logLineScanner.Bytes()...)
		if !json.Valid(logLine) {
			continue
		}
		if len(recentApplicationLogs) == maximumLogEntries {
			copy(recentApplicationLogs, recentApplicationLogs[1:])
			recentApplicationLogs[len(recentApplicationLogs)-1] =
				json.RawMessage(logLine)
			continue
		}
		recentApplicationLogs = append(
			recentApplicationLogs,
			json.RawMessage(logLine),
		)
	}
	if scanLogFileError := logLineScanner.Err(); scanLogFileError != nil {
		return nil, fmt.Errorf("读取日志文件失败: %w", scanLogFileError)
	}
	return recentApplicationLogs, nil
}
