package tool

// Tool 接口定义了所有工具必须实现的方法。
//
// Go 的接口是隐式实现的：一个类型只要定义了这四个方法，就自动实现了 Tool 接口。
type Tool interface {
	Name() string
	Description() string

	// InputSchema 返回工具参数的 JSON Schema，用于向 API 声明工具签名
	InputSchema() map[string]any

	Execute(
		toolArguments map[string]any,
		executionEnvironment ToolExecutionEnvironment,
	) (string, error)
}
