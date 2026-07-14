// analyzerlib 包：文本分析器。演示以下 Go 概念：
//   - slice（切片）的创建、append、截取
//   - 多返回值 + 具名返回值
//   - 可变参数
//   - new vs make 的区别
//   - 接收者用值 vs 指针
//   - 空接口 any + 类型断言 + 类型选择
//   - error 接口 + 自定义错误 + fmt.Errorf + %w 错误包装
//   - 包级变量 + init 函数
package analyzerlib

import (
	"fmt"
	"strings"
	"unicode"
)

// ==================== 自定义错误类型 ====================
// EmptyInputError 实现了 error 接口（Go 的 error 是一个接口，只有一个方法 Error() string）。
// 任何类型只要实现了 Error() string 方法，就是一个合法的 error。
type EmptyInputError struct {
	FuncName string // 哪个函数触发了这个错误
}

// Error 实现 error 接口。接收者用值类型（EmptyInputError，不是 *EmptyInputError），
// 因为这个方法只读数据、不修改结构体字段。
func (e EmptyInputError) Error() string {
	return fmt.Sprintf("analyzerlib: 空输入（发生在 %s）", e.FuncName)
}

// ==================== 包级变量 ====================
// 包级变量在 init 之前初始化，整个包都能访问。
// DefaultConfig 是默认分析配置，大写开头 = 导出（包外可见）。
var DefaultConfig = AnalyzerConfig{
	MinWordLen:  2,    // 最短单词长度，短于此的忽略
	MaxWords:     100,  // 最多返回多少个高频词
	CaseSensitive: false,
}

// ==================== init 函数 ====================
// init 在包被导入时自动执行，在 main 之前。
// 一个包可以有多个 init，按在文件里出现的顺序执行。
// 执行顺序：被导入包的 init → 当前包的 init → main。
// 这里用来验证 DefaultConfig 的值是否合理。
func init() {
	fmt.Println("  [analyzerlib init] 包初始化中...")
	if DefaultConfig.MinWordLen < 1 {
		panic("analyzerlib: MinWordLen 必须 >= 1") // panic 不演示了，这里是保护
	}
	if DefaultConfig.MaxWords < 1 {
		DefaultConfig.MaxWords = 100
	}
	fmt.Printf("  [analyzerlib init] 默认配置: MinWordLen=%d, MaxWords=%d\n",
		DefaultConfig.MinWordLen, DefaultConfig.MaxWords)
}

// ==================== 结构体定义 ====================
// AnalyzerConfig 分析器的配置
type AnalyzerConfig struct {
	MinWordLen    int
	MaxWords      int
	CaseSensitive bool
}

// WordCount 词频条——单词和它的出现次数
type WordCount struct {
	Word  string
	Count int
}

// Analyzer 文本分析器。字段小写开头 = 包外不能直接访问，只能通过方法操作。
type Analyzer struct {
	config AnalyzerConfig
	// 分析结果缓存，key 是单词，value 是出现次数
	cache map[string]int
}

// TextStats 分析结果汇总
type TextStats struct {
	TotalChars  int          // 总字符数
	TotalWords  int          // 总单词数
	TotalLines  int          // 总行数
	TopWords    []WordCount  // 高频词列表（按频次降序）
	AvgWordLen  float64      // 平均单词长度
}

// ==================== 构造函数（分别演示 new 和 make）====================
// NewAnalyzer 使用 make 初始化 Analyzer。
//
// make vs new 的区别（这是 Go 面试高频问题）：
//   - make 只用于 slice、map、chan 三种引用类型，返回的是值（不是指针）。
//     这三种类型底层有数据结构需要初始化，否则是 nil，nil slice/map/chan 操作会 panic。
//   - new 可用于任何类型，分配一块零值内存，返回该类型的指针。
//     new 不初始化内部结构，只是给了一块内存。对 map 来说 new 返回的是 nil map 的指针，
//     往 nil map 写数据会 panic，所以 map 必须用 make。
func NewAnalyzer(cfg AnalyzerConfig) *Analyzer {
	return &Analyzer{
		config: cfg,
		cache:  make(map[string]int), // make: 创建已初始化的 map，可以安全写入
	}
}

