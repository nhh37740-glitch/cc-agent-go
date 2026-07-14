package game

import (
	"fmt"
	"strings"

	"cc-agent-go/democode/v12/model"
)

type Session struct {
	ID             string                 `json:"id"`
	Script         *model.Script          `json:"script"`
	PhaseIndex     int                    `json:"phaseIndex"`
	Seats          []model.PlayerSeat     `json:"seats"`
	Transcript     []model.TranscriptItem `json:"transcript"`
	DisclosedClues []model.ClueRef        `json:"disclosedClues"`
	Done           bool                   `json:"done"`
}

type Options struct {
	SessionID     string
	PlayerName    string
	PlayerAvatar  string
	Language      string
	PreferredRole string
}

func NewSession(script *model.Script, opts Options) (*Session, error) {
	if script == nil {
		return nil, fmt.Errorf("script 不能为空")
	}
	if opts.PlayerName == "" {
		opts.PlayerName = "玩家"
	}
	if opts.Language == "" {
		opts.Language = "zh-CN"
	}
	roleID := opts.PreferredRole
	if roleID == "" && len(script.Roles) > 0 {
		roleID = script.Roles[0].ID
	}
	if _, ok := roleByID(script, roleID); !ok {
		return nil, fmt.Errorf("未知角色: %s", roleID)
	}
	seats := []model.PlayerSeat{{
		ID: "human", DisplayName: opts.PlayerName, Avatar: opts.PlayerAvatar,
		Language: opts.Language, Human: true, RoleID: roleID,
	}}
	ai := 1
	for _, role := range script.Roles {
		if role.ID == roleID {
			continue
		}
		seats = append(seats, model.PlayerSeat{
			ID: fmt.Sprintf("ai-%d", ai), DisplayName: role.Name, Avatar: role.Avatar,
			Language: "zh-CN", Human: false, RoleID: role.ID,
		})
		ai++
	}
	id := opts.SessionID
	if id == "" {
		id = "local"
	}
	return &Session{ID: id, Script: script, Seats: seats}, nil
}

func (s *Session) CurrentPhase() (model.Phase, bool) {
	if s == nil || s.Script == nil || s.PhaseIndex < 0 || s.PhaseIndex >= len(s.Script.Phases) {
		return model.Phase{}, false
	}
	return s.Script.Phases[s.PhaseIndex], true
}

func (s *Session) HumanSeat() model.PlayerSeat {
	for _, seat := range s.Seats {
		if seat.Human {
			return seat
		}
	}
	return model.PlayerSeat{}
}

func (s *Session) HumanRole() model.ScriptRole {
	seat := s.HumanSeat()
	role, _ := roleByID(s.Script, seat.RoleID)
	return role
}

func (s *Session) EnterCurrentPhase() []map[string]any {
	phase, ok := s.CurrentPhase()
	if !ok {
		s.Done = true
		return []map[string]any{{"type": "done"}}
	}
	frames := []map[string]any{{
		"type": "phase", "phase": phase.ID, "title": phase.Title,
		"index": s.PhaseIndex + 1, "total": len(s.Script.Phases),
	}}
	frames = append(frames, map[string]any{
		"type": "dm", "from": "DM", "text": s.dmText(phase),
	})
	for _, clue := range phase.Clues {
		if clue.Visibility == "public" || clueVisibleToRole(clue, s.HumanSeat().RoleID) {
			s.DisclosedClues = appendClueOnce(s.DisclosedClues, clue)
			frame := map[string]any{"type": "clue", "clue": clue}
			if clue.Visibility != "public" {
				frame["private"] = true
			}
			frames = append(frames, frame)
		}
	}
	if text := strings.TrimSpace(phase.PrivateText[s.HumanSeat().RoleID]); text != "" {
		frames = append(frames, map[string]any{
			"type": "script", "roleId": s.HumanSeat().RoleID, "private": true,
			"title": phase.Title, "text": text,
		})
	}
	if phase.WaitForInput {
		frames = append(frames, map[string]any{
			"type": "prompt", "phase": phase.ID, "message": "请以当前角色身份发言。",
		})
	}
	return frames
}

func (s *Session) RecordHumanInput(text string) []map[string]any {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	seat := s.HumanSeat()
	role, _ := roleByID(s.Script, seat.RoleID)
	item := model.TranscriptItem{From: seat.DisplayName, RoleID: seat.RoleID, Text: text}
	s.Transcript = append(s.Transcript, item)
	frames := []map[string]any{{
		"type": "speech", "from": seat.DisplayName, "role": role.Name, "roleId": seat.RoleID, "text": text,
	}}
	phase, ok := s.CurrentPhase()
	if ok {
		for _, ai := range s.Seats {
			if ai.Human {
				continue
			}
			role, _ := roleByID(s.Script, ai.RoleID)
			reply := s.aiReply(role, phase, text)
			s.Transcript = append(s.Transcript, model.TranscriptItem{From: ai.DisplayName, RoleID: ai.RoleID, Text: reply})
			frames = append(frames, map[string]any{"type": "speech", "from": ai.DisplayName, "role": role.Name, "roleId": role.ID, "text": reply})
		}
	}
	return frames
}

func (s *Session) Advance() bool {
	if s.PhaseIndex+1 >= len(s.Script.Phases) {
		s.Done = true
		return false
	}
	s.PhaseIndex++
	return true
}

func (s *Session) PublicState() map[string]any {
	return map[string]any{
		"type": "state", "sessionId": s.ID, "scriptId": s.Script.ID,
		"phaseIndex": s.PhaseIndex, "phaseTotal": len(s.Script.Phases),
		"players": s.Seats, "clueCount": len(s.DisclosedClues),
	}
}

func (s *Session) RevealFrame() map[string]any {
	roles := make([]map[string]string, 0, len(s.Seats))
	for _, seat := range s.Seats {
		role, _ := roleByID(s.Script, seat.RoleID)
		roles = append(roles, map[string]string{"player": seat.DisplayName, "roleId": role.ID, "role": role.Name})
	}
	return map[string]any{"type": "reveal", "roles": roles}
}

func (s *Session) dmText(phase model.Phase) string {
	if phase.DMGoal != "" {
		return fmt.Sprintf("【%s】%s", phase.Title, phase.DMGoal)
	}
	return "【" + phase.Title + "】请继续推进。"
}

func (s *Session) aiReply(role model.ScriptRole, phase model.Phase, humanText string) string {
	private := strings.TrimSpace(phase.PrivateText[role.ID])
	if private != "" {
		return fmt.Sprintf("%s沉默片刻，接住刚才的话，把记忆落在「%s」。", role.Name, truncate(private, 32))
	}
	return fmt.Sprintf("%s点点头，回应刚才的发言，并把话题带回%s。", role.Name, phase.Title)
}

func roleByID(script *model.Script, id string) (model.ScriptRole, bool) {
	for _, role := range script.Roles {
		if role.ID == id {
			return role, true
		}
	}
	return model.ScriptRole{}, false
}

func clueVisibleToRole(clue model.ClueRef, roleID string) bool {
	for _, id := range clue.RoleIDs {
		if id == roleID {
			return true
		}
	}
	return false
}

func appendClueOnce(clues []model.ClueRef, clue model.ClueRef) []model.ClueRef {
	for _, existing := range clues {
		if existing.ID == clue.ID {
			return clues
		}
	}
	return append(clues, clue)
}

func truncate(text string, max int) string {
	r := []rune(strings.TrimSpace(text))
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max]) + "..."
}
