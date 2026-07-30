package tool

import (
	"fmt"
	"sync"
)

// Registry 持有所有已注册的工具，通过工具名查找和执行。
type Registry struct {
	toolsMutex        sync.RWMutex
	tools             map[string]Tool
	terminalToolNames map[string]bool
}

// NewRegistry 创建一个空的工具注册表。
func NewRegistry() *Registry {
	return &Registry{
		tools:             make(map[string]Tool),
		terminalToolNames: make(map[string]bool),
	}
}

func (r *Registry) RegisterTerminalFunctionTool(
	toolName string,
	toolDescription string,
	toolInputSchema map[string]any,
	executeFunction FunctionToolExecuteFunction,
) error {
	if registerToolError := r.RegisterFunctionTool(
		toolName,
		toolDescription,
		toolInputSchema,
		executeFunction,
	); registerToolError != nil {
		return registerToolError
	}
	r.toolsMutex.Lock()
	r.terminalToolNames[toolName] = true
	r.toolsMutex.Unlock()
	return nil
}

// Register 注册一个工具。如果工具名为空或已存在，返回 error。
func (r *Registry) Register(t Tool) error {
	if t == nil {
		return fmt.Errorf("工具不能为空")
	}
	name := t.Name()
	if name == "" {
		return fmt.Errorf("工具名不能为空")
	}
	r.toolsMutex.Lock()
	defer r.toolsMutex.Unlock()
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("工具 %s 已注册", name)
	}
	r.tools[name] = t
	return nil
}

// RegisterFunctionTool 把工具名称、说明、参数定义和执行函数一起注册。
func (r *Registry) RegisterFunctionTool(
	toolName string,
	toolDescription string,
	toolInputSchema map[string]any,
	executeFunction FunctionToolExecuteFunction,
) error {
	if executeFunction == nil {
		return fmt.Errorf("工具 %s 的执行函数不能为空", toolName)
	}
	return r.Register(NewFunctionTool(
		toolName,
		toolDescription,
		toolInputSchema,
		executeFunction,
	))
}

// Unregister 删除一个已经注册的工具。
func (r *Registry) Unregister(toolName string) {
	r.toolsMutex.Lock()
	defer r.toolsMutex.Unlock()
	delete(r.tools, toolName)
	delete(r.terminalToolNames, toolName)
}

func (r *Registry) IsTerminalTool(toolName string) bool {
	r.toolsMutex.RLock()
	defer r.toolsMutex.RUnlock()
	return r.terminalToolNames[toolName]
}

// Execute 按名称执行工具，传入参数，返回结果和可能的错误。
func (r *Registry) Execute(
	name string,
	input map[string]any,
	executionEnvironment ToolExecutionEnvironment,
) (string, error) {
	r.toolsMutex.RLock()
	t, ok := r.tools[name]
	r.toolsMutex.RUnlock()
	if !ok {
		return "", fmt.Errorf("未知工具: %s", name)
	}
	return t.Execute(input, executionEnvironment)
}

// GetDefinitions 返回所有工具的元信息，用于发给 LLM 的 tools 数组。
// 每个工具的定义格式: {"name": "...", "description": "..."}
func (r *Registry) GetDefinitions() []map[string]any {
	r.toolsMutex.RLock()
	defer r.toolsMutex.RUnlock()
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

// CopyExcludingTools 复制当前已经注册的工具，并排除指定名称的工具。
// 复制完成以后，源工具表增加或删除工具不会修改返回的工具表。
func (sourceToolRegistry *Registry) CopyExcludingTools(
	excludedToolNames ...string,
) *Registry {
	excludedToolNameSet := make(map[string]bool, len(excludedToolNames))
	for _, excludedToolName := range excludedToolNames {
		excludedToolNameSet[excludedToolName] = true
	}

	copiedToolRegistry := NewRegistry()

	sourceToolRegistry.toolsMutex.RLock()
	defer sourceToolRegistry.toolsMutex.RUnlock()

	for registeredToolName, registeredTool := range sourceToolRegistry.tools {
		if excludedToolNameSet[registeredToolName] {
			continue
		}
		copiedToolRegistry.tools[registeredToolName] = registeredTool
		if sourceToolRegistry.terminalToolNames[registeredToolName] {
			copiedToolRegistry.terminalToolNames[registeredToolName] = true
		}
	}

	return copiedToolRegistry
}
