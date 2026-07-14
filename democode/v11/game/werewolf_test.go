package game

import (
	"strings"
	"testing"
)

func testState(t *testing.T) *State {
	t.Helper()
	s, err := NewWerewolf([]string{"A", "B", "C", "D", "E", "F", "G", "H"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func nameByRole(t *testing.T, s *State, role Role) string {
	t.Helper()
	for _, p := range s.Players {
		if p.Role == role {
			return p.Name
		}
	}
	t.Fatalf("role not found: %s", role)
	return ""
}

func namesNot(t *testing.T, s *State, excluded ...string) []string {
	t.Helper()
	blocked := map[string]bool{}
	for _, name := range excluded {
		blocked[name] = true
	}
	var out []string
	for _, p := range s.Players {
		if p.Alive && !blocked[p.Name] {
			out = append(out, p.Name)
		}
	}
	if len(out) == 0 {
		t.Fatal("no candidate names")
	}
	return out
}

func namesNotRole(t *testing.T, s *State, role Role, excluded ...string) []string {
	t.Helper()
	blocked := map[string]bool{}
	for _, name := range excluded {
		blocked[name] = true
	}
	var out []string
	for _, p := range s.Players {
		if p.Alive && p.Role != role && !blocked[p.Name] {
			out = append(out, p.Name)
		}
	}
	if len(out) == 0 {
		t.Fatalf("no candidate names outside role %s", role)
	}
	return out
}

func TestWitchSaveHappensBeforeNightDeaths(t *testing.T) {
	s := testState(t)
	witch := nameByRole(t, s, RoleWitch)
	target := namesNotRole(t, s, RoleWerewolf, witch)[0]

	if err := s.RecordWolfKill(target); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordWitchAction(true, ""); err != nil {
		t.Fatal(err)
	}
	killed := s.ApplyNightResults()
	if len(killed) != 0 {
		t.Fatalf("expected saved night with no deaths, got %v", killed)
	}
	if !s.PlayerAlive(target) {
		t.Fatalf("expected %s to remain alive after save", target)
	}
	announcement := s.DayAnnouncement()
	if !strings.Contains(announcement, "被药瓶挡回") || !strings.Contains(announcement, target) {
		t.Fatalf("expected save reason in day announcement, got %q", announcement)
	}
}

func TestWitchPoisonKillsAtNightResolution(t *testing.T) {
	s := testState(t)
	witch := nameByRole(t, s, RoleWitch)
	target := namesNot(t, s, witch)[0]

	if err := s.RecordWitchAction(false, target); err != nil {
		t.Fatal(err)
	}
	killed := s.ApplyNightResults()
	if len(killed) != 1 || killed[0] != target {
		t.Fatalf("expected poison death %s, got %v", target, killed)
	}
	if s.PlayerAlive(target) {
		t.Fatalf("expected %s to be dead", target)
	}
}

func TestHunterCanShootAfterBeingLynched(t *testing.T) {
	s := testState(t)
	hunter := nameByRole(t, s, RoleHunter)
	for _, voter := range namesNot(t, s, hunter)[:3] {
		if err := s.RecordVote(voter, hunter); err != nil {
			t.Fatal(err)
		}
	}
	lynched, _ := s.ApplyVoteResult()
	if lynched != hunter {
		t.Fatalf("expected hunter lynched, got %s", lynched)
	}
	if s.PlayerAlive(hunter) {
		t.Fatalf("expected hunter dead after lynch")
	}
	target := namesNot(t, s, hunter)[0]
	if err := s.RecordHunterShoot(hunter, target); err != nil {
		t.Fatal(err)
	}
	if s.PlayerAlive(target) {
		t.Fatalf("expected silver bullet target dead")
	}
}

func TestVotesArePerVoter(t *testing.T) {
	s := testState(t)
	voter := s.AlivePlayers()[0]
	target := s.AlivePlayers()[1]
	if err := s.RecordVote(voter, target); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordVote(voter, target); err == nil {
		t.Fatal("expected duplicate vote to be rejected")
	}
}

func TestWolvesCannotTargetWolfPartner(t *testing.T) {
	s := testState(t)
	wolves := s.WolfGroup()
	if len(wolves) < 2 {
		t.Fatal("expected two wolves")
	}
	if err := s.RecordWolfKill(wolves[1]); err == nil {
		t.Fatal("expected wolf target to be rejected")
	}
}
