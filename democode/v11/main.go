package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"cc-agent-go/democode/v11/config"
	"cc-agent-go/democode/v11/game"
	"cc-agent-go/democode/v11/model"
	"cc-agent-go/democode/v11/service"
	"cc-agent-go/democode/v11/tool"
)

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
	return result, nil
}

var personalities map[string]string
var werewolfPersonalities map[string]string
var werewolfCharacterPersonalities map[string]string
var werewolfLanguagePrompts map[string]string

const memoryRule = `## memory/AGENT.MD 长期记忆规则

memory/AGENT.MD 是你的长期记忆文件。如果用户在对话中表达了会反复使用的偏好、项目规则、任务状态或重要决策，你必须主动用 bash 工具创建或更新该文件。
更新方式：bash 执行 echo "内容" >> memory/AGENT.MD
（bash 工具的工作目录已经是 workspace/，所以直接写 memory/AGENT.MD 即可）`

const systemPromptBase = `你是一个 AI 助手。你可以使用 bash 工具执行 shell 命令来操作文件。
当用户要求创建文件、读文件、执行命令时，你必须调用 bash 工具，不要只用文字说明。
你需要结合对话历史中的上下文来理解用户的追问和省略表达。
如果 activate_skill 工具列出的技能和用户需求匹配，先调用 activate_skill 激活技能再回答。`

func loadMemory() string {
	data, err := os.ReadFile("workspace/memory/AGENT.MD")
	if err != nil {
		return ""
	}
	return string(data)
}

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

func handleChat(w http.ResponseWriter, r *http.Request) {
	var req model.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "请求体必须是合法 JSON", http.StatusBadRequest)
		return
	}
	cfg := config.Load()
	store := service.NewStore(cfg.SessionsDir)
	reply, convId, err := service.Run(req.Message, req.ConversationId, buildSystemPrompt(), cfg, registry, store)
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
	conversationId := req.ConversationId
	if conversationId == "" {
		conversationId = service.GenerateConversationId()
	}
	convIdJSON, _ := json.Marshal(map[string]string{"type": "conversation_id", "conversationId": conversationId})
	fmt.Fprintf(w, "data: %s\n\n", convIdJSON)
	flusher.Flush()
	cfg := config.Load()
	store := service.NewStore(cfg.SessionsDir)
	done := make(chan struct{})
	go func() {
		defer close(done)
		reply, _, err := service.RunStream(req.Message, conversationId, buildSystemPrompt(), cfg, registry, store, func(token string) {
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

func handleListConversations(w http.ResponseWriter, r *http.Request) {
	cfg := config.Load()
	store := service.NewStore(cfg.SessionsDir)
	summaries, err := store.ListConversations()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(summaries)
}

func handleGetConversation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, err := service.NewStore(config.Load().SessionsDir).LoadConversation(id)
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
	if err := service.NewStore(config.Load().SessionsDir).DeleteConversation(r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func handleCouncil(w http.ResponseWriter, r *http.Request) {
	var req service.CouncilRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "JSON 错误", http.StatusBadRequest)
		return
	}
	if req.Topic == "" {
		http.Error(w, "topic 不能为空", http.StatusBadRequest)
		return
	}
	if req.MaxRounds < 1 {
		req.MaxRounds = 3
	}
	transcript, totalTokens, err := service.RunCouncil(req.Topic, req.MaxRounds, req.Interruption, personalities, config.Load())
	if err != nil {
		http.Error(w, fmt.Sprintf("辩论失败: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(service.CouncilResponse{Topic: req.Topic, Rounds: req.MaxRounds, Transcript: transcript, TotalTokens: totalTokens})
}

func handleCouncilStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream;charset=UTF-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "不支持流式", http.StatusInternalServerError)
		return
	}
	var req service.CouncilRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fmt.Fprintf(w, "data: {\"type\":\"error\"}\n\n")
		flusher.Flush()
		return
	}
	if req.Topic == "" {
		fmt.Fprintf(w, "data: {\"type\":\"error\"}\n\n")
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
		history := []model.Message{{Role: "user", Content: []model.ContentBlock{{Type: "text", Text: "【元老院议题】" + req.Topic}}}}
		if req.Interruption != "" {
			history = append(history, model.Message{Role: "user", Content: []model.ContentBlock{{Type: "text", Text: "【公民插话】" + req.Interruption}}})
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
					fmt.Fprintf(w, "data: {\"type\":\"error\"}\n\n")
					flusher.Flush()
					return
				}
				history = append(history, model.Message{Role: "assistant", Content: []model.ContentBlock{{Type: "text", Text: "【" + name + "】" + resp.Text}}})
				sf, _ := json.Marshal(map[string]any{"type": "speech", "round": round, "agent": name, "text": resp.Text})
				fmt.Fprintf(w, "data: %s\n\n", sf)
				flusher.Flush()
			}
		}
		fmt.Fprintf(w, "data: {\"type\":\"done\"}\n\n")
		flusher.Flush()
	}()
	<-done
}

// ============================================================================
// 狼人杀
// ============================================================================

type gameSession struct {
	state         *game.State
	transcript    []service.RoomMessage
	cfg           config.Config
	personMap     map[string]string
	charMap       map[string]string
	humanName     string
	humanInput    chan string
	flusher       http.Flusher
	w             http.ResponseWriter
	wlog          *log.Logger
	wolfRounds    int
	dayRounds     int
	seerMemory    string
	privateMemory map[string][]string
	language      string
	langPrompt    string
	ended         bool
}

var activeGame *gameSession

var werewolfSeatOrder = []string{"钟匠赫尔", "药师薇拉", "守墓人洛林", "铁匠巴伦", "猎犬师诺克", "修女伊芙", "磨坊女艾达", "旅人"}

func handleWerewolfStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream;charset=UTF-8")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "不支持流式", http.StatusInternalServerError)
		return
	}
	if len(werewolfPersonalities) < 8 {
		sendSSE(w, flusher, map[string]string{"type": "error", "message": "缺少人格"})
		return
	}

	nns := append([]string{}, werewolfSeatOrder...)
	hn := "旅人"
	preferredRole := parseWerewolfRole(r.URL.Query().Get("role"))
	wolfRounds := parseWolfRounds(r.URL.Query().Get("wolfRounds"))
	dayRounds := parseDayRounds(r.URL.Query().Get("dayRounds"))
	language := normalizeWerewolfLanguage(r.URL.Query().Get("language"))
	langPrompt := werewolfLanguagePrompts[language]
	cfg := config.Load()
	cfg.Temperature = parseTemperature(r.URL.Query().Get("temperature"), cfg.Temperature)
	state, err := game.NewWerewolfWithRole(nns, hn, preferredRole)
	if err != nil {
		sendSSE(w, flusher, map[string]string{"type": "error", "message": err.Error()})
		return
	}

	r2p := map[game.Role]string{
		game.RoleWerewolf: "werewolf_a", game.RoleSeer: "seer", game.RoleWitch: "witch",
		game.RoleHunter: "hunter", game.RoleVillager: "villager_a",
	}
	rc := map[game.Role]int{}
	pm := make(map[string]string)
	for _, p := range state.Players {
		r := p.Role
		c := rc[r]
		rc[r] = c + 1
		key := r2p[r]
		if r == game.RoleWerewolf || r == game.RoleVillager {
			suf := "_a"
			if c > 0 {
				suf = "_" + string(rune(97+c))
			}
			key = strings.TrimSuffix(key, "_a") + suf
		}
		pm[p.Name] = key
	}

	os.MkdirAll("logs", 0755)
	wf, _ := os.OpenFile("logs/werewolf.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	wl := log.New(wf, "[Wolf] ", log.LstdFlags)
	wl.Printf("===== 新游戏 =====")

	gs := &gameSession{
		state: state, transcript: []service.RoomMessage{}, cfg: cfg,
		personMap: pm, charMap: werewolfCharacterPersonalities, humanName: hn, humanInput: make(chan string, 1),
		privateMemory: make(map[string][]string),
		flusher:       flusher, w: w, wlog: wl, wolfRounds: wolfRounds, dayRounds: dayRounds, language: language, langPrompt: langPrompt,
	}
	activeGame = gs

	wl.Printf("人类角色: %s(%s)", state.PlayerRole(hn), state.PlayerTeam(hn))
	wl.Printf("月痕秘语轮数: %d", wolfRounds)
	wl.Printf("白昼议事轮数: %d", dayRounds)
	wl.Printf("AI发言温度: %.2f", cfg.Temperature)
	wl.Printf("语言: %s", language)
	roleFrame := map[string]interface{}{
		"type": "role", "role": string(state.PlayerRole(hn)), "roleName": roleCN(state.PlayerRole(hn)),
		"name": hn, "team": state.PlayerTeam(hn), "teamName": teamCN(state.PlayerTeam(hn)), "wolfRounds": wolfRounds, "dayRounds": dayRounds, "temperature": cfg.Temperature, "language": language,
	}
	if state.PlayerRole(hn) == game.RoleWerewolf {
		mates := make([]string, 0)
		for _, name := range state.WolfGroup() {
			if name != hn {
				mates = append(mates, name)
			}
		}
		roleFrame["teammates"] = mates
	}
	sendSSE(w, flusher, roleFrame)
	gs.sendPublicState()

	gs.runGame()
	gs.finishGame()
	activeGame = nil
	wl.Printf("===== 结束 =====")
	wf.Close()
}

func (gs *gameSession) runGame() {
	gs.runTalkPhase(game.PhaseIntro, nil)
	for gs.state.Winner == "" {
		gs.state.ClearNightState()
		gs.runWolfNight()
		if gs.state.CheckWin() {
			break
		}

		gs.runSingleActionPhase(game.PhaseNightSeer, gs.state.AliveRoleHolder(game.RoleSeer), "check")
		gs.runSingleActionPhase(game.PhaseNightWitch, gs.state.AliveRoleHolder(game.RoleWitch), "witch")
		killed := gs.state.ApplyNightResults()
		gs.wlog.Printf(" 夜晚结算: 黑雾目标=%s 复苏=%v 凋零=%s 死亡=%v", gs.state.Victim, gs.state.WitchSave, gs.state.WitchPoison, killed)
		gs.announce(gs.state.DayAnnouncement())
		gs.sendPublicState()
		if gs.state.CheckWin() {
			break
		}

		gs.runDayDiscussPhase()
		gs.runVotePhase()
		if gs.state.CheckWin() {
			break
		}
		gs.state.AdvanceRound()
	}
}

func (gs *gameSession) runDayDiscussPhase() {
	phase := game.PhaseDayDiscuss
	participants := gs.state.AlivePlayers()
	for round := 1; round <= gs.dayRounds; round++ {
		gs.state.SetPhase(phase)
		gs.sendPhase(phase, "", nil)
		shuffleStrings(participants)
		for _, name := range participants {
			if !gs.state.PlayerAlive(name) {
				continue
			}
			label := fmt.Sprintf("第%d/%d轮白昼议事", round, gs.dayRounds)
			msg := gs.phasePrompt(phase, name, label, nil)
			gs.collectSpeech(phase, name, msg, "", nil, nil)
		}
	}
}

func (gs *gameSession) runTalkPhase(phase game.Phase, participants []string) {
	if participants == nil {
		participants = gs.state.PlayersInPhase(phase)
	}
	gs.state.SetPhase(phase)
	gs.sendPhase(phase, "", nil)
	shuffleStrings(participants)
	for _, name := range participants {
		if !gs.state.PlayerAlive(name) && phase != game.PhaseIntro {
			continue
		}
		msg := gs.phasePrompt(phase, name, "", nil)
		gs.collectSpeech(phase, name, msg, "", nil, nil)
	}
}

func (gs *gameSession) runWolfNight() {
	phase := game.PhaseNightWolves
	wolves := gs.state.WolfGroup()
	if len(wolves) == 0 {
		return
	}
	gs.state.SetPhase(phase)
	candidates := gs.state.NonWolfAlivePlayers()
	gs.sendPhase(phase, "kill", candidates)
	var intents []*model.GameAction
	for talkRound := 1; talkRound <= gs.wolfRounds; talkRound++ {
		shuffleStrings(wolves)
		for _, name := range wolves {
			if !gs.state.PlayerAlive(name) {
				continue
			}
			msg := gs.phasePrompt(phase, name, fmt.Sprintf("第%d轮月痕低语", talkRound), candidates)
			action := gs.collectSpeech(phase, name, msg, "kill", candidates, wolves)
			if action != nil && action.Action == "kill" && action.Target != "" {
				intents = append(intents, action)
			}
		}
	}
	target := chooseTarget(intents, candidates)
	if target == "" && len(candidates) > 0 {
		target = candidates[rand.Intn(len(candidates))]
	}
	if target != "" {
		if err := gs.state.RecordWolfKill(target); err != nil {
			gs.wlog.Printf("  月痕目标写入失败: %v", err)
		} else {
			text := "黑雾最终记住了 " + target + " 的名字。"
			for _, wolf := range wolves {
				gs.addPrivateMemory(wolf, fmt.Sprintf("第%d夜月痕低语：你和同伴%s最终让黑雾记住%s。", gs.state.Round, strings.Join(wolves, "、"), target))
			}
			gs.transcript = append(gs.transcript, service.AgentReply("黑雾", text, wolves))
			sendSSE(gs.w, gs.flusher, map[string]interface{}{"type": "speech", "from": "黑雾", "text": text, "visibleTo": wolves})
		}
	}
}

