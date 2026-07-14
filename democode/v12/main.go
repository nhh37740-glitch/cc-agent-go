package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"cc-agent-go/democode/v12/game"
	"cc-agent-go/democode/v12/service"
)

type activeMysterySession struct {
	session *game.Session
	input   chan string
}

var (
	library       *service.Library
	activeMystery *activeMysterySession
	activeMu      sync.Mutex
)

func main() {
	root := runtimeRoot()
	library = service.NewLibrary(filepath.Join(root, "scripts"))
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	portNum, err := strconv.Atoi(port)
	if err != nil || portNum < 1 {
		log.Fatalf("无效 PORT: %s", port)
	}

	http.HandleFunc("GET /mystery", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(root, "mystery.html"))
	})
	http.HandleFunc("GET /api/mystery/scripts", handleListScripts)
	http.HandleFunc("GET /api/mystery/scripts/{id}", handleGetScript)
	http.HandleFunc("GET /api/mystery/stream", handleMysteryStream)
	http.HandleFunc("POST /api/mystery/input", handleMysteryInput)

	fmt.Printf("v12 剧本杀服务启动: http://localhost:%d/mystery\n", portNum)
	for _, url := range localURLs(portNum) {
		fmt.Printf("  LAN: %s/mystery\n", url)
	}
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func handleListScripts(w http.ResponseWriter, r *http.Request) {
	items, err := library.ListScripts()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, items)
}

func handleGetScript(w http.ResponseWriter, r *http.Request) {
	script, err := library.LoadScript(r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, service.Summary(script))
}

func handleMysteryStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream;charset=UTF-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "不支持流式传输", http.StatusInternalServerError)
		return
	}

	scriptID := r.URL.Query().Get("scriptId")
	if scriptID == "" {
		scriptID = "farewell-poem"
	}
	script, err := library.LoadScript(scriptID)
	if err != nil {
		sendSSE(w, flusher, map[string]any{"type": "error", "message": err.Error()})
		return
	}
	session, err := game.NewSession(script, game.Options{
		SessionID:     fmt.Sprintf("mystery-%d", time.Now().UnixNano()),
		PlayerName:    r.URL.Query().Get("playerName"),
		PlayerAvatar:  r.URL.Query().Get("avatar"),
		Language:      r.URL.Query().Get("language"),
		PreferredRole: r.URL.Query().Get("roleId"),
	})
	if err != nil {
		sendSSE(w, flusher, map[string]any{"type": "error", "message": err.Error()})
		return
	}

	active := &activeMysterySession{session: session, input: make(chan string, 1)}
	activeMu.Lock()
	activeMystery = active
	activeMu.Unlock()
	defer func() {
		activeMu.Lock()
		if activeMystery == active {
			activeMystery = nil
		}
		activeMu.Unlock()
	}()

	sendSSE(w, flusher, map[string]any{"type": "script", "script": service.Summary(script)})
	sendSSE(w, flusher, map[string]any{"type": "role", "role": session.HumanRole(), "player": session.HumanSeat()})
	sendSSE(w, flusher, session.PublicState())

	for {
		phase, ok := session.CurrentPhase()
		if !ok {
			break
		}
		for _, frame := range session.EnterCurrentPhase() {
			sendSSE(w, flusher, frame)
		}
		sendSSE(w, flusher, session.PublicState())
		if phase.WaitForInput {
			select {
			case text := <-active.input:
				for _, frame := range session.RecordHumanInput(text) {
					sendSSE(w, flusher, frame)
				}
			case <-r.Context().Done():
				return
			}
		} else {
			time.Sleep(600 * time.Millisecond)
		}
		if !session.Advance() {
			break
		}
		time.Sleep(450 * time.Millisecond)
	}
	sendSSE(w, flusher, session.RevealFrame())
	sendSSE(w, flusher, map[string]any{"type": "done"})
}

func handleMysteryInput(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Text == "" {
		http.Error(w, "text 不能为空", http.StatusBadRequest)
		return
	}
	activeMu.Lock()
	active := activeMystery
	activeMu.Unlock()
	if active == nil {
		http.Error(w, "没有进行中的剧本杀", http.StatusBadRequest)
		return
	}
	select {
	case active.input <- req.Text:
		writeJSON(w, map[string]string{"status": "ok"})
	default:
		http.Error(w, "上一条输入仍在处理", http.StatusConflict)
	}
}

func writeJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	_ = json.NewEncoder(w).Encode(data)
}

func sendSSE(w http.ResponseWriter, flusher http.Flusher, data any) {
	b, _ := json.Marshal(data)
	fmt.Fprintf(w, "data: %s\n\n", b)
	flusher.Flush()
}

func runtimeRoot() string {
	candidates := []string{".", "democode/v12"}
	for _, candidate := range candidates {
		if _, err := os.Stat(filepath.Join(candidate, "scripts")); err == nil {
			return candidate
		}
	}
	return "."
}

func localURLs(port int) []string {
	var out []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.To4() == nil {
				continue
			}
			out = append(out, fmt.Sprintf("http://%s:%d", ip.String(), port))
		}
	}
	return out
}
