package service

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadScriptManifest(t *testing.T) {
	root := writeTestScript(t)
	lib := NewLibrary(root)
	script, err := lib.LoadScript("demo")
	if err != nil {
		t.Fatal(err)
	}
	if script.ID != "demo" || len(script.Roles) != 2 || len(script.Phases) != 2 {
		t.Fatalf("unexpected script: %+v", script)
	}
	if script.Phases[1].Clues[0].Path == "clue.txt" {
		t.Fatalf("expected clue path to be resolved")
	}
}

func TestLoadScriptRejectsMissingFields(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "bad"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad", "manifest.json"), []byte(`{"id":"bad"}`), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := NewLibrary(dir).LoadScript("bad")
	if err == nil {
		t.Fatal("expected missing fields error")
	}
}

func TestListScripts(t *testing.T) {
	root := writeTestScript(t)
	items, err := NewLibrary(root).ListScripts()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "demo" || len(items[0].Roles) != 2 {
		t.Fatalf("unexpected summaries: %+v", items)
	}
}

func writeTestScript(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	scriptDir := filepath.Join(dir, "demo")
	if err := os.MkdirAll(scriptDir, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "id":"demo",
  "title":"Demo",
  "playerCount":2,
  "roles":[
    {"id":"a","name":"A","publicIntro":"A intro"},
    {"id":"b","name":"B","publicIntro":"B intro"}
  ],
  "phases":[
    {"id":"p1","title":"P1","dmGoal":"start","waitForInput":true},
    {"id":"p2","title":"P2","dmGoal":"next","clues":[{"id":"c1","title":"C1","path":"clue.txt","visibility":"role","roleIds":["a"]}]}
  ]
}`
	if err := os.WriteFile(filepath.Join(scriptDir, "manifest.json"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scriptDir, "clue.txt"), []byte("clue"), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}