func (gs *gameSession) runSingleActionPhase(phase game.Phase, actor, actionKind string) {
	if actor == "" || !gs.state.PlayerAlive(actor) {
		return
	}
	gs.state.SetPhase(phase)
	candidates := gs.candidatesFor(phase, actor)
	gs.sendPhase(phase, actionKind, candidates)
	action := gs.collectSpeech(phase, actor, gs.phasePrompt(phase, actor, "", candidates), actionKind, candidates, []string{actor})
	if action != nil && action.Action != "none" {
		if err := applyAction(gs.state, actor, action); err != nil {
			gs.wlog.Printf("  私密行动执行失败 %s: %v", actor, err)
			return
		}
	}
	if phase == game.PhaseNightSeer && gs.state.SeerCheck != nil {
		l := "没有月痕"
		if gs.state.SeerCheck.IsWolf {
			l = "带有月痕"
		}
		gs.addPrivateMemory(actor, fmt.Sprintf("第%d夜烛火神谕：你查验%s，烛火显示%s。", gs.state.Round, gs.state.SeerCheck.Target, l))
		gs.privateSystem(actor, fmt.Sprintf("烛火神谕：%s %s。", gs.state.SeerCheck.Target, l))
		gs.updateSeerMemory(actor)
	}
	if phase == game.PhaseNightWitch {
		choice := "你没有使用药瓶。"
		if gs.state.WitchSave {
			choice = "你倒出了复苏药。"
			gs.privateSystem(actor, "你倒出了复苏药。")
		}
		if gs.state.WitchPoison != "" {
			choice = "你让凋零药记住了 " + gs.state.WitchPoison + "。"
			gs.privateSystem(actor, "你让凋零药记住了 "+gs.state.WitchPoison+"。")
		}
		victim := "无人"
		if gs.state.Victim != "" {
			victim = gs.state.Victim
		}
		gs.addPrivateMemory(actor, fmt.Sprintf("第%d夜药瓶低语：你看见的黑雾目标是%s；%s 复苏药剩余:%s；凋零药剩余:%s。", gs.state.Round, victim, choice, yesNo(gs.state.WitchHasSave), yesNo(gs.state.WitchHasPoison)))
	}
}

func (gs *gameSession) runVotePhase() {
	phase := game.PhaseDayVote
	gs.state.SetPhase(phase)
	participants := gs.state.AlivePlayers()
	shuffleStrings(participants)
	gs.sendPhase(phase, "vote", participants)
	for _, name := range participants {
		if !gs.state.PlayerAlive(name) {
			continue
		}
		candidates := gs.candidatesFor(phase, name)
		action := gs.collectSpeech(phase, name, gs.phasePrompt(phase, name, "", candidates), "vote", candidates, nil)
		if action == nil || action.Action != "vote" {
			action = &model.GameAction{Actor: name, Phase: string(phase), Action: "vote", Target: fallbackTarget(candidates), Source: "fallback"}
		}
		if err := applyAction(gs.state, name, action); err != nil {
			gs.wlog.Printf("  审判指认失败 %s: %v", name, err)
		}
	}
	lynched, votes := gs.state.ApplyVoteResult()
	if lynched == "" {
		gs.announce("审判钟摇摆不定，今日日落前无人被逐出雾镇。")
	} else {
		gs.announce(fmt.Sprintf("审判钟落下：%s 被逐出雾镇（%d次指认）。", lynched, votes))
		if gs.state.PlayerRole(lynched) == game.RoleHunter {
			gs.runHunterShot(lynched)
		}
	}
	gs.state.ClearVotes()
	gs.sendPublicState()
}

func (gs *gameSession) runHunterShot(shooter string) {
	candidates := gs.state.OtherAlivePlayers(shooter)
	if len(candidates) == 0 {
		return
	}
	phase := game.Phase("hunter_shoot")
	gs.sendPhase(phase, "shoot", candidates)
	action := gs.collectSpeech(phase, shooter, fmt.Sprintf("【银弹诀别】你被审判钟逐出雾镇。若要射出银弹，必须明确说出目标姓名。候选: %s", strings.Join(candidates, "、")), "shoot", candidates, []string{shooter})
	if action == nil || action.Action != "shoot" {
		action = &model.GameAction{Actor: shooter, Phase: string(phase), Action: "shoot", Target: fallbackTarget(candidates), Source: "fallback"}
	}
	if err := gs.state.RecordHunterShoot(shooter, action.Target); err != nil {
		gs.wlog.Printf("  银弹失败 %s: %v", shooter, err)
		return
	}
	gs.announce(fmt.Sprintf("%s 离开前射出了银弹，%s 也倒在雾中。", shooter, action.Target))
	gs.state.CheckWin()
}

func (gs *gameSession) collectSpeech(phase game.Phase, name, msg, actionKind string, candidates, visibleTo []string) *model.GameAction {
	var text string
	if name == gs.humanName {
		sendSSE(gs.w, gs.flusher, map[string]interface{}{"type": "prompt", "phase": string(phase), "message": msg, "actionKind": actionKind, "candidates": candidates, "private": len(visibleTo) > 0})
		gs.wlog.Printf(" 等待人类输入...")
		text = <-gs.humanInput
		gs.wlog.Printf("  人类输入: %s", text)
	} else {
		text = gs.callAgentText(phase, name, msg)
	}
	if text == "" {
		return nil
	}
	gs.transcript = append(gs.transcript, service.AgentReply(name, text, visibleTo))
	sendSSE(gs.w, gs.flusher, map[string]interface{}{"type": "speech", "from": name, "text": text, "visibleTo": visibleTo})
	time.Sleep(850 * time.Millisecond)
	if actionKind == "" {
		return nil
	}
	action := gs.parseActionForSpeech(name, text, phase, actionKind, candidates)
	gs.sendActionTrace(name, phase, actionKind, action)
	if action.Action != "none" {
		action.Actor = name
		action.Phase = string(phase)
		action.RawText = text
		gs.wlog.Printf("  主持人解析 %s: action=%s target=%s source=%s", name, action.Action, action.Target, action.Source)
	}
	return action
}

func (gs *gameSession) sendActionTrace(actor string, phase game.Phase, actionKind string, action *model.GameAction) {
	if action == nil {
		action = &model.GameAction{Action: "none", Source: "none"}
	}
	target := action.Target
	if target == "" {
		target = "-"
	}
	source := action.Source
	if source == "" {
		source = "unknown"
	}
	text := fmt.Sprintf("主持人解析：actor=%s phase=%s kind=%s action=%s target=%s source=%s", actor, phase, actionKind, action.Action, target, source)
	sendSSE(gs.w, gs.flusher, map[string]interface{}{
		"type": "speech", "from": "主持人", "text": text, "visibleTo": []string{"雾镜旁听"},
	})
}

func (gs *gameSession) sendJudgeRawTrace(actor string, phase game.Phase, actionKind, raw string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" && err == nil {
		return
	}
	detail := raw
	if detail == "" {
		detail = "-"
	}
	if len(detail) > 180 {
		detail = detail[:180] + "..."
	}
	status := "ok"
	if err != nil {
		status = err.Error()
	}
	text := fmt.Sprintf("主持人LLM原文：actor=%s phase=%s kind=%s status=%s raw=%s", actor, phase, actionKind, status, detail)
	sendSSE(gs.w, gs.flusher, map[string]interface{}{
		"type": "speech", "from": "主持人", "text": text, "visibleTo": []string{"雾镜旁听"},
	})
}

func (gs *gameSession) parseActionForSpeech(name, text string, phase game.Phase, actionKind string, candidates []string) *model.GameAction {
	action, raw, err := parseActionViaLLM(name, text, gs.state, phase, actionKind, gs.cfg, candidates, gs.langPrompt)
	gs.sendJudgeRawTrace(name, phase, actionKind, raw, err)
	if err != nil {
		gs.wlog.Printf("  LLM解析失败 %s: %v", name, err)
	}
	if action != nil && action.Action != "none" {
		return action
	}

	local := parseActionLocal(name, text, phase, actionKind, candidates)
	if local.Action != "none" {
		gs.wlog.Printf("  LLM无动作，使用本地兜底 %s: action=%s target=%s", name, local.Action, local.Target)
	}
	return local
}

