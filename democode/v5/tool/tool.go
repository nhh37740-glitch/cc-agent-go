package tool

// Tool 接口定义了所有工具必须实现的方法。
//
// Go 的接口是隐式实现的：一个类型只要定义了 Name()、Description()、Execute() 三个方法，
// 它就自动实现了 Tool 接口，不需要 "implements" 关键字。
type Tool interface {
	// Name 返回工具名，例如 "bash"
	Name() string

	// Description 返回工具的功能描述，会发给 LLM 帮助它决定什么时候用这个工具
	Description() string

	// Execute 执行工具，参数是 LLM 传的输入（JSON 反序列化为 map）
	// 返回执行结果字符串和可能的错误
	Execute(input map[string]any) (string, error)
}
