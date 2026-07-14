package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"cc-agent-go/democode/v12/model"
)

type Library struct {
	Root string
}

func NewLibrary(root string) *Library {
	return &Library{Root: root}
}

func (l *Library) ListScripts() ([]model.ScriptSummary, error) {
	entries, err := os.ReadDir(l.Root)
	if err != nil {
		return nil, fmt.Errorf("读取剧本目录失败: %w", err)
	}
	var scripts []model.ScriptSummary
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		script, err := l.LoadScript(entry.Name())
		if err != nil {
			return nil, err
		}
		scripts = append(scripts, Summary(script))
	}
	sort.Slice(scripts, func(i, j int) bool { return scripts[i].ID < scripts[j].ID })
	return scripts, nil
}

func (l *Library) LoadScript(id string) (*model.Script, error) {
	if id == "" || strings.Contains(id, "..") || filepath.Base(id) != id {
		return nil, fmt.Errorf("无效剧本ID: %s", id)
	}
	manifestPath := filepath.Join(l.Root, id, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("读取 manifest 失败: %w", err)
	}
	var script model.Script
	if err := json.Unmarshal(data, &script); err != nil {
		return nil, fmt.Errorf("解析 manifest 失败: %w", err)
	}
	if err := validateScript(&script); err != nil {
		return nil, err
	}
	manifestDir := filepath.Dir(manifestPath)
	boundary := filepath.Dir(l.Root)
	script.ManualPath = resolvePath(boundary, manifestDir, script.ManualPath)
	for i := range script.Roles {
		script.Roles[i].SourcePath = resolvePath(boundary, manifestDir, script.Roles[i].SourcePath)
		script.Roles[i].ScriptFile = resolvePath(boundary, manifestDir, script.Roles[i].ScriptFile)
	}
	for i := range script.Phases {
		for j := range script.Phases[i].Clues {
			script.Phases[i].Clues[j].Path = resolvePath(boundary, manifestDir, script.Phases[i].Clues[j].Path)
		}
	}
	return &script, nil
}

func Summary(script *model.Script) model.ScriptSummary {
	return model.ScriptSummary{
		ID: script.ID, Title: script.Title, Subtitle: script.Subtitle, Locale: script.Locale,
		PlayerCount: script.PlayerCount, Summary: script.Summary, Theme: script.Theme, Roles: script.Roles,
	}
}

func ReadText(path string, maxBytes int) (string, error) {
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if maxBytes > 0 && len(data) > maxBytes {
		data = data[:maxBytes]
	}
	return strings.TrimSpace(string(data)), nil
}

func validateScript(script *model.Script) error {
	if script.ID == "" {
		return fmt.Errorf("manifest 缺少 id")
	}
	if script.Title == "" {
		return fmt.Errorf("manifest 缺少 title")
	}
	if script.PlayerCount < 1 {
		return fmt.Errorf("manifest playerCount 必须大于0")
	}
	if len(script.Roles) == 0 {
		return fmt.Errorf("manifest 缺少 roles")
	}
	if len(script.Phases) == 0 {
		return fmt.Errorf("manifest 缺少 phases")
	}
	roleIDs := map[string]bool{}
	for _, role := range script.Roles {
		if role.ID == "" || role.Name == "" {
			return fmt.Errorf("role 缺少 id 或 name")
		}
		if roleIDs[role.ID] {
			return fmt.Errorf("role id 重复: %s", role.ID)
		}
		roleIDs[role.ID] = true
	}
	for _, phase := range script.Phases {
		if phase.ID == "" || phase.Title == "" {
			return fmt.Errorf("phase 缺少 id 或 title")
		}
		for _, clue := range phase.Clues {
			if clue.ID == "" || clue.Title == "" {
				return fmt.Errorf("clue 缺少 id 或 title")
			}
			if clue.Visibility == "role" || clue.Visibility == "private" {
				if len(clue.RoleIDs) == 0 {
					return fmt.Errorf("私有线索 %s 缺少 roleIds", clue.ID)
				}
				for _, roleID := range clue.RoleIDs {
					if !roleIDs[roleID] {
						return fmt.Errorf("线索 %s 引用了未知角色 %s", clue.ID, roleID)
					}
				}
			}
		}
	}
	return nil
}

func resolvePath(boundary, base, rel string) string {
	if rel == "" {
		return ""
	}
	if filepath.IsAbs(rel) {
		return filepath.Clean(rel)
	}
	clean := filepath.Clean(filepath.Join(base, rel))
	absBoundary, berr := filepath.Abs(boundary)
	absClean, cerr := filepath.Abs(clean)
	if berr == nil && cerr == nil {
		if absClean == absBoundary || strings.HasPrefix(absClean, absBoundary+string(os.PathSeparator)) {
			return absClean
		}
	}
	return clean
}
