package game

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
)

type Phase string

const (
	PhaseIntro           Phase = "intro"
	PhaseNightWolves     Phase = "night_wolves"
	PhaseNightSeer       Phase = "night_seer"
	PhaseNightWitch      Phase = "night_witch"
	PhaseDayAnnounce     Phase = "day_announce"
	PhaseDayDiscuss      Phase = "day_discuss"
	PhaseDayVote         Phase = "day_vote"
	PhaseDayAnnounceVote Phase = "day_announce_vote"
	PhaseEnd             Phase = "end"
)

type Role string

const (
	RoleWerewolf Role = "werewolf"
	RoleSeer     Role = "seer"
	RoleWitch    Role = "witch"
	RoleHunter   Role = "hunter"
	RoleVillager Role = "villager"
)

type Player struct {
	Name  string `json:"name"`
	Role  Role   `json:"role"`
	Alive bool   `json:"alive"`
	Team  string `json:"team"`
}

type State struct {
	Phase          Phase             `json:"phase"`
	Round          int               `json:"round"`
	Players        []Player          `json:"players"`
	Victim         string            `json:"victim"`
	Killed         []string          `json:"killed"`
	AllKilled      []string          `json:"allKilled"`
	SeerCheck      *SeerResult       `json:"seerCheck"`
	WitchSave      bool              `json:"witchSave"`
	WitchPoison    string            `json:"witchPoison"`
	WitchHasSave   bool              `json:"witchHasSave"`
	WitchHasPoison bool              `json:"witchHasPoison"`
	Votes          map[string]string `json:"votes"`
	Winner         string            `json:"winner"`
}

type SeerResult struct {
	Target string `json:"target"`
	IsWolf bool   `json:"isWolf"`
}

func NewWerewolf(playerNames []string) (*State, error) {
	if len(playerNames) != 8 {
		return nil, fmt.Errorf("狼人杀需要8名玩家")
	}
	roles := []Role{RoleWerewolf, RoleWerewolf, RoleSeer, RoleWitch, RoleHunter, RoleVillager, RoleVillager, RoleVillager}
	rand.Shuffle(len(roles), func(i, j int) { roles[i], roles[j] = roles[j], roles[i] })
	players := make([]Player, 8)
	for i := range players {
		team := "villager"
		if roles[i] == RoleWerewolf {
			team = "werewolf"
		}
		players[i] = Player{Name: playerNames[i], Role: roles[i], Alive: true, Team: team}
	}
	return &State{
		Phase: PhaseIntro, Round: 1, Players: players,
		Killed: []string{}, AllKilled: []string{},
		WitchHasSave: true, WitchHasPoison: true, Votes: make(map[string]string),
	}, nil
}

func NewWerewolfWithRole(playerNames []string, playerName string, preferred Role) (*State, error) {
	state, err := NewWerewolf(playerNames)
	if err != nil {
		return nil, err
	}
	if preferred == "" {
		return state, nil
	}
	if !validRole(preferred) {
		return nil, fmt.Errorf("无效身份: %s", preferred)
	}

	humanIdx, roleIdx := -1, -1
	for i, p := range state.Players {
		if p.Name == playerName {
			humanIdx = i
		}
		if p.Role == preferred && roleIdx == -1 {
			roleIdx = i
		}
	}
	if humanIdx == -1 {
		return nil, fmt.Errorf("玩家不存在: %s", playerName)
	}
	if roleIdx == -1 {
		return nil, fmt.Errorf("身份不存在: %s", preferred)
	}
	state.Players[humanIdx].Role, state.Players[roleIdx].Role = state.Players[roleIdx].Role, state.Players[humanIdx].Role
	state.refreshTeams()
	return state, nil
}

