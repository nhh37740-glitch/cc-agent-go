package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

	"cc-agent-go/model"
	"cc-agent-go/service"
)

func (server *Server) handleCouncil(w http.ResponseWriter, r *http.Request) {
	var req service.CouncilRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.writeAPIError(w, "handleCouncil.decode", "",
			server.invalidRequestError("handleCouncil.decode", err))
		return
	}
	if req.Topic == "" {
		server.writeAPIError(w, "handleCouncil.validate", "",
			server.invalidRequestError("handleCouncil.validate", fmt.Errorf("topic 不能为空")))
		return
	}
	if req.MaxRounds < 1 {
		req.MaxRounds = 3
	}
	cfg := server.loadConfig()
	transcript, totalTokens, err := service.RunCouncil(
		req.Topic, req.MaxRounds, req.Interruption, server.personalities, cfg)
	if err != nil {
		server.writeAPIError(w, "handleCouncil.run", "", err)
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

func (server *Server) handleCouncilStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream;charset=UTF-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		server.writeAPIError(w, "handleCouncilStream.flusher", "",
			service.NewAppError(service.ErrorInternal, "handleCouncilStream.flusher", 0,
				fmt.Errorf("ResponseWriter 不支持 http.Flusher")))
		return
	}
	var req service.CouncilRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.writeSSEError(w, flusher, "handleCouncilStream.decode", "",
			server.invalidRequestError("handleCouncilStream.decode", err))
		return
	}
	if req.Topic == "" {
		server.writeSSEError(w, flusher, "handleCouncilStream.validate", "",
			server.invalidRequestError("handleCouncilStream.validate", fmt.Errorf("topic 不能为空")))
		return
	}
	if req.MaxRounds < 1 {
		req.MaxRounds = 3
	}

	cfg := server.loadConfig()
	topicFrame, _ := json.Marshal(map[string]string{"type": "topic", "text": req.Topic})
	fmt.Fprintf(w, "data: %s\n\n", topicFrame)
	flusher.Flush()

	done := make(chan struct{})
	go func() {
		defer close(done)
		names := make([]string, 0, len(server.personalities))
		for name := range server.personalities {
			names = append(names, name)
		}
		sort.Strings(names)

		history := []model.Message{
			{Role: "user", Content: []model.MessageContentBlock{
				model.TextContentBlock{Text: "【元老院议题】" + req.Topic},
			}},
		}
		if req.Interruption != "" {
			history = append(history, model.Message{
				Role: "user",
				Content: []model.MessageContentBlock{
					model.TextContentBlock{Text: "【公民插话】" + req.Interruption},
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
				resp, err := service.Chat(r.Context(), messages, server.personalities[name], cfg, nil, 4096)
				if err != nil {
					server.writeSSEError(w, flusher, "handleCouncilStream.chat", "", err)
					return
				}
				history = append(history, model.Message{
					Role: "assistant",
					Content: []model.MessageContentBlock{
						model.TextContentBlock{Text: "【" + name + "】" + resp.Text},
					},
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