func (gs *gameSession) callAgentText(phase game.Phase, name, msg string) string {
	personality, ok := werewolfPersonalities[gs.personMap[name]]
	if !ok {
		personality = "你是雾镇入席者，用中文简短交流。"
	}
	character := strings.TrimSpace(gs.charMap[name])
	if character != "" {
		gs.wlog.Printf(" 人物背景命中 %s: %d字", name, len([]rune(character)))
	} else {
		gs.wlog.Printf(" 人物背景未命中 %s", name)
	}
	roleInfo := service.BuildGameRoleInfo(gs.state, name) + "\n\n## 你的私有夜晚记忆（权威，必须记住）\n" + gs.privateMemoryPrompt(name)
	sp := service.BuildWerewolfSystemPrompt(character, personality, roleInfo, msg, gs.langPrompt)
	vm := service.FilterMessages(gs.transcript, name)
	history := joinLines(vm)
	if history == "" {
		history = "暂无公开记录。"
	}
	messages := []model.Message{{Role: "user", Content: []model.ContentBlock{{Type: "text", Text: "【可见记录】\n" + history + "\n\n请回应当前情景，尤其注意最近一条可见发言。"}}}}
	gs.wlog.Printf(" calling %s(%s)", name, gs.state.PlayerRole(name))
	resp, err := service.Chat(messages, sp, gs.cfg, nil, 4096)
	if err != nil {
		gs.wlog.Printf("  Chat失败: %v", err)
		return ""
	}
	gs.wlog.Printf("  %s -> %s", name, trunc(resp.Text, 60))
	return resp.Text
}

func (gs *gameSession) sendPhase(phase game.Phase, actionKind string, candidates []string) {
	sendSSE(gs.w, gs.flusher, map[string]interface{}{
		"type": "phase", "phase": string(phase), "round": gs.state.Round,
		"actionKind": actionKind, "candidates": candidates,
	})
}

func (gs *gameSession) announce(text string) {
	gs.transcript = append(gs.transcript, service.SystemAnnouncement(text))
	sendSSE(gs.w, gs.flusher, map[string]string{"type": "announce", "text": text})
	gs.wlog.Printf(" 公告: %s", text)
}

func (gs *gameSession) privateSystem(to, text string) {
	visible := []string{to}
	gs.transcript = append(gs.transcript, service.AgentReply("烛火", text, visible))
	sendSSE(gs.w, gs.flusher, map[string]interface{}{"type": "speech", "from": "烛火", "text": text, "visibleTo": visible})
}

func (gs *gameSession) updateSeerMemory(seer string) {
	if gs.state.SeerCheck == nil {
		return
	}
	result := "没有月痕"
	if gs.state.SeerCheck.IsWolf {
		result = "带有月痕"
	}
	prompt := fmt.Sprintf(`你要为雾镇中“能读烛火神谕的人”更新私有记忆。

要求：
- 输入是真实查验结果，必须准确保留。
- 生成一段给该角色后续 API 调用使用的短记忆，不超过180字。
- 必须包含所有已知查验结论，不要遗忘旧结论。
- 使用沉浸说法：烛火、月痕、影子、雾镇；不要使用“预言家、狼人、金水、查杀、身份牌”等桌游黑话。
- 这不是公开发言，是私有记忆，可直接写清楚结论和下一步倾向。

已有私有记忆:
%s

第%d夜新查验:
%s => %s

只输出更新后的私有记忆。`, emptyDash(gs.seerMemory), gs.state.Round, gs.state.SeerCheck.Target, result)
	judgeCfg := gs.cfg
	judgeCfg.Temperature = 0
	resp, err := service.Chat([]model.Message{{
		Role:    "user",
		Content: []model.ContentBlock{{Type: "text", Text: prompt}},
	}}, "你是狼人杀角色记忆整理器。只输出给单个角色使用的私有记忆正文。", judgeCfg, nil, 360)
	if err != nil {
		gs.wlog.Printf("  预言家记忆更新失败: %v", err)
		gs.seerMemory = mergeSeerMemoryFallback(gs.seerMemory, gs.state.Round, gs.state.SeerCheck.Target, result)
	} else {
		gs.seerMemory = strings.TrimSpace(resp.Text)
	}
	if gs.seerMemory == "" {
		return
	}
	text := "烛火记忆：" + gs.seerMemory
	visible := []string{seer}
	gs.transcript = append(gs.transcript, service.AgentReply("烛火记忆", text, visible))
	sendSSE(gs.w, gs.flusher, map[string]interface{}{"type": "speech", "from": "烛火记忆", "text": text, "visibleTo": visible})
}

func (gs *gameSession) addPrivateMemory(name, line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	items := append(gs.privateMemory[name], line)
	if len(items) > 12 {
		items = items[len(items)-12:]
	}
	gs.privateMemory[name] = items
}

func (gs *gameSession) privateMemoryPrompt(name string) string {
	items := gs.privateMemory[name]
	if len(items) == 0 {
		return "暂无。"
	}
	return strings.Join(items, "\n")
}

func (gs *gameSession) candidatesFor(phase game.Phase, actor string) []string {
	switch phase {
	case game.PhaseNightWolves:
		return gs.state.NonWolfAlivePlayers()
	case game.PhaseNightSeer, game.PhaseDayVote, game.Phase("hunter_shoot"):
		return gs.state.OtherAlivePlayers(actor)
	case game.PhaseNightWitch:
		return gs.state.OtherAlivePlayers(actor)
	default:
		return nil
	}
}

func (gs *gameSession) phasePrompt(phase game.Phase, actor, label string, candidates []string) string {
	cand := strings.Join(candidates, "、")
	switch phase {
	case game.PhaseIntro:
		return fmt.Sprintf("【雾镇入席】你是%s。简短介绍公开职业和性格，不透露任何夜晚秘密。", actor)
	case game.PhaseNightWolves:
		if label == "" {
			label = "月痕低语"
		}
		mates := strings.Join(gs.state.WolfGroup(), "、")
		return fmt.Sprintf("【第%d夜·%s】现在是月痕同伴的私密夜谈环节。你的真实夜晚身份: 带月痕者。你可见的同伴: %s。你们本环节要从未带月痕的存活者中确定今晚黑雾目标；这句回复会被主持人解析为本环节行动。请用雾镇语言回应，并明确说出一个完整候选姓名。候选: %s", gs.state.Round, label, mates, cand)
	case game.PhaseNightSeer:
		return fmt.Sprintf("【第%d夜·烛火神谕】现在是读烛火者的私密查验环节，只有你独自行动。你的真实夜晚身份: 读烛火神谕的人。你本环节要从其他存活者中选择一名查验目标；这句回复会被主持人解析为本环节行动。请明确说出一个完整候选姓名，不要写自己的名字。候选: %s", gs.state.Round, cand)
	case game.PhaseNightWitch:
		victim := "今夜没有人被黑雾拖走。"
		if gs.state.Victim != "" {
			victim = gs.state.Victim + " 正被黑雾拖走。"
		}
		return fmt.Sprintf("【第%d夜·药瓶低语】现在是掌药瓶者的私密抉择环节，只有你独自行动。你的真实夜晚身份: 掌药瓶的人。当前夜晚信息: %s 复苏药: %s；凋零药: %s。本环节可救下黑雾目标、放弃救人，或用凋零药指定一名其他存活者；这句回复会被主持人解析为本环节行动。凋零候选: %s", gs.state.Round, victim, yesNo(gs.state.WitchHasSave), yesNo(gs.state.WitchHasPoison), cand)
	case game.PhaseDayDiscuss:
		if label == "" {
			label = "白昼议事"
		}
		return fmt.Sprintf("【%s】你是%s。现在仍是白天讨论，不是投票环节；先回应最近发言，结合死讯、改口、推卸、沉默和急切指认来推进怀疑。可以追问、拉票、反驳或修正判断，但不要使用桌游黑话。", label, actor)
	case game.PhaseDayVote:
		return fmt.Sprintf("【审判钟】你是%s。指认一名存活者接受审判，必须明确说出姓名。候选: %s", actor, cand)
	default:
		return "请发言。"
	}
}