func (s *State) NextPhase() (Phase, map[string]string) {
	var phase Phase
	var prompts map[string]string
	switch s.Phase {
	case PhaseIntro:
		prompts = s.introPrompts()
		phase = PhaseIntro
		s.Phase = PhaseNightWolves
	case PhaseNightWolves:
		prompts = s.wolfPrompts()
		phase = PhaseNightWolves
		s.Phase = PhaseNightSeer
	case PhaseNightSeer:
		prompts = s.seerPrompts()
		phase = PhaseNightSeer
		s.Phase = PhaseNightWitch
	case PhaseNightWitch:
		prompts = s.witchPrompts()
		phase = PhaseNightWitch
		s.Phase = PhaseDayAnnounce
	case PhaseDayAnnounce:
		phase = PhaseDayAnnounce
		if s.checkWin() {
			s.Phase = PhaseEnd
		} else {
			s.Phase = PhaseDayDiscuss
		}
	case PhaseDayDiscuss:
		prompts = s.discussPrompts()
		phase = PhaseDayDiscuss
		s.Phase = PhaseDayVote
	case PhaseDayVote:
		prompts = s.votePrompts()
		phase = PhaseDayVote

		s.Phase = PhaseDayAnnounceVote
	case PhaseDayAnnounceVote:
		phase = PhaseDayAnnounceVote
		if s.checkWin() {
			s.Phase = PhaseEnd
		} else {
			s.Round++

			s.Phase = PhaseNightWolves
		}
	default:
		return s.Phase, nil
	}
	return phase, prompts
}

func (s *State) WolfGroup() []string {
	var w []string
	for _, p := range s.Players {
		if p.Role == RoleWerewolf && p.Alive {
			w = append(w, p.Name)
		}
	}
	return w
}

func (s *State) RecordWolfKill(target string) error {
	if !contains(s.AlivePlayers(), target) {
		return fmt.Errorf("%s 不是存活玩家", target)
	}
	if p := s.findPlayer(target); p != nil && p.Role == RoleWerewolf {
		return fmt.Errorf("狼人不能选择狼人同伴作为夜晚袭击目标")
	}
	s.Victim = target
	return nil
}

func (s *State) RecordSeerCheck(target string) error {
	if !contains(s.AlivePlayers(), target) {
		return fmt.Errorf("%s 不是存活玩家", target)
	}
	seer := s.findPlayerByRole(RoleSeer)
	if seer == nil || !seer.Alive {
		return fmt.Errorf("预言家已死")
	}
	if seer.Name == target {
		return fmt.Errorf("预言家不能查验自己")
	}
	isWolf := s.findPlayer(target) != nil && s.findPlayer(target).Role == RoleWerewolf
	s.SeerCheck = &SeerResult{Target: target, IsWolf: isWolf}
	return nil
}

func (s *State) RecordWitchAction(save bool, poison string) error {
	witch := s.findPlayerByRole(RoleWitch)
	if witch == nil || !witch.Alive {
		return fmt.Errorf("女巫已死")
	}
	if save && !s.WitchHasSave {
		return fmt.Errorf("解药已用")
	}
	if poison != "" && !s.WitchHasPoison {
		return fmt.Errorf("毒药已用")
	}
	if poison != "" && !contains(s.AlivePlayers(), poison) {
		return fmt.Errorf("%s 不是存活玩家", poison)
	}
	if poison != "" && poison == witch.Name {
		return fmt.Errorf("女巫不能毒自己")
	}
	if save && poison != "" {
		return fmt.Errorf("同一晚不能同时用解药和毒药")
	}
	if save && s.Victim == "" {
		return fmt.Errorf("今夜没有可救的人")
	}
	if save {
		s.WitchSave = true
		s.WitchHasSave = false
	}
	if poison != "" {
		s.WitchPoison = poison
		s.WitchHasPoison = false
	}
	return nil
}

func (s *State) DayAnnouncement() string {
	var parts []string
	parts = append(parts, fmt.Sprintf("【第%d天·天亮】", s.Round))
	for _, name := range s.Killed {
		parts = append(parts, fmt.Sprintf("%s 死了。", name))
	}
	if len(s.Killed) == 0 {
		if s.WitchSave && s.Victim != "" {
			parts = append(parts, fmt.Sprintf("今晚是平安夜，无人死亡。黑雾曾靠近 %s，但被药瓶挡回。", s.Victim))
		} else {
			parts = append(parts, "今晚是平安夜，无人死亡。")
		}
	}
	return strings.Join(parts, " ")
}

