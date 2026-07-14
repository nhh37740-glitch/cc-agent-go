package service

import (
	"fmt"
	"strings"

	"cc-agent-go/democode/v12/model"
)

func BuildDMPrompt(script *model.Script, phase model.Phase, publicClues []model.ClueRef, transcript []model.TranscriptItem) string {
	var lines []string
	lines = append(lines, "你是剧本杀DM，只主持当前阶段，不泄露未来幕信息。")
	lines = append(lines, fmt.Sprintf("剧本: %s", script.Title))
	lines = append(lines, fmt.Sprintf("当前阶段: %s", phase.Title))
	lines = append(lines, fmt.Sprintf("主持任务: %s", phase.DMGoal))
	if len(publicClues) > 0 {
		lines = append(lines, "已公开线索:")
		for _, clue := range publicClues {
			lines = append(lines, "- "+clue.Title)
		}
	}
	if len(transcript) > 0 {
		lines = append(lines, "可见发言摘要:")
		for _, item := range transcript {
			lines = append(lines, fmt.Sprintf("- %s: %s", item.From, truncate(item.Text, 120)))
		}
	}
	return strings.Join(lines, "\n")
}

func BuildRolePrompt(script *model.Script, role model.ScriptRole, seat model.PlayerSeat, phase model.Phase, privateText string) string {
	return fmt.Sprintf(`你正在参与情感沉浸剧本杀《%s》。

玩家显示身份:
- 名字: %s
- 语言: %s

剧本角色身份:
- 角色: %s
- 公开设定: %s

当前阶段:
- %s
- %s

私有剧本片段:
%s

要求:
- 玩家显示身份和剧本角色身份是两层，不要混淆。
- 只使用当前阶段已经可见的信息。
- 用自然、短句、沉浸式发言。`, script.Title, seat.DisplayName, seat.Language, role.Name, role.PublicIntro, phase.Title, phase.DMGoal, privateText)
}

func truncate(text string, n int) string {
	r := []rune(strings.TrimSpace(text))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "..."
}
