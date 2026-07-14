package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ============================================================================
// SkillTool — activate_skill
// ============================================================================

// SkillTool 实现 Tool 接口，让 Agent 按需激活 workspace/skills/ 下的 skill。
type SkillTool struct {
	dir  string            // skills 目录路径
	desc map[string]string // 缓存: skill名(filename去.md) → frontmatter description
}

// NewSkillTool 创建 SkillTool，启动时扫一次目录建立初始缓存。
func NewSkillTool(dir string) *SkillTool {
	t := &SkillTool{dir: dir, desc: make(map[string]string)}
	t.refreshCache()
	return t
}

// Name 返回工具名。
func (t *SkillTool) Name() string {
	return "activate_skill"
}

// Description 返回工具描述。
// 每次 API 调用时 GetDefinitions() 触发。先扫目录发现新文件并读 description 加入缓存。
func (t *SkillTool) Description() string {
	t.refreshCache()

	if len(t.desc) == 0 {
		return "激活一个技能来获得专业领域的专用指令。参数 skill: 技能名称。（目前没有可用 skill，参考 workspace/skills/_template.md 创建）"
	}

	var sb strings.Builder
	sb.WriteString("激活一个技能来获得专业领域的专用指令。参数 skill: 技能名称。可用: ")
	first := true
	for name, d := range t.desc {
		if !first {
			sb.WriteString("; ")
		}
		first = false
		sb.WriteString(name)
		if d != "" {
			sb.WriteString("(" + d + ")")
		}
	}
	return sb.String()
}

// InputSchema 返回参数定义。
func (t *SkillTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"skill": map[string]any{
				"type":        "string",
				"description": "要激活的技能名称（即文件名，不含 .md 后缀）",
			},
		},
		"required": []any{"skill"},
	}
}

// Execute 读取指定 skill 的 .md 文件，跳过 frontmatter，返回 prompt 正文。
// skill 名就是文件名去掉 .md（如 "张雪峰" → workspace/skills/张雪峰.md）。
func (t *SkillTool) Execute(input map[string]any) (string, error) {
	skillName, ok := input["skill"].(string)
	if !ok || skillName == "" {
		return "", fmt.Errorf("skill 参数缺失或不是字符串")
	}

	path := filepath.Join(t.dir, skillName+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("未知 skill: %s（可用: %s）", skillName, t.allNames())
		}
		return "", fmt.Errorf("读取 skill 文件失败: %w", err)
	}

	_, body := parseFrontmatter(string(data))
	return body, nil
}

// ============================================================================
// 缓存刷新
// ============================================================================

// refreshCache 扫目录文件名，缓存中没有的就读 frontmatter description 加入。
// _ 开头的文件（如 _template.md）跳过。
// skill 名 = 文件名去掉 .md，Description 和 Execute 都用这个名匹配。
func (t *SkillTool) refreshCache() {
	entries, err := os.ReadDir(t.dir)
	if err != nil {
		return
	}

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") {
			continue
		}
		if strings.HasPrefix(name, "_") {
			continue
		}

		skillName := strings.TrimSuffix(name, ".md")
		if _, ok := t.desc[skillName]; ok {
			continue // 已在缓存，跳过
		}

		// 新文件，读 frontmatter 拿 description
		data, err := os.ReadFile(filepath.Join(t.dir, name))
		if err != nil {
			continue
		}
		fm, _ := parseFrontmatter(string(data))
		t.desc[skillName] = fm["description"]
	}
}

// allNames 返回所有 skill 名称（错误提示用）。
func (t *SkillTool) allNames() string {
	names := make([]string, 0, len(t.desc))
	for name := range t.desc {
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

// ============================================================================
// frontmatter 解析
// ============================================================================

// parseFrontmatter 解析 .md 文件: frontmatter 元数据 + prompt 正文。
//
// 文件格式:
//
//	---
//	name: 张雪峰
//	description: 高考志愿填报专家
//	---
//	这里是 prompt 正文...
//
// 返回: map[字段名]值, prompt正文
func parseFrontmatter(content string) (map[string]string, string) {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return nil, content
	}

	// 跳过第一个 ---
	rest := content[3:]
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	}

	// 找第二个 ---
	end := strings.Index(rest, "\n---")
	if end == -1 {
		return nil, strings.TrimSpace(rest)
	}

	fmText := rest[:end]
	body := strings.TrimSpace(rest[end+4:])

	meta := make(map[string]string)
	for _, line := range strings.Split(fmText, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		colon := strings.Index(line, ":")
		if colon == -1 {
			continue
		}
		key := strings.TrimSpace(line[:colon])
		val := strings.TrimSpace(line[colon+1:])
		meta[key] = val
	}
	return meta, body
}
