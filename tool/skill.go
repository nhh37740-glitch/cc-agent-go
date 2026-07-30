package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SkillTool 读取本次项目目录中的 .cc-agent/skills 文件。
type SkillTool struct{}

func NewSkillTool() *SkillTool {
	return &SkillTool{}
}

func (skillTool *SkillTool) Name() string {
	return "activate_skill"
}

func (skillTool *SkillTool) Description() string {
	return "读取本次项目目录下 .cc-agent/skills/<skill>.md 的技能指令。参数 skill 是文件名（不含 .md）。"
}

func (skillTool *SkillTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"skill": map[string]any{
				"type":        "string",
				"description": "要激活的技能名称（文件名不含 .md）",
			},
		},
		"required": []string{"skill"},
	}
}

func (skillTool *SkillTool) Execute(
	toolArguments map[string]any,
	executionEnvironment ToolExecutionEnvironment,
) (string, error) {
	skillName, skillNameIsString := toolArguments["skill"].(string)
	if !skillNameIsString || skillName == "" {
		return "", fmt.Errorf("skill 参数缺失或不是字符串")
	}
	if strings.ContainsAny(skillName, `/\`) || skillName == "." || skillName == ".." {
		return "", fmt.Errorf("skill 名称不能包含路径")
	}

	skillsDirectory := filepath.Join(
		executionEnvironment.WorkingDirectory,
		".cc-agent",
		"skills",
	)
	skillFilePath := filepath.Join(skillsDirectory, skillName+".md")
	skillFileContent, readSkillFileError := os.ReadFile(skillFilePath)
	if readSkillFileError != nil {
		if os.IsNotExist(readSkillFileError) {
			return "", fmt.Errorf(
				"未知 skill: %s（可用: %s）",
				skillName,
				listSkillNames(skillsDirectory),
			)
		}
		return "", fmt.Errorf("读取 skill 文件失败: %w", readSkillFileError)
	}

	_, skillInstruction := parseFrontmatter(string(skillFileContent))
	return skillInstruction, nil
}

func listSkillNames(skillsDirectory string) string {
	skillsDirectoryEntries, readSkillsDirectoryError := os.ReadDir(skillsDirectory)
	if readSkillsDirectoryError != nil {
		return ""
	}
	skillNames := make([]string, 0, len(skillsDirectoryEntries))
	for _, skillsDirectoryEntry := range skillsDirectoryEntries {
		fileName := skillsDirectoryEntry.Name()
		if skillsDirectoryEntry.IsDir() ||
			!strings.HasSuffix(fileName, ".md") ||
			strings.HasPrefix(fileName, "_") {
			continue
		}
		skillNames = append(skillNames, strings.TrimSuffix(fileName, ".md"))
	}
	return strings.Join(skillNames, ", ")
}

func parseFrontmatter(content string) (map[string]string, string) {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return nil, content
	}

	remainingContent := content[3:]
	if len(remainingContent) > 0 && remainingContent[0] == '\n' {
		remainingContent = remainingContent[1:]
	}
	frontmatterEnd := strings.Index(remainingContent, "\n---")
	if frontmatterEnd == -1 {
		return nil, strings.TrimSpace(remainingContent)
	}

	frontmatterText := remainingContent[:frontmatterEnd]
	instructionBody := strings.TrimSpace(remainingContent[frontmatterEnd+4:])
	frontmatterFields := make(map[string]string)
	for _, frontmatterLine := range strings.Split(frontmatterText, "\n") {
		frontmatterLine = strings.TrimSpace(frontmatterLine)
		fieldSeparatorIndex := strings.Index(frontmatterLine, ":")
		if frontmatterLine == "" || fieldSeparatorIndex == -1 {
			continue
		}
		frontmatterFields[strings.TrimSpace(frontmatterLine[:fieldSeparatorIndex])] =
			strings.TrimSpace(frontmatterLine[fieldSeparatorIndex+1:])
	}
	return frontmatterFields, instructionBody
}