func (s *State) RecordVote(voter, target string) error {
	if !contains(s.AlivePlayers(), voter) {
		return fmt.Errorf("%s 不是存活玩家", voter)
	}
	if !contains(s.AlivePlayers(), target) {
		return fmt.Errorf("%s 不是存活玩家", target)
	}
	if voter == target {
		return fmt.Errorf("不能投票给自己")
	}
	if _, exists := s.Votes[voter]; exists {
		return fmt.Errorf("%s 已经完成审判指认", voter)
	}
	s.Votes[voter] = target
	return nil
}

func (s *State) RecordHunterShoot(shooter, target string) error {
	if !contains(s.AlivePlayers(), target) {
		return fmt.Errorf("%s 不是存活玩家", target)
	}
	hunter := s.findPlayer(shooter)
	if hunter == nil || hunter.Role != RoleHunter {
		return fmt.Errorf("%s 不是银弹持有者", shooter)
	}
	if shooter == target {
		return fmt.Errorf("猎人不能开枪打自己")
	}
	s.addKilled(target)
	s.killPlayer(target)
	return nil
}

func (s *State) VoteResult() (string, int) {
	counts := make(map[string]int)
	for _, target := range s.Votes {
		counts[target]++
	}
	maxVotes, winner, tie := 0, "", false
	for target, count := range counts {
		if count > maxVotes {
			maxVotes = count
			winner = target
			tie = false
		} else if count == maxVotes && count > 0 {
			tie = true
		}
	}
	if tie {
		return "", maxVotes
	}
	return winner, maxVotes
}

func (s *State) AlivePlayers() []string {
	var n []string
	for _, p := range s.Players {
		if p.Alive {
			n = append(n, p.Name)
		}
	}
	return n
}

func (s *State) PlayerRole(name string) Role {
	p := s.findPlayer(name)
	if p == nil {
		return ""
	}
	return p.Role
}

func (s *State) PlayerTeam(name string) string {
	p := s.findPlayer(name)
	if p == nil {
		return ""
	}
	return p.Team
}

func (s *State) PlayersInPhase(phase Phase) []string {
	switch phase {
	case PhaseIntro:
		all := make([]string, len(s.Players))
		for i, p := range s.Players {
			all[i] = p.Name
		}
		sort.Strings(all)
		return all
	case PhaseNightWolves:
		return s.WolfGroup()
	case PhaseNightSeer:
		if seer := s.findPlayerByRole(RoleSeer); seer != nil && seer.Alive {
			return []string{seer.Name}
		}
	case PhaseNightWitch:
		if witch := s.findPlayerByRole(RoleWitch); witch != nil && witch.Alive {
			return []string{witch.Name}
		}
	case PhaseDayDiscuss, PhaseDayVote:
		a := s.AlivePlayers()
		sort.Strings(a)
		return a
	}
	return nil
}

func (s *State) AIPlayers(phase Phase, humanName string) []string {
	var ai []string
	for _, name := range s.PlayersInPhase(phase) {
		if name != humanName {
			ai = append(ai, name)
		}
	}
	return ai
}

func (s *State) HumanActive(phase Phase, humanName string) bool {
	for _, name := range s.PlayersInPhase(phase) {
		if name == humanName {
			return true
		}
	}
	return false
}

func (s *State) RoleDescription(p Player) string {
	switch p.Role {
	case RoleWerewolf:
		wolves := s.WolfGroup()
		others := make([]string, 0)
		for _, w := range wolves {
			if w != p.Name {
				others = append(others, w)
			}
		}
		return fmt.Sprintf("暗中事实: 你身上有月痕诅咒；同样带月痕的人: %s。夜里你们要商量让一名未受月痕的人消失。白天必须像普通雾镇村民一样说话，不能说出“狼人”等桌游身份词。", strings.Join(others, "、"))
	case RoleSeer:
		return "暗中事实: 你能读懂烛火神谕。每夜可凝视一名存活者，得知此人是否带有月痕诅咒。白天只能把神谕包装成观察、直觉和推理。"
	case RoleWitch:
		return fmt.Sprintf("暗中事实: 你掌管两只药瓶。复苏药: %s，凋零药: %s。夜里可用复苏药救下遇袭者，或用凋零药带走一名存活者。白天像普通村民一样说话。", yn(s.WitchHasSave), yn(s.WitchHasPoison))
	case RoleHunter:
		return "暗中事实: 你藏着一枚银弹。若你被审判钟逐出村庄，可在离场前指定一名存活者同归于尽。白天不要主动暴露这枚银弹。"
	default:
		return "暗中事实: 你没有秘术，也不知道他人的真实夜晚归属。只能根据发言、死亡、审判指认和前后矛盾推理。"
	}
}

