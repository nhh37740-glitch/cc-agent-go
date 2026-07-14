package game

import (
	"testing"

	"cc-agent-go/democode/v12/model"
)

func TestPlayerSeatAndScriptRoleAreDecoupled(t *testing.T) {
	s, err := NewSession(testScript(), Options{PlayerName: "旅行者", PreferredRole: "gu-yan", Language: "ja-JP"})
	if err != nil {
		t.Fatal(err)
	}
	if s.HumanSeat().DisplayName != "旅行者" {
		t.Fatalf("expected player display name to stay separate, got %s", s.HumanSeat().DisplayName)
	}
	if s.HumanRole().Name != "顾言" {
		t.Fatalf("expected script role 顾言, got %s", s.HumanRole().Name)
	}
	if s.HumanSeat().Language != "ja-JP" {
		t.Fatalf("expected language on player seat")
	}
}

func TestEnterCurrentPhaseFiltersPrivateClues(t *testing.T) {
	s, err := NewSession(testScript(), Options{PreferredRole: "gu-yan"})
	if err != nil {
		t.Fatal(err)
	}
	s.PhaseIndex = 1
	frames := s.EnterCurrentPhase()
	seenGuClue := false
	seenSuClue := false
	for _, frame := range frames {
		if frame["type"] != "clue" {
			continue
		}
		clue := frame["clue"].(model.ClueRef)
		if clue.ID == "gu-private" {
			seenGuClue = true
		}
		if clue.ID == "su-private" {
			seenSuClue = true
		}
	}
	if !seenGuClue {
		t.Fatal("expected role-visible clue")
	}
	if seenSuClue {
		t.Fatal("did not expect clue for another role")
	}
}

func TestPhaseAdvanceAndInput(t *testing.T) {
	s, err := NewSession(testScript(), Options{PlayerName: "P", PreferredRole: "gu-yan"})
	if err != nil {
		t.Fatal(err)
	}
	frames := s.EnterCurrentPhase()
	if frames[len(frames)-1]["type"] != "prompt" {
		t.Fatalf("expected input prompt, got %+v", frames)
	}
	replies := s.RecordHumanInput("我想起那张照片。")
	if len(replies) < 2 {
		t.Fatalf("expected human and ai speech frames, got %+v", replies)
	}
	if !s.Advance() {
		t.Fatal("expected advance")
	}
	if phase, _ := s.CurrentPhase(); phase.ID != "clues" {
		t.Fatalf("expected clues phase, got %s", phase.ID)
	}
}

func testScript() *model.Script {
	return &model.Script{
		ID: "farewell-poem", Title: "告别诗", PlayerCount: 2,
		Roles: []model.ScriptRole{
			{ID: "gu-yan", Name: "顾言", PublicIntro: "阳光开朗"},
			{ID: "su-cheng", Name: "苏橙", PublicIntro: "沉默守候"},
		},
		Phases: []model.Phase{
			{ID: "opening", Title: "开场", DMGoal: "让玩家入场。", WaitForInput: true},
			{ID: "clues", Title: "照片线索", DMGoal: "发放线索。", Clues: []model.ClueRef{
				{ID: "public", Title: "公开照片", Visibility: "public"},
				{ID: "gu-private", Title: "顾言照片", Visibility: "role", RoleIDs: []string{"gu-yan"}},
				{ID: "su-private", Title: "苏橙照片", Visibility: "role", RoleIDs: []string{"su-cheng"}},
			}},
		},
	}
}
