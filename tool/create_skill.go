package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CreateSkillTool 实现 Tool 接口，让 Agent 能在 workspace/skills/ 下创建新的 skill 文件。
// 和 SkillTool 完全独立，唯一共享的是同一个目录路径。
type CreateSkillTool struct {
}

// NewCreateSkillTool 创建 CreateSkillTool。
func NewCreateSkillTool() *CreateSkillTool {
	return &CreateSkillTool{}
}

// Name 返回工具名。
func (t *CreateSkillTool) Name() string {
	return "create_skill"
}

// Description 返回工具描述，告诉模型如何创建 skill。
func (t *CreateSkillTool) Description() string {
	return `创建一个新的技能文件（.md）到本次项目目录的 .cc-agent/skills/ 目录。创建后 activate_skill 工具即可激活使用。

参数说明:
- name: 技能名称，如 "张雪峰"、"代码审查"。会用作文件名（自动加 .md 后缀）
- description: 一句话描述，如 "高考志愿填报专家"。模型看到此描述决定何时激活该技能
- prompt: 技能的专用提示词正文，激活后注入对话。写清楚角色、规则、输出格式

`
}

// InputSchema 返回参数定义。
func (t *CreateSkillTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "技能名称，如 代码审查、张雪峰",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "一句话描述技能用途",
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "技能的专用提示词正文",
			},
		},
		"required": []any{"name", "prompt"},
	}
}

// Execute 创建 skill 的 .md 文件。
func (t *CreateSkillTool) Execute(
	input map[string]any,
	executionEnvironment ToolExecutionEnvironment,
) (string, error) {
	name, _ := input["name"].(string)
	desc, _ := input["description"].(string)
	prompt, _ := input["prompt"].(string)

	if name == "" || prompt == "" {
		return "", fmt.Errorf("name 和 prompt 不能为空")
	}
	if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return "", fmt.Errorf("name 不能包含路径")
	}

	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("name: " + name + "\n")
	if desc != "" {
		sb.WriteString("description: " + desc + "\n")
	}
	sb.WriteString("---\n\n")
	sb.WriteString(prompt)

	skillsDirectory := filepath.Join(
		executionEnvironment.WorkingDirectory,
		".cc-agent",
		"skills",
	)
	if createSkillsDirectoryError := os.MkdirAll(skillsDirectory, 0755); createSkillsDirectoryError != nil {
		return "", fmt.Errorf("创建 skills 目录失败: %w", createSkillsDirectoryError)
	}
	path := filepath.Join(skillsDirectory, name+".md")
	if err := os.WriteFile(path, []byte(sb.String()), 0644); err != nil {
		return "", fmt.Errorf("创建 skill 文件失败: %w", err)
	}

	return fmt.Sprintf("skill %q 创建成功，可通过 activate_skill 激活使用。", name), nil
}