func shuffleStrings(items []string) {
	rand.Shuffle(len(items), func(i, j int) { items[i], items[j] = items[j], items[i] })
}

func chooseTarget(intents []*model.GameAction, candidates []string) string {
	allowed := make(map[string]bool)
	for _, c := range candidates {
		allowed[c] = true
	}
	counts := make(map[string]int)
	last := ""
	for _, intent := range intents {
		if intent == nil || !allowed[intent.Target] {
			continue
		}
		counts[intent.Target]++
		last = intent.Target
	}
	best, bestCount, tie := "", 0, false
	for target, count := range counts {
		if count > bestCount {
			best, bestCount, tie = target, count, false
		} else if count == bestCount && count > 0 {
			tie = true
		}
	}
	if tie {
		return last
	}
	return best
}

func fallbackTarget(candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}
	return candidates[rand.Intn(len(candidates))]
}

func parseActionLocal(actor, text string, phase game.Phase, actionKind string, candidates []string) *model.GameAction {
	target := extractNamedCandidate(text, candidates)
	action := "none"
	switch actionKind {
	case "kill":
		if target != "" {
			action = "kill"
		}
	case "check":
		if target != "" {
			action = "check"
		}
	case "vote":
		if target != "" {
			action = "vote"
		}
	case "shoot":
		if target != "" {
			action = "shoot"
		}
	case "witch":
		if wantsSave(text) {
			action = "save"
			target = ""
		} else if wantsPoison(text) && target != "" {
			action = "poison"
		}
	}
	return &model.GameAction{Actor: actor, Phase: string(phase), Action: action, Target: target, RawText: text, Source: "local"}
}

func extractNamedCandidate(text string, candidates []string) string {
	target, last := "", -1
	for _, candidate := range candidates {
		if idx := strings.LastIndex(text, candidate); idx > last {
			target, last = candidate, idx
		}
	}
	if target != "" {
		return target
	}
	return extractCandidateBySeat(text, candidates)
}

func extractCandidateBySeat(text string, candidates []string) string {
	allowed := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		allowed[candidate] = true
	}
	for seat, name := range werewolfSeatOrder {
		if allowed[name] && containsSeatReference(text, seat+1) {
			return name
		}
	}
	return ""
}

func containsSeatReference(text string, seat int) bool {
	digit := strconv.Itoa(seat)
	chinese := chineseNumber(seat)
	patterns := []string{
		digit + "号", digit + " 号", "第" + digit + "号", "第 " + digit + " 号",
		digit + "位", digit + " 位", "第" + digit + "位", "第 " + digit + " 位",
		chinese + "号", chinese + " 号", "第" + chinese + "号", "第 " + chinese + " 号",
		chinese + "位", chinese + " 位", "第" + chinese + "位", "第 " + chinese + " 位",
	}
	for _, pattern := range patterns {
		if strings.Contains(text, pattern) {
			return true
		}
	}
	return false
}

func candidateAliases(candidate string) []string {
	switch candidate {
	case "钟匠赫尔":
		return []string{"钟匠", "赫尔"}
	case "药师薇拉":
		return []string{"药师", "薇拉"}
	case "守墓人洛林":
		return []string{"守墓人", "守墓", "洛林"}
	case "铁匠巴伦":
		return []string{"铁匠", "巴伦"}
	case "猎犬师诺克":
		return []string{"猎犬师", "猎犬", "诺克"}
	case "修女伊芙":
		return []string{"修女", "伊芙"}
	case "磨坊女艾达":
		return []string{"磨坊女", "磨坊", "艾达"}
	case "旅人":
		return []string{"旅人"}
	default:
		return nil
	}
}

func chineseNumber(n int) string {
	switch n {
	case 1:
		return "一"
	case 2:
		return "二"
	case 3:
		return "三"
	case 4:
		return "四"
	case 5:
		return "五"
	case 6:
		return "六"
	case 7:
		return "七"
	case 8:
		return "八"
	default:
		return strconv.Itoa(n)
	}
}

func (gs *gameSession) finishGame() {
	if gs.ended || gs.state.Winner == "" {
		return
	}
	gs.ended = true
	sendSSE(gs.w, gs.flusher, map[string]string{"type": "announce", "text": fmt.Sprintf("游戏结束！%s获胜！", teamCN(gs.state.Winner))})
	var rv []string
	for _, p := range gs.state.Players {
		rv = append(rv, fmt.Sprintf("%s(%s)", p.Name, roleCN(p.Role)))
	}
	sendSSE(gs.w, gs.flusher, map[string]string{"type": "reveal", "text": strings.Join(rv, "；")})
	sendSSE(gs.w, gs.flusher, map[string]string{"type": "done", "winner": gs.state.Winner})
	gs.wlog.Printf(" 游戏结束: %s胜", gs.state.Winner)
}

func (gs *gameSession) sendPublicState() {
	players := make([]map[string]interface{}, 0, len(gs.state.Players))
	for _, p := range gs.state.Players {
		item := map[string]interface{}{
			"name":    p.Name,
			"alive":   p.Alive,
			"isHuman": p.Name == gs.humanName,
		}
		if p.Name == gs.humanName {
			item["role"] = string(p.Role)
			item["roleName"] = roleCN(p.Role)
			item["team"] = p.Team
			item["teamName"] = teamCN(p.Team)
		}
		players = append(players, item)
	}
	sendSSE(gs.w, gs.flusher, map[string]interface{}{
		"type":    "state",
		"round":   gs.state.Round,
		"phase":   string(gs.state.Phase),
		"players": players,
		"alive":   len(gs.state.AlivePlayers()),
	})
}