// ==================== 演示 new 的用法 ====================
// NewBlankWordCount 用 new 返回一个零值 WordCount 的指针。
// new(WordCount) 等价于 &WordCount{}，但 new 不写字段名，直接拿零值。
// Go 社区更习惯用 &WordCount{} 而不是 new，但 new 在一些场景（如泛型零值）有用。
func NewBlankWordCount() *WordCount {
	wc := new(WordCount) // new: 分配内存，返回 *WordCount，字段是零值（Word="" Count=0）
	return wc
}

// ==================== 接收者用值 vs 指针 ====================
// AddWord 接收者是指针（*Analyzer），因为要修改 Analyzer 的内部状态（cache）。
// 如果接收者用值（Analyzer），会操作一份拷贝，方法返回后拷贝被扔掉，原对象不变。
// 这是 Go 初学常见坑：用值接收者去写数据，写了白写。
func (a *Analyzer) AddWord(word string) {
	if !a.config.CaseSensitive {
		word = strings.ToLower(word)
	}
	a.cache[word]++
}

// Config 接收者是值（Analyzer），只读操作不需要指针。
// 值接收者保证方法内部拿到的是 Analyzer 的快照，不会意外修改原对象。
func (a Analyzer) Config() AnalyzerConfig {
	return a.config
}

// TopWords 返回高频词列表。接收者是指针，因为要读取 cache。
// 实际项目里一般统一用指针接收者，除非有特殊理由用值。
func (a *Analyzer) TopWords(n int) []WordCount {
	return topN(a.cache, n)
}

// ==================== 多返回值 + 具名返回值 ====================
// Analyze 对一段文本做完整分析。
//
// 多返回值：Go 的标准模式是 (result, error)，调用方立即检查 error。
//
// 具名返回值：返回值的名字（stats 和 err）写在函数签名里。
// 好处：函数体里直接 return 就返回这些变量当前的值（空返回）。
// 坏处：名字的作用域是整个函数体，可能在长函数里被意外遮蔽（shadowing）。
func Analyze(text string, cfg AnalyzerConfig) (stats TextStats, err error) {
	// 具名返回值在函数入口就初始化为零值，所以 stats.TotalChars = 0, err = nil 已经成立。
	if text == "" {
		// 使用自定义错误
		err = EmptyInputError{FuncName: "Analyze"}
		return // 空返回：返回当前 stats（零值）和 err（刚赋的值）
	}

	// 创建分析器
	a := NewAnalyzer(cfg)

	// 按行拆分 —— 下面用 slice 操作
	lines := splitLines(text)
	stats.TotalLines = len(lines)

	// 遍历每一行，收集单词
	var allWords []string // slice 零值是 nil，nil slice 的 len 是 0，可以安全 append
	for _, line := range lines {
		words := splitWords(line)
		allWords = append(allWords, words...) // append: 追加切片，... 是展开操作符
		for _, w := range words {
			if len([]rune(w)) >= cfg.MinWordLen { // []rune 转 rune 切片以正确处理多字节字符
				a.AddWord(w)
			}
		}
	}

	stats.TotalWords = len(allWords)
	stats.TotalChars = len([]rune(text))
	if stats.TotalWords > 0 {
		stats.AvgWordLen = float64(stats.TotalChars) / float64(stats.TotalWords)
	}
	stats.TopWords = a.TopWords(cfg.MaxWords)
	return // 具名返回值的空返回
}

