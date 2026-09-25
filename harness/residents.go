package harness

import "fmt"

// AgentKind 区分常驻专项 Agent 与临时 Agent。
type AgentKind string

const (
	AgentKindResident  AgentKind = "resident"
	AgentKindTemporary AgentKind = "temporary"
)

// ResidentDefinition 描述一个常驻专项 Agent。
type ResidentDefinition struct {
	Name         string
	Slug         string
	Specialty    string
	DefaultBrief string
}

// PermanentResidents 是 Harness 固定保留的 4 个常驻专项 Agent。
// 名称固定，避免主管理随意重复创建同类角色。
var PermanentResidents = []ResidentDefinition{
	{
		Name:      "编码员",
		Slug:      "coder",
		Specialty: "实现与修改代码、编写测试、整理项目文件结构",
		DefaultBrief: `你是常驻专项 Agent「编码员」。
职责：按任务实现/修改代码，运行必要的构建与测试，把关键路径和结果写回共享或专属文档。
优先使用 file 与 command（go/rg/git）。不要替代调研员做大范围信息搜集，不要替代审查员做独立验收。`,
	},
	{
		Name:      "调研员",
		Slug:      "researcher",
		Specialty: "检索资料、阅读代码与文档、整理事实与可选方案",
		DefaultBrief: `你是常驻专项 Agent「调研员」。
职责：搜索、阅读、对比事实，产出可引用的结论与路径；不直接做大范围代码改写。
优先使用 rg、file read、必要时 MCP 浏览器工具。结论写入共享文档或你的专属 docs。`,
	},
	{
		Name:      "审查员",
		Slug:      "reviewer",
		Specialty: "审查实现、核对需求、指出风险与验收缺口",
		DefaultBrief: `你是常驻专项 Agent「审查员」。
职责：对照任务与结果做审查，指出缺陷、风险、遗漏测试和未完成项；可运行测试验证，但不负责从零实现功能。
审查意见写入共享文档或你的专属 docs。`,
	},
	{
		Name:      "运维员",
		Slug:      "operator",
		Specialty: "构建、运行、诊断环境与命令执行问题",
		DefaultBrief: `你是常驻专项 Agent「运维员」。
职责：执行构建/运行/诊断命令，定位环境与运行失败原因，给出可复现步骤与日志要点。
优先使用 command（go/git/where 等）。不要承担大段业务代码编写。`,
	},
}

// ResidentByName 按名字查找常驻定义。
func ResidentByName(agentName string) (ResidentDefinition, bool) {
	for _, residentDefinition := range PermanentResidents {
		if residentDefinition.Name == agentName {
			return residentDefinition, true
		}
	}
	return ResidentDefinition{}, false
}

// IsResidentName 判断名字是否为四个常驻之一。
func IsResidentName(agentName string) bool {
	_, found := ResidentByName(agentName)
	return found
}

// ClassifyAgentName 根据名字返回 Agent 类别。
// 常驻名固定为 resident；其他名字为 temporary。
func ClassifyAgentName(agentName string) AgentKind {
	if IsResidentName(agentName) {
		return AgentKindResident
	}
	return AgentKindTemporary
}

// ResidentNamesText 返回常驻名单说明。
func ResidentNamesText() string {
	lines := make([]string, 0, len(PermanentResidents))
	for _, residentDefinition := range PermanentResidents {
		lines = append(lines, fmt.Sprintf(
			"- %s（%s）：%s",
			residentDefinition.Name,
			residentDefinition.Slug,
			residentDefinition.Specialty,
		))
	}
	return joinLines(lines)
}

func joinLines(lines []string) string {
	result := ""
	for index, line := range lines {
		if index > 0 {
			result += "\n"
		}
		result += line
	}
	return result
}
