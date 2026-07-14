package model

type Theme struct {
	Title      string            `json:"title"`
	Subtitle   string            `json:"subtitle"`
	Background string            `json:"background"`
	Accent     string            `json:"accent"`
	Surface    string            `json:"surface"`
	Text       string            `json:"text"`
	Muted      string            `json:"muted"`
	Font       string            `json:"font"`
	BGM        string            `json:"bgm"`
	RoleColors map[string]string `json:"roleColors"`
}

type Script struct {
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	Subtitle    string       `json:"subtitle"`
	Locale      string       `json:"locale"`
	PlayerCount int          `json:"playerCount"`
	Summary     string       `json:"summary"`
	SourceRoot  string       `json:"sourceRoot,omitempty"`
	ManualPath  string       `json:"manualPath,omitempty"`
	Theme       Theme        `json:"theme"`
	Roles       []ScriptRole `json:"roles"`
	Phases      []Phase      `json:"phases"`
}

type ScriptSummary struct {
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	Subtitle    string       `json:"subtitle"`
	Locale      string       `json:"locale"`
	PlayerCount int          `json:"playerCount"`
	Summary     string       `json:"summary"`
	Theme       Theme        `json:"theme"`
	Roles       []ScriptRole `json:"roles"`
}

type ScriptRole struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Gender      string `json:"gender"`
	Avatar      string `json:"avatar"`
	Color       string `json:"color"`
	PublicIntro string `json:"publicIntro"`
	ScriptFile  string `json:"scriptFile,omitempty"`
	SourcePath  string `json:"sourcePath,omitempty"`
}

type Phase struct {
	ID           string            `json:"id"`
	Title        string            `json:"title"`
	Kind         string            `json:"kind"`
	DMGoal       string            `json:"dmGoal"`
	WaitForInput bool              `json:"waitForInput"`
	PrivateText  map[string]string `json:"privateText,omitempty"`
	Clues        []ClueRef         `json:"clues,omitempty"`
}

type ClueRef struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Path       string   `json:"path"`
	Visibility string   `json:"visibility"`
	RoleIDs    []string `json:"roleIds,omitempty"`
}

type PlayerSeat struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Avatar      string `json:"avatar"`
	Language    string `json:"language"`
	Human       bool   `json:"human"`
	RoleID      string `json:"roleId"`
}

type TranscriptItem struct {
	From      string   `json:"from"`
	Text      string   `json:"text"`
	RoleID    string   `json:"roleId,omitempty"`
	VisibleTo []string `json:"visibleTo,omitempty"`
}
