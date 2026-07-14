package main

import (
	"testing"

	"cc-agent-go/democode/v11/model"
)

func TestWitchSaveParsingRequiresExplicitAction(t *testing.T) {
	cases := []string{
		"黑雾来了，谁也救不了被盯上的人。",
		"我不能救，这药瓶今晚不该打开。",
		"她总说救人，可这不像真正的祈祷。",
	}
	for _, text := range cases {
		if wantsSave(text) {
			t.Fatalf("expected no save action for %q", text)
		}
	}

	explicit := []string{
		"我救，倒出复苏药。",
		"决定救下他。",
		"用解药，把人从黑雾里拉回来。",
		"拯救 修女伊芙",
		"救。\n（在胸前画十字）主说过，黑雾拽走的未必都是该被丢弃的羊。",
	}
	for _, text := range explicit {
		if !wantsSave(text) {
			t.Fatalf("expected save action for %q", text)
		}
	}
}

func TestWitchPoisonParsingRequiresExplicitAction(t *testing.T) {
	if wantsPoison("这话很毒，但我不用凋零药。") {
		t.Fatal("expected no poison action")
	}
	if !wantsPoison("我毒 钟匠赫尔。") {
		t.Fatal("expected poison action")
	}
}

func TestParsedActionValidation(t *testing.T) {
	candidates := []string{"钟匠赫尔", "药师薇拉"}
	if !validParsedAction(modelAction("kill", "钟匠赫尔"), "kill", candidates) {
		t.Fatal("expected valid kill action")
	}
	if validParsedAction(modelAction("vote", "钟匠赫尔"), "kill", candidates) {
		t.Fatal("expected vote to be rejected during kill parsing")
	}
	if validParsedAction(modelAction("kill", "不存在的人"), "kill", candidates) {
		t.Fatal("expected target outside candidates to be rejected")
	}
	if !validParsedAction(modelAction("save", ""), "witch", candidates) {
		t.Fatal("expected save to be valid during witch parsing")
	}
}

func TestSeerTargetLocalParsingAcceptsFullNameAndSeat(t *testing.T) {
	candidates := []string{"钟匠赫尔", "药师薇拉", "修女伊芙", "铁匠巴伦"}
	cases := map[string]string{
		"查验：修女伊芙":     "修女伊芙",
		"我今晚看6号。":     "修女伊芙",
		"验第四位，炉火那个人。": "铁匠巴伦",
		"查验铁匠巴伦。":     "铁匠巴伦",
	}

	for text, want := range cases {
		got := parseActionLocal("旅人", text, "night_seer", "check", candidates)
		if got.Action != "check" || got.Target != want {
			t.Fatalf("parseActionLocal(%q) = action=%s target=%s, want check %s", text, got.Action, got.Target, want)
		}
	}
}

func TestSeerTargetLocalParsingDoesNotGuessAliases(t *testing.T) {
	candidates := []string{"钟匠赫尔", "药师薇拉", "修女伊芙", "铁匠巴伦"}
	got := parseActionLocal("旅人", "让烛火凝视伊芙。", "night_seer", "check", candidates)
	if got.Action != "none" || got.Target != "" {
		t.Fatalf("expected alias-only local parse to stay none, got action=%s target=%s", got.Action, got.Target)
	}
}

func TestParseTemperature(t *testing.T) {
	if got := parseTemperature("1.2", 0.8); got != 1.2 {
		t.Fatalf("expected 1.2, got %v", got)
	}
	if got := parseTemperature("-1", 0.8); got != 0 {
		t.Fatalf("expected clamp to 0, got %v", got)
	}
	if got := parseTemperature("2", 0.8); got != 1.5 {
		t.Fatalf("expected clamp to 1.5, got %v", got)
	}
	if got := parseTemperature("bad", 0.8); got != 0.8 {
		t.Fatalf("expected fallback, got %v", got)
	}
}

func TestParseDayRounds(t *testing.T) {
	if got := parseDayRounds(""); got != 3 {
		t.Fatalf("expected default 3, got %d", got)
	}
	if got := parseDayRounds("0"); got != 1 {
		t.Fatalf("expected clamp to 1, got %d", got)
	}
	if got := parseDayRounds("9"); got != 5 {
		t.Fatalf("expected clamp to 5, got %d", got)
	}
	if got := parseDayRounds("4"); got != 4 {
		t.Fatalf("expected 4, got %d", got)
	}
}

func modelAction(action, target string) model.GameAction {
	return model.GameAction{Action: action, Target: target}
}
