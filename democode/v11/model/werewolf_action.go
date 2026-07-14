package model

// GameAction 代表从自然语言中提取出的结构化游戏意图。
// Action 枚举: kill | check | save | poison | vote | shoot | none
type GameAction struct {
	Actor   string `json:"actor,omitempty"`
	Phase   string `json:"phase,omitempty"`
	Action  string `json:"action"`
	Target  string `json:"target,omitempty"`
	RawText string `json:"rawText,omitempty"`
	Source  string `json:"source,omitempty"` // local | llm | fallback
}