func (s *State) introPrompts() map[string]string {
	p := make(map[string]string)
	for _, pl := range s.Players {
		p[pl.Name] = fmt.Sprintf("【雾镇入席】你是%s。做一个简短自我介绍（不超过50字），只说公开职业和性格，不透露任何夜晚秘密。", pl.Name)
	}
	return p
}

func (s *State) wolfPrompts() map[string]string {
	wolves := s.WolfGroup()
	targets := s.nonWolfAlivePlayers()
	p := make(map[string]string)
	msg := fmt.Sprintf("【第%d夜·月痕低语】现在是月痕同伴的私密夜谈环节。你的真实夜晚身份: 带月痕者。你可见的同伴: %s。你们本环节要从未带月痕的存活者中确定今晚黑雾目标。请用雾镇语言简短商量，并明确说出一个完整候选姓名。候选: %s", s.Round, strings.Join(wolves, "、"), strings.Join(targets, "、"))
	for _, w := range wolves {
		p[w] = msg
	}
	return p
}

func (s *State) seerPrompts() map[string]string {
	seer := s.findPlayerByRole(RoleSeer)
	if seer == nil || !seer.Alive {
		return nil
	}
	hist := ""
	if s.SeerCheck != nil {
		if s.SeerCheck.IsWolf {
			hist = fmt.Sprintf("上次神谕显示%s带有月痕。", s.SeerCheck.Target)
		} else {
			hist = fmt.Sprintf("上次神谕显示%s没有月痕。", s.SeerCheck.Target)
		}
	}
	targets := s.otherAlivePlayers(seer.Name)
	return map[string]string{seer.Name: fmt.Sprintf("【第%d夜·烛火神谕】现在是读烛火者的私密查验环节，只有你独自行动。你的真实夜晚身份: 读烛火神谕的人。你本环节要从其他存活者中选择一名查验目标。请明确说出一个完整候选姓名，不要写自己的名字。候选: %s。%s", s.Round, strings.Join(targets, "、"), hist)}
}

func (s *State) witchPrompts() map[string]string {
	witch := s.findPlayerByRole(RoleWitch)
	if witch == nil || !witch.Alive {
		return nil
	}
	vt := ""
	if s.Victim != "" {
		vt = fmt.Sprintf(" 你看见%s正被黑雾拖走。", s.Victim)
	} else {
		vt = " 今夜没有人被黑雾拖走。"
	}
	targets := s.otherAlivePlayers(witch.Name)
	return map[string]string{witch.Name: fmt.Sprintf("【第%d夜·药瓶低语】现在是掌药瓶者的私密抉择环节，只有你独自行动。你的真实夜晚身份: 掌药瓶的人。当前夜晚信息:%s 复苏药: %s；凋零药: %s。本环节可救下黑雾目标、放弃救人，或用凋零药指定一名其他存活者。凋零候选: %s", s.Round, vt, yn(s.WitchHasSave), yn(s.WitchHasPoison), strings.Join(targets, "、"))}
}

func (s *State) discussPrompts() map[string]string {
	p := make(map[string]string)
	for _, name := range s.AlivePlayers() {
		p[name] = fmt.Sprintf("【第%d日·雾镇议事】你是%s。结合昨夜死讯、谁在改口、谁在推卸、谁在急着指认，给出怀疑对象和理由。不要使用桌游黑话。", s.Round, name)
	}
	return p
}

func (s *State) votePrompts() map[string]string {
	p := make(map[string]string)
	alive := s.AlivePlayers()
	for _, name := range alive {
		others := make([]string, 0)
		for _, n := range alive {
			if n != name {
				others = append(others, n)
			}
		}
		p[name] = fmt.Sprintf("【审判钟】你是%s。必须指认一名存活者接受审判，必须明确说出一个人名。候选: %s", name, strings.Join(others, "、"))
	}
	return p
}

