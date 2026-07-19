package tool

// FunctionToolExecuteFunction 是动态注册工具时保存的执行函数。
type FunctionToolExecuteFunction func(toolArguments map[string]any) (string, error)

// FunctionTool 把工具定义和执行函数保存在同一个 Tool 实现中。
type FunctionTool struct {
	toolName        string
	toolDescription string
	toolInputSchema map[string]any
	executeFunction FunctionToolExecuteFunction
}

func NewFunctionTool(
	toolName string,
	toolDescription string,
	toolInputSchema map[string]any,
	executeFunction FunctionToolExecuteFunction,
) *FunctionTool {
	return &FunctionTool{
		toolName:        toolName,
		toolDescription: toolDescription,
		toolInputSchema: toolInputSchema,
		executeFunction: executeFunction,
	}
}

func (functionTool *FunctionTool) Name() string {
	return functionTool.toolName
}

func (functionTool *FunctionTool) Description() string {
	return functionTool.toolDescription
}

func (functionTool *FunctionTool) InputSchema() map[string]any {
	return functionTool.toolInputSchema
}

func (functionTool *FunctionTool) Execute(toolArguments map[string]any) (string, error) {
	return functionTool.executeFunction(toolArguments)
}