// ==================== slice 操作 ====================
// splitLines 按换行符拆分字符串，返回字符串切片。
// 这是最基础的切片操作：创建、追加。
func splitLines(text string) []string {
	// 用标准库 strings 包拆分
	lines := strings.Split(text, "\n")
	// 过滤掉空行（演示切片截取和 append）
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// splitWords 按空白字符拆单词。演示更多切片操作。
func splitWords(line string) []string {
	raw := strings.Fields(line) // Fields 按空白拆分，返回 []string
	// 清理标点符号
	var cleaned []string
	for _, w := range raw {
		w = strings.TrimFunc(w, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
		if w != "" {
			cleaned = append(cleaned, w)
		}
	}
	return cleaned
}

// topN 对词频 map 排序取前 N 个。演示切片排序和截取。
func topN(freq map[string]int, n int) []WordCount {
	// 第一步：map → slice
	result := make([]WordCount, 0, len(freq)) // make slice 时预先分配容量（cap），避免多次扩容
	for word, count := range freq {
		result = append(result, WordCount{Word: word, Count: count})
	}

	// 第二步：冒泡排序（简单实现），按 Count 降序
	for i := 0; i < len(result)-1; i++ {
		for j := 0; j < len(result)-1-i; j++ {
			if result[j].Count < result[j+1].Count {
				result[j], result[j+1] = result[j+1], result[j]
			}
		}
	}

	// 第三步：截取前 N 个（演示切片截取 [:n]）
	if n > len(result) {
		n = len(result)
	}
	return result[:n] // 切片表达式 [low:high]，取 low 到 high-1 的元素
}

// ==================== 可变参数 ====================
// Merge 合并多段文本，用分隔符连接。texts ...string 是可变参数。
// 调用方可以传 0 个或多个 string。
func Merge(sep string, texts ...string) string {
	if len(texts) == 0 {
		return ""
	}
	return strings.Join(texts, sep)
}

// ==================== 函数作为值传递（分析 Pipeline）====================
// PipelineStep 是一个函数类型：接收一段文本，输出分析结果或错误。
// 函数在 Go 里是一等公民：可以赋给变量、作为参数传递、作为返回值。
type PipelineStep func(input string) (map[string]any, error)

// RunPipeline 按顺序执行多个分析步骤，收集结果。
// steps 接收一个由函数组成的切片——把逻辑当作数据来组装。
func RunPipeline(text string, steps ...PipelineStep) ([]map[string]any, error) {
	if text == "" {
		// %w 错误包装：将底层错误包装到新错误里，保留原始错误的类型信息。
		// 调用方可以用 errors.Is / errors.As 来检查原始错误。
		return nil, fmt.Errorf("RunPipeline: %w", EmptyInputError{FuncName: "RunPipeline"})
	}

	var results []map[string]any
	for i, step := range steps {
		result, err := step(text)
		if err != nil {
			// %w 再次演示：包装时带上步骤编号
			return nil, fmt.Errorf("步骤 %d 失败: %w", i+1, err)
		}
		results = append(results, result)
	}
	return results, nil
}

// StepWordCount 一个 PipelineStep 实现，做词频统计。用作"函数作为值"的演示。
func StepWordCount(text string) (map[string]any, error) {
	words := splitWords(text)
	freq := make(map[string]int)
	for _, w := range words {
		freq[w]++
	}
	return map[string]any{
		"step":       "词频统计",
		"totalWords": len(words),
		"uniqueWords": len(freq),
	}, nil
}

// StepLineCount 另一个 PipelineStep，统计行数和字符数。
func StepLineCount(text string) (map[string]any, error) {
	lines := splitLines(text)
	return map[string]any{
		"step":       "行数统计",
		"totalLines": len(lines),
		"totalChars": len([]rune(text)),
	}, nil
}

// ==================== 类型断言 + 类型选择 ====================
// PrintResult 接收一个 any（空接口），用类型选择（type switch）判断具体类型并打印。
// any 是 Go 1.18 引入的，等价于 interface{}，可以存任何类型的值。
func PrintResult(v any) {
	fmt.Printf("  值: %v, 类型: ", v)

	// 类型选择：switch v := x.(type)。注意写法是 .(type)，只在 switch 里有效。
	switch val := v.(type) {
	case TextStats:
		fmt.Printf("TextStats（总单词=%d, 总行数=%d）\n", val.TotalWords, val.TotalLines)
	case int:
		fmt.Printf("int（值=%d）\n", val)
	case string:
		fmt.Printf("string（长度=%d）\n", len(val))
	case []WordCount:
		fmt.Println("[]WordCount（高频词列表）")
		for _, wc := range val {
			fmt.Printf("    %s: %d\n", wc.Word, wc.Count)
		}
	case map[string]any:
		fmt.Println("map[string]any（Pipeline 步骤结果）")
		for k, v := range val {
			fmt.Printf("    %s: %v\n", k, v)
		}
	default:
		fmt.Printf("未识别的类型 %T\n", val)
	}
}

// ExtractInt 演示类型断言（不是类型选择）。
// 从 any 里提取 int 值，如果不是 int 返回错误。
func ExtractInt(v any) (int, error) {
	// 类型断言：x.(T)。双返回值写法：第二个返回值 ok 表示断言是否成功。
	// 单返回值写法 n := v.(int) 如果断言失败会 panic。
	n, ok := v.(int)
	if !ok {
		return 0, fmt.Errorf("类型断言失败：期望 int，实际是 %T", v)
	}
	return n, nil
}