func handleWerewolfInput(w http.ResponseWriter, r *http.Request) {
	if activeGame == nil {
		http.Error(w, "没有进行中的游戏", http.StatusBadRequest)
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Text == "" {
		http.Error(w, "text 不能为空", http.StatusBadRequest)
		return
	}
	select {
	case activeGame.humanInput <- req.Text:
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	default:
		http.Error(w, "通道已满", http.StatusInternalServerError)
	}
}

func sendSSE(w http.ResponseWriter, fl http.Flusher, data interface{}) {
	b, _ := json.Marshal(data)
	fmt.Fprintf(w, "data: %s\n\n", b)
	fl.Flush()
}

func joinLines(msgs []service.RoomMessage) string {
	var ls []string
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type == "text" && b.Text != "" {
				t := b.Text
				if len(t) > 200 {
					t = t[:200] + "..."
				}
				ls = append(ls, m.From+": "+t)
			}
		}
	}
	return strings.Join(ls, "\n")
}

// parseActionViaLLM 用 LLM 从玩家自然语言发言中提取结构化游戏行动。
// 无行动阶段（intro/discuss）跳过 LLM 调用，直接返回 none。
func parseActionViaLLM(playerName, text string, state *game.State, phase game.Phase, actionKind string, cfg config.Config, candidates []string, languagePrompt string) (*model.GameAction, string, error) {
	if !phaseHasAction(phase) {
		return &model.GameAction{Action: "none"}, "", nil
	}

	allowed := allowedActionsFor(actionKind)
	systemPrompt := werewolfJudgeSystemPrompt()
	userMsg := buildJudgePrompt(playerName, text, state, phase, actionKind, candidates, allowed, languagePrompt)

	messages := []model.Message{{
		Role:    "user",
		Content: []model.ContentBlock{{Type: "text", Text: userMsg}},
	}}

	judgeCfg := cfg
	judgeCfg.Temperature = 0
	resp, err := service.Chat(messages, systemPrompt, judgeCfg, nil, 480)
	if err != nil {
		return &model.GameAction{Action: "none"}, "", err
	}

	action, normalized, err := parseJudgeJSON(resp.Text)
	if err != nil || !validParsedAction(action, actionKind, candidates) {
		repaired, repairRaw, repairErr := repairJudgeJSON(resp.Text, userMsg, systemPrompt, judgeCfg, actionKind, candidates)
		raw := combineJudgeRaw(resp.Text, repairRaw)
		if repairErr != nil {
			if err != nil {
				return &model.GameAction{Action: "none"}, raw, err
			}
			return &model.GameAction{Action: "none"}, raw, repairErr
		}
		if !validParsedAction(repaired, actionKind, candidates) {
			return &model.GameAction{Action: "none"}, raw, nil
		}
		repaired.Actor = playerName
		repaired.Phase = string(phase)
		repaired.RawText = text
		repaired.Source = "llm_repair"
		return &repaired, raw, nil
	}
	action.Actor = playerName
	action.Phase = string(phase)
	action.RawText = text
	action.Source = "llm"
	return &action, normalized, nil
}

func werewolfJudgeSystemPrompt() string {
	return `你是“雾镇主持人”的命令裁决模型，只做一件事：把玩家的沉浸式自然语言发言翻译成当前狼人杀阶段的结构化游戏命令。

你会收到完整规则、产品体验要求、当前局面、发言者真实身份和阶段目的。身份信息只用于理解命令，不是让你改写剧情。

输出强约束：
1. 只能输出一个 JSON object，不能输出 Markdown、解释、前后缀或多余文本。
2. JSON 必须且只能使用这个结构: {"action":"...","target":"..."}。
3. action 必须从“允许 action”中选择。
4. target 若需要，必须完全复制“合法候选”中的一个姓名；save/none 的 target 必须省略或为空字符串。
5. 如果不能确定发言者是在执行当前阶段命令，输出 {"action":"none"}。
6. 不要把“角色扮演台词”翻译成桌游黑话；只判断其中隐含的实际操作。
7. 不做关键词匹配。根据当前阶段目的、发言者职责、原始发言上下文判断玩家是否已经给出可执行命令。
8. 玩家可能用姓名、职业称呼、简称、座位号或沉浸式描述来指代目标；必须根据候选映射和上下文归一化为合法候选中的完整姓名。
9. 若目标无法唯一映射到一个合法候选，输出 {"action":"none"}。`
}

func buildJudgePrompt(playerName, text string, state *game.State, phase game.Phase, actionKind string, candidates, allowed []string, languagePrompt string) string {
	if strings.TrimSpace(languagePrompt) == "" {
		languagePrompt = "发言主要为简体中文。"
	}
	return fmt.Sprintf(`## 完整游戏规则
%s

## 产品体验要求
%s

## 本局语言设定
%s

## 当前局面
回合: 第%d轮
当前阶段: %s
当前需要解析的操作类型: %s
允许 action: %s
合法候选: %s
候选映射: %s
昨夜黑雾目标: %s
复苏药是否可用: %s
凋零药是否可用: %s
存活者: %s
已离场者: %s

## 发这个消息的人
姓名: %s
真实身份: %s
真实阵营: %s
身份上下文: %s
当前阶段目的: %s

## 玩家原始发言
%s

## 裁决任务
根据完整规则、当前局面、发言者身份目的和原始发言，判断他/她是否在执行当前阶段命令。
只输出 JSON: {"action":"...","target":"..."}。`, werewolfRuleText(), commandJudgeRequirements(), languagePrompt, state.Round, phase, actionKind, strings.Join(allowed, "、"), formatList(candidates), formatCandidateGuide(candidates), emptyDash(state.Victim), yesNo(state.WitchHasSave), yesNo(state.WitchHasPoison), formatList(state.AlivePlayers()), formatList(state.AllKilled), playerName, roleCN(state.PlayerRole(playerName)), teamCN(state.PlayerTeam(playerName)), state.RoleDescription(game.Player{Name: playerName, Role: state.PlayerRole(playerName), Team: state.PlayerTeam(playerName), Alive: true}), phasePurposeForJudge(state, playerName, phase, actionKind), text)
}

func werewolfRuleText() string {
	return `本局为 8 人狼人杀变体：2 名狼人、1 名预言家、1 名女巫、1 名猎人、3 名平民。
- 阵营目标：狼人阵营要让好人阵营无法继续占多数；好人阵营要找出并驱逐所有狼人。
- 夜晚狼人阶段：所有存活狼人私下商议，从非狼人存活者中选择 1 人作为袭击目标。只要发言体现出明确目标，就应解析为 kill。
- 夜晚预言家阶段：预言家选择 1 名其他存活者查验，得知其是否为狼人。此阶段的结构化行动为 check。
- 夜晚女巫阶段：女巫知道当夜被狼人袭击的人。复苏药可救下当夜袭击目标，每局一次；凋零药可毒死 1 名其他存活者，每局一次；同一晚不能同时救和毒。明确救人、挽回、用复苏药、阻止黑雾带走当夜目标，应解析为 save；明确下毒、用凋零药并点名，应解析为 poison；明确不救、不用药或没有可执行动作，解析为 none。
- 白天发言阶段：玩家自由讨论，不解析游戏命令。
- 白天审判阶段：每名存活者必须指认 1 名其他存活者接受审判，明确指认目标时解析为 vote。
- 猎人离场阶段：猎人被审判出局时可指定 1 名其他存活者同归于尽，明确开枪/银弹/带走目标时解析为 shoot。
- 候选限制高于语义推理：需要目标的 action，target 必须是合法候选中的精确姓名。`
}