func (s *State) ApplyNightResults() []string {
	s.Killed = []string{}
	if s.Victim != "" && !s.WitchSave {
		s.addKilled(s.Victim)
		s.killPlayer(s.Victim)
	}
	if s.WitchPoison != "" {
		s.addKilled(s.WitchPoison)
		s.killPlayer(s.WitchPoison)
	}
	return append([]string{}, s.Killed...)
}

func (s *State) ApplyVoteResult() (string, int) {
	lynched, _ := s.VoteResult()
	if lynched != "" {
		s.addKilled(lynched)
		s.killPlayer(lynched)
	}
	votes := 0
	if lynched != "" {
		for _, target := range s.Votes {
			if target == lynched {
				votes++
			}
		}
	}
	return lynched, votes
}

func (s *State) killPlayer(name string) {
	for i := range s.Players {
		if s.Players[i].Name == name {
			s.Players[i].Alive = false
		}
	}
}

func (s *State) addKilled(name string) {
	if !contains(s.AllKilled, name) {
		s.AllKilled = append(s.AllKilled, name)
	}
	if !contains(s.Killed, name) {
		s.Killed = append(s.Killed, name)
	}
}

func (s *State) ClearNightState() {
	s.Victim = ""
	s.Killed = []string{}
	s.SeerCheck = nil
	s.WitchSave = false
	s.WitchPoison = ""
}

func (s *State) ClearVotes() {
	s.Votes = make(map[string]string)
}

func (s *State) SetPhase(phase Phase) {
	s.Phase = phase
}

func (s *State) AdvanceRound() {
	s.Round++
}

func (s *State) CheckWin() bool {
	return s.checkWin()
}

func (s *State) AliveRoleHolder(role Role) string {
	if p := s.findPlayerByRole(role); p != nil {
		return p.Name
	}
	return ""
}

func (s *State) PlayerAlive(name string) bool {
	return contains(s.AlivePlayers(), name)
}

func (s *State) OtherAlivePlayers(name string) []string {
	return s.otherAlivePlayers(name)
}

func (s *State) NonWolfAlivePlayers() []string {
	return s.nonWolfAlivePlayers()
}

func (s *State) checkWin() bool {
	wolves, villagers := 0, 0
	for _, p := range s.Players {
		if p.Alive {
			if p.Team == "werewolf" {
				wolves++
			} else {
				villagers++
			}
		}
	}
	if wolves == 0 {
		s.Winner = "villager"
		return true
	}
	if wolves >= villagers {
		s.Winner = "werewolf"
		return true
	}
	return false
}

func (s *State) findPlayer(name string) *Player {
	for i := range s.Players {
		if s.Players[i].Name == name {
			return &s.Players[i]
		}
	}
	return nil
}

func (s *State) findPlayerByRole(role Role) *Player {
	for i := range s.Players {
		if s.Players[i].Role == role && s.Players[i].Alive {
			return &s.Players[i]
		}
	}
	return nil
}

func (s *State) refreshTeams() {
	for i := range s.Players {
		if s.Players[i].Role == RoleWerewolf {
			s.Players[i].Team = "werewolf"
		} else {
			s.Players[i].Team = "villager"
		}
	}
}

func validRole(role Role) bool {
	switch role {
	case RoleWerewolf, RoleSeer, RoleWitch, RoleHunter, RoleVillager:
		return true
	default:
		return false
	}
}

func (s *State) otherAlivePlayers(name string) []string {
	var n []string
	for _, p := range s.Players {
		if p.Alive && p.Name != name {
			n = append(n, p.Name)
		}
	}
	sort.Strings(n)
	return n
}

func (s *State) nonWolfAlivePlayers() []string {
	var n []string
	for _, p := range s.Players {
		if p.Alive && p.Role != RoleWerewolf {
			n = append(n, p.Name)
		}
	}
	sort.Strings(n)
	return n
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func yn(b bool) string {
	if b {
		return "可用"
	}
	return "不可用"
}
