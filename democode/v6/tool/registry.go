package tool

import "fmt"

// Registry 持有所有已注册的工具，通过工具名查找和执行。
type Registry struct {
	tools map[string]Tool
}

// NewRegistry 创建一个空的工具注册表。
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register 注册一个工具。如果工具名为空或已存在，返回 error。
func (r *Registry) Register(t Tool) error {
	name := t.Name()
	if name == "" {
		return fmt.Errorf("工具名不能为空")
	}
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("工具 %s 已注册", name)
	}
	r.tools[name] = t
	return nil
}

// Execute 按名称执行工具，传入参数，返回结果和可能的错误。
func (r *Registry) Execute(name string, input map[string]any) (string, error) {
	t, ok := r.tools[name]
	if !ok {
		return "", fmt.Errorf("未知工具: %s", name)
	}
	return t.Execute(input)
}

// GetDefinitions 返回所有工具的元信息，用于发给 LLM 的 tools 数组。
// 每个工具的定义格式: {"name": "...", "description": "..."}
func (r *Registry) GetDefinitions() []map[string]any {
	defs := make([]map[string]any, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, map[string]any{
			"name":         t.Name(),
			"description":  t.Description(),
			"input_schema": t.InputSchema(),
		})
	}
	return defs
}