func commandJudgeRequirements() string {
	return `用户要求：
- 不要把玩家台词写死成固定关键词判断；优先由大模型结合规则、上下文、身份目的和自然语言含义裁决。
- 玩家可以沉浸扮演架空村民，台词可能是万圣节、哥特、奇幻、童话、恐怖风格，不能因为没有说桌游术语就忽略真实命令。
- 主持人解析只服务于游戏状态，不应破坏角色沉浸感。
- 输出必须稳定、机器可解析，严格符合 JSON 格式。
- 对所有行动阶段都要优先结合阶段目的和角色职责判断，不要退化成关键词匹配。`
}

func phasePurposeForJudge(state *game.State, playerName string, phase game.Phase, actionKind string) string {
	switch actionKind {
	case "kill":
		return "作为存活狼人，在夜晚密谋中从非狼人候选里确定今晚袭击目标。沉浸表达中的“让某人消失/交给黑雾/今晚取某人”等都应按目标选择理解。"
	case "check":
		return "作为预言家，本阶段职责是从合法候选中选择一名其他存活者作为查验目标。根据原始发言判断被选择的对象；target 必须按候选映射归一化为合法候选中的完整姓名。若没有选择对象，或无法唯一映射目标，输出 none。"
	case "witch":
		if state.Victim != "" {
			return fmt.Sprintf("作为女巫，你看见 %s 正被黑雾拖走；若使用复苏药就是救下该遇袭者，若使用凋零药则必须另行点名毒杀候选。", state.Victim)
		}
		return "作为女巫，今夜没有狼人袭击目标；不能救空目标，但若明确点名使用凋零药可解析为 poison。"
	case "vote":
		return "作为白天存活者，必须指认一名其他存活者接受审判。"
	case "shoot":
		return "作为猎人离场者，可用银弹指定一名其他存活者同归于尽。"
	default:
		return fmt.Sprintf("当前操作类型为 %s；若发言没有明确执行该操作则输出 none。", actionKind)
	}
}

func parseJudgeJSON(raw string) (model.GameAction, string, error) {
	jsonStr := extractJSONObject(raw)
	if jsonStr == "" {
		return model.GameAction{}, raw, fmt.Errorf("未找到JSON对象")
	}
	var action model.GameAction
	if err := json.Unmarshal([]byte(jsonStr), &action); err != nil {
		return model.GameAction{}, jsonStr, err
	}
	action.Action = strings.TrimSpace(action.Action)
	action.Target = strings.TrimSpace(action.Target)
	return action, jsonStr, nil
}

func extractJSONObject(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end < start {
		return ""
	}
	return strings.TrimSpace(s[start : end+1])
}

func repairJudgeJSON(raw, originalPrompt, systemPrompt string, cfg config.Config, actionKind string, candidates []string) (model.GameAction, string, error) {
	repairPrompt := fmt.Sprintf(`下面是一次主持人命令解析任务。第一次模型输出没有通过 JSON/合法性校验。

请只根据“原任务”和“第一次输出”修复为严格 JSON。
允许 action: %s
合法候选: %s
候选映射: %s
需要目标的 action，target 必须完全等于合法候选中的一个名字；若第一次输出用了座位号或简称，请按候选映射改成完整姓名；save/none 的 target 必须为空或省略。

## 原任务
%s

## 第一次输出
%s

只输出 JSON。`, strings.Join(allowedActionsFor(actionKind), "、"), formatList(candidates), formatCandidateGuide(candidates), originalPrompt, raw)
	messages := []model.Message{{
		Role:    "user",
		Content: []model.ContentBlock{{Type: "text", Text: repairPrompt}},
	}}
	resp, err := service.Chat(messages, systemPrompt, cfg, nil, 240)
	if err != nil {
		return model.GameAction{}, "", err
	}
	action, _, err := parseJudgeJSON(resp.Text)
	return action, resp.Text, err
}

func combineJudgeRaw(first, second string) string {
	first = strings.TrimSpace(first)
	second = strings.TrimSpace(second)
	if second == "" {
		return first
	}
	if first == "" {
		return "修复: " + second
	}
	return "初次: " + first + " || 修复: " + second
}

func formatList(items []string) string {
	if len(items) == 0 {
		return "-"
	}
	return strings.Join(items, "、")
}

func formatCandidateGuide(candidates []string) string {
	if len(candidates) == 0 {
		return "-"
	}
	allowed := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		allowed[candidate] = true
	}
	parts := make([]string, 0, len(candidates))
	for seat, name := range werewolfSeatOrder {
		if !allowed[name] {
			continue
		}
		aliases := candidateAliases(name)
		label := fmt.Sprintf("%d号/%s号=%s", seat+1, chineseNumber(seat+1), name)
		if len(aliases) > 0 {
			label += "（别名: " + strings.Join(aliases, "、") + "）"
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, "；")
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func yesNo(v bool) string {
	if v {
		return "可用"
	}
	return "不可用"
}

func mergeSeerMemoryFallback(memory string, round int, target, result string) string {
	line := fmt.Sprintf("第%d夜烛火照见%s：%s。", round, target, result)
	memory = strings.TrimSpace(memory)
	if memory == "" {
		return line
	}
	if strings.Contains(memory, target+"：") || strings.Contains(memory, target+" ") || strings.Contains(memory, target+"，") {
		return memory + " " + line
	}
	return memory + " " + line
}

func allowedActionsFor(actionKind string) []string {
	switch actionKind {
	case "kill":
		return []string{"kill", "none"}
	case "check":
		return []string{"check", "none"}
	case "witch":
		return []string{"save", "poison", "none"}
	case "vote":
		return []string{"vote", "none"}
	case "shoot":
		return []string{"shoot", "none"}
	default:
		return []string{"none"}
	}
}

func validParsedAction(action model.GameAction, actionKind string, candidates []string) bool {
	allowed := map[string]bool{}
	for _, item := range allowedActionsFor(actionKind) {
		allowed[item] = true
	}
	if !allowed[action.Action] {
		return false
	}
	switch action.Action {
	case "none", "save":
		return true
	default:
		return containsString(candidates, action.Target)
	}
}

// applyAction 将解析出的结构化行动写入游戏状态。
func applyAction(state *game.State, playerName string, action *model.GameAction) error {
	switch action.Action {
	case "kill":
		return state.RecordWolfKill(action.Target)
	case "check":
		return state.RecordSeerCheck(action.Target)
	case "save":
		return state.RecordWitchAction(true, "")
	case "poison":
		return state.RecordWitchAction(false, action.Target)
	case "vote":
		return state.RecordVote(playerName, action.Target)
	case "shoot":
		return state.RecordHunterShoot(playerName, action.Target)
	}
	return nil
}

// phaseHasAction 返回当前阶段是否有游戏行动需要解析。
func phaseHasAction(phase game.Phase) bool {
	switch phase {
	case game.PhaseNightWolves, game.PhaseNightSeer, game.PhaseNightWitch, game.PhaseDayVote, game.Phase("hunter_shoot"):
		return true
	}
	return false
}

func wantsSave(text string) bool {
	negative := []string{"不救", "不复苏", "不用复苏", "不用药", "不倒药", "不出药", "不能救", "救不了", "无药可救", "不使用复苏"}
	if containsAny(text, negative) {
		return false
	}
	explicit := []string{"我救", "要救", "选择救", "决定救", "救下", "救回", "救他", "救她", "拯救", "用解药", "使用解药", "倒出复苏", "用复苏药", "使用复苏", "倒出药", "复苏药给"}
	return startsWithCommandWord(text, "救") || containsAny(text, explicit)
}

func wantsPoison(text string) bool {
	if strings.Contains(text, "不毒") || strings.Contains(text, "不用毒") || strings.Contains(text, "不用凋零") {
		return false
	}
	explicit := []string{"我毒", "要毒", "选择毒", "决定毒", "毒 ", "毒死", "下毒", "用毒", "使用毒", "凋零药给", "用凋零药", "让凋零药记住"}
	return containsAny(text, explicit)
}

func containsString(items []string, item string) bool {
	for _, v := range items {
		if v == item {
			return true
		}
	}
	return false
}

func containsAny(text string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func startsWithCommandWord(text, word string) bool {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, word) {
		return false
	}
	rest := strings.TrimPrefix(trimmed, word)
	if rest == "" {
		return true
	}
	r, _ := utf8.DecodeRuneInString(rest)
	return unicode.IsSpace(r) || strings.ContainsRune("，。！？；：、,.!?;:", r)
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func roleCN(role game.Role) string {
	switch role {
	case game.RoleWerewolf:
		return "狼人"
	case game.RoleSeer:
		return "预言家"
	case game.RoleWitch:
		return "女巫"
	case game.RoleHunter:
		return "猎人"
	default:
		return "平民"
	}
}

func teamCN(team string) string {
	if team == "werewolf" {
		return "狼人阵营"
	}
	return "好人阵营"
}

func parseWerewolfRole(raw string) game.Role {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "werewolf", "wolf":
		return game.RoleWerewolf
	case "seer":
		return game.RoleSeer
	case "witch":
		return game.RoleWitch
	case "hunter":
		return game.RoleHunter
	case "villager":
		return game.RoleVillager
	default:
		return ""
	}
}

func normalizeWerewolfLanguage(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "ja", "ja-jp", "japanese":
		return "ja-JP"
	case "zh", "zh-cn", "cn", "chinese", "":
		return "zh-CN"
	default:
		if _, ok := werewolfLanguagePrompts[raw]; ok {
			return raw
		}
		return "zh-CN"
	}
}

