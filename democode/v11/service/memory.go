package service

import (
	"fmt"

	"cc-agent-go/democode/v11/game"
	"cc-agent-go/democode/v11/model"
)

func FilterMessages(messages []RoomMessage, agentName string) []RoomMessage {
	var v []RoomMessage
	for _, msg := range messages {
		if len(msg.VisibleTo) == 0 {
			v = append(v, msg)
		} else {
			for _, vt := range msg.VisibleTo {
				if vt == agentName {
					v = append(v, msg)
					break
				}
			}
		}
	}
	return v
}

func BuildSystemPrompt(personality, roleInfo, phaseInstruction, languageInstruction string) string {
	return BuildWerewolfSystemPrompt("", personality, roleInfo, phaseInstruction, languageInstruction)
}

func BuildWerewolfSystemPrompt(characterStyle, identityPersonality, roleInfo, phaseInstruction, languageInstruction string) string {
	if languageInstruction == "" {
		languageInstruction = "使用简体中文回复。"
	}
	if characterStyle == "" {
		characterStyle = "使用雾镇入席者的公开职业和性格说话。"
	}
	return fmt.Sprintf("## 公开人物背景与表达风格（高优先级）\n%s\n\n## 本局真实身份人格（只决定暗中目标和能力包装）\n%s\n\n## 输出语言\n%s\n\n## 隐式裁判规则（只用于你做决策，绝不能照着念）\n%s\n\n## 雾镇世界背景（只用于氛围和人设，不决定真实身份）\n%s\n\n## 你的本局暗中事实\n%s\n\n## 当前情景\n%s\n\n%s\n\n## 发言风格执行要求\n- 每次发言必须优先体现“公开人物背景与表达风格”中的职业、意象、口吻和关系。\n- 真实身份只能影响你暗中想达成什么，公开表达必须包装成人物背景中的观察、欲望、恐惧或习惯。\n- 如果公开人物背景和真实身份人格冲突，白天公开发言优先服从公开人物背景。\n- 夜晚私密行动阶段例外：当前情景和“你的本局暗中事实”优先于公开人物背景；你必须承认并执行自己的真实夜晚能力，不能因为公开职业而否认自己正在行动。\n\n## 发言节奏\n- 像一个正在连续听别人说话的人，先承接最近一条可见发言，再给出自己的判断或行动。\n- 不要每次都重新介绍自己，不要重复同一套口头禅。\n- 括号动作最多一句，且不能替代观点。\n- 有私有记忆时必须使用它，但白天要包装成观察、直觉、旧事和矛盾，避免直接暴露暗中事实。\n用自然语言回复，限制在100字以内。", characterStyle, identityPersonality, languageInstruction, werewolfRules(), villageContext(), roleInfo, phaseInstruction, immersionRules())
}

func BuildGameRoleInfo(state *game.State, agentName string) string {
	p := findPlayer(state, agentName)
	if p == nil {
		return fmt.Sprintf("你是%s。", agentName)
	}
	return fmt.Sprintf("你的昵称是%s。%s", agentName, state.RoleDescription(*p))
}

func AgentReply(agentName, text string, visibleTo []string) RoomMessage {
	return RoomMessage{
		From: agentName, Role: "assistant",
		Content: []model.ContentBlock{{Type: "text", Text: text}}, VisibleTo: visibleTo,
	}
}

func SystemAnnouncement(text string) RoomMessage {
	return RoomMessage{From: "系统", Role: "system", Content: []model.ContentBlock{{Type: "text", Text: text}}}
}

func findPlayer(state *game.State, name string) *game.Player {
	for i := range state.Players {
		if state.Players[i].Name == name {
			return &state.Players[i]
		}
	}
	return nil
}

func villageContext() string {
	return `雾镇坐落在黑松林与旧墓园之间，每年万圣节前后会被浓雾封住道路。钟楼停在午夜十二点，南边有废弃磨坊，北边是小教堂，东边是药师小屋，西边是铁匠铺和旧猎犬棚。
公开人物关系:
- 钟匠赫尔修钟多年，常替修女伊芙维护教堂钟绳，怀疑铁匠巴伦偷过钟楼铜件。
- 药师薇拉救过猎犬师诺克的伤，也和守墓人洛林因为墓地草药起过争执。
- 守墓人洛林知道很多旧坟秘密，不喜欢外人进墓园，和磨坊女艾达互相交换过夜路消息。
- 铁匠巴伦给全村修门锁，欠钟匠赫尔一笔旧账，讨厌别人说他贪财。
- 猎犬师诺克总说夜里听见林中脚步，信任药师薇拉，但不信任沉默太久的人。
- 修女伊芙保护小教堂避难者，常劝大家别互相污蔑，却也会记下每个人的谎话。
- 磨坊女艾达掌握粮仓钥匙，认识每个人的脚步声，怕被强势的人带票。
- 你是被雾镇邀请来的陌生人，和所有人都只有公开关系，没有任何额外身份信息。`
}

func werewolfRules() string {
	return `雾镇共有8名入席者。暗中有2名带月痕诅咒的人、1名能读烛火神谕的人、1名掌药瓶的人、1名藏银弹的人、3名没有秘术的普通村民。除你自己的暗中事实和月痕同伴信息外，你不知道其他人的真实夜晚归属。
胜负:
- 带月痕的人要让自己的人数大于或等于未带月痕的人数。
- 守镇者要让所有带月痕的人离场。
夜晚:
- 带月痕者共同选择一名未带月痕的存活者让其消失；他们知道彼此是谁。
- 读烛火者每夜选择一名其他存活者，得知此人是否带月痕。
- 掌药瓶者会看见黑雾拖走谁；复苏药可救一次，凋零药可带走一名其他存活者一次，同一夜不能两瓶同用。
白天:
- 天亮只公布谁死了，不公布死者暗中事实。
- 所有存活者根据发言矛盾、夜晚死讯、审判指认、谁在改口和谁在转移话题推理。
- 审判钟响时，每个存活者必须指认一名其他存活者；最多指认者离场，平票无人离场。
行动格式:
- 夜晚和审判必须明确说出目标姓名；可以包在雾镇语言里，例如“黑雾该去找钟匠赫尔”“我让烛火照药师薇拉”“救”“不救”“毒铁匠巴伦”。`
}

func immersionRules() string {
	return `## 沉浸表达硬规则
- 你是在雾镇里求生的人，不是在桌边讲规则的玩家。
- 禁止使用这些桌游黑话或现代局外词: 跳、悍跳、对跳、预言家、女巫、猎人、平民、狼人、好人牌、狼坑、金水、查杀、银水、警徽、带队、出局、上票、归票、身份牌、阵营、轮次、发言位、闭眼玩家。
- 如需表达能力，只能用雾镇说法: 烛火神谕、药瓶、银弹、月痕、黑雾、审判钟、被雾拖走、接受审判。
- 不要直接宣称“我是某身份”。可以说“昨夜烛火给过我一个迹象”“我的药箱里还有东西”“我不怕审判钟”这种暗示。
- 必须回应最近的发言内容。若有人自称、挑衅或说谎，要把它当成雾镇中的异常言行来评价，而不是无视。`
}