func parseWolfRounds(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 2
	}
	if n < 1 {
		return 1
	}
	if n > 5 {
		return 5
	}
	return n
}

func parseDayRounds(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 3
	}
	if n < 1 {
		return 1
	}
	if n > 5 {
		return 5
	}
	return n
}

func parseTemperature(raw string, fallback float64) float64 {
	if strings.TrimSpace(raw) == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return fallback
	}
	if value < 0 {
		return 0
	}
	if value > 1.5 {
		return 1.5
	}
	return value
}

// ============================================================================
// main
// ============================================================================

func main() {
	var err error
	personalities, err = loadPersonalities("personalities")
	if err != nil {
		fmt.Printf("加载人格失败: %v\n", err)
		os.Exit(1)
	}
	names := make([]string, 0, len(personalities))
	for name := range personalities {
		names = append(names, name)
	}
	sort.Strings(names)
	fmt.Printf("已加载 %d 位元老: %s\n", len(names), strings.Join(names, ", "))

	wwP, werr := loadPersonalities("personalities/werewolf")
	if werr != nil {
		fmt.Printf("加载狼人杀人格失败: %v\n", werr)
		os.Exit(1)
	}
	werewolfPersonalities = wwP
	wn := make([]string, 0, len(wwP))
	for n := range wwP {
		wn = append(wn, n)
	}
	sort.Strings(wn)
	fmt.Printf("已加载 %d 位狼人杀角色: %s\n", len(wn), strings.Join(wn, ", "))

	wcP, wcErr := loadPersonalities("personalities/werewolf/characters")
	if wcErr != nil {
		fmt.Printf("加载狼人杀人物背景失败: %v\n", wcErr)
		os.Exit(1)
	}
	werewolfCharacterPersonalities = wcP
	wcn := make([]string, 0, len(wcP))
	for n := range wcP {
		wcn = append(wcn, n)
	}
	sort.Strings(wcn)
	fmt.Printf("已加载 %d 位狼人杀人物背景: %s\n", len(wcn), strings.Join(wcn, ", "))

	wlP, wlErr := loadPersonalities("personalities/werewolf/languages")
	if wlErr != nil {
		fmt.Printf("加载狼人杀语言提示失败: %v\n", wlErr)
		os.Exit(1)
	}
	werewolfLanguagePrompts = wlP
	wln := make([]string, 0, len(wlP))
	for n := range wlP {
		wln = append(wln, n)
	}
	sort.Strings(wln)
	fmt.Printf("已加载 %d 份狼人杀语言提示: %s\n", len(wln), strings.Join(wln, ", "))

	registry.Register(tool.NewBashTool("workspace"))
	registry.Register(tool.NewSkillTool("workspace/skills"))
	registry.Register(tool.NewCreateSkillTool("workspace/skills"))

	http.HandleFunc("GET /api/werewolf/stream", handleWerewolfStream)
	http.HandleFunc("POST /api/werewolf/input", handleWerewolfInput)
	http.HandleFunc("POST /api/chat", handleChat)
	http.HandleFunc("POST /api/chat/stream", handleChatStream)
	http.HandleFunc("GET /api/conversations", handleListConversations)
	http.HandleFunc("GET /api/conversations/{id}", handleGetConversation)
	http.HandleFunc("DELETE /api/conversations/{id}", handleDeleteConversation)
	http.HandleFunc("POST /api/council", handleCouncil)
	http.HandleFunc("POST /api/council/stream", handleCouncilStream)
	http.HandleFunc("GET /werewolf", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, "werewolf.html")
	})
	http.HandleFunc("GET /static/{file}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "max-age=86400")
		http.ServeFile(w, r, "static/"+r.PathValue("file"))
	})
	http.HandleFunc("GET /council", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, "index.html") })
	http.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, "../v1/index.html") })

	fmt.Println("cc-agent-go v11 🐺 狼人杀 启动在 http://localhost:8080")
	fmt.Printf("  狼人杀: http://localhost:8080/werewolf\n")
	fmt.Printf("  辩论室: http://localhost:8080/council\n")
	fmt.Printf("  教学沙箱: http://localhost:8080/\n")
	for _, url := range localLANURLs(8080) {
		fmt.Printf("  手机/LAN: %s/werewolf\n", url)
	}
	if err := http.ListenAndServe(":8080", nil); err != nil {
		fmt.Printf("HTTP 服务退出: %v\n", err)
	}
}

func localLANURLs(port int) []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var urls []string
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP == nil || ipNet.IP.IsLoopback() {
			continue
		}
		ip := ipNet.IP.To4()
		if ip == nil {
			continue
		}
		urls = append(urls, fmt.Sprintf("http://%s:%d", ip.String(), port))
	}
	sort.Strings(urls)
	return urls
}
