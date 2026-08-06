package main

import (
	"fmt"
	"os"

	"cc-agent-go/democode/v01/analyzerlib"
)

// ==================== 包级变量 ====================
// 这里定义一个客户端可见的变量（仅做演示，实际上被下面的配置覆盖）。
var defaultText = `Go is a statically typed compiled language
designed at Google by Robert Griesemer Rob Pike and Ken Thompson
Go has garbage collection
Go supports concurrent programming with goroutines and channels
Go is simple fast and reliable
Go Go Go`

// ==================== init 函数 ====================
// 第二个包的 init 也会在 main 之前执行。
// 执行顺序：标准库 → analyzerlib init → main 包 init → main。
func init() {
	fmt.Println("[main 包 init] analyzerlib init 已经执行完了")
}

// 演示函数作为值：用自定义分析步骤组成 pipeline。
func customPipeline() []analyzerlib.PipelineStep {
	// Go 里函数可以赋给变量，这里把匿名函数转为 PipelineStep 类型。
	customStep := analyzerlib.PipelineStep(func(input string) (map[string]any, error) {
		return map[string]any{
			"step":  "自定义步骤",
			"len":   len(input),
			"hasGo": true, // 实际判断略
		}, nil
	})
	return []analyzerlib.PipelineStep{
		analyzerlib.StepWordCount,
		analyzerlib.StepLineCount,
		customStep,
	}
}

func main() {
	fmt.Println("===== v0.1 文本分析器 =====")

	// 1. 获取输入文本
	text := defaultText
	if len(os.Args) > 1 {
		text = os.Args[1]
	}
	fmt.Printf("\n输入文本:\n---\n%s\n---\n", text)

	// 2. 演示：多返回值 + 具名返回值
	fmt.Println("\n--- 1. 多返回值 + 具名返回值 ---")
	stats, err := analyzerlib.Analyze(text, analyzerlib.DefaultConfig)
	if err != nil {
		fmt.Printf("错误: %v\n", err)
		return
	}
	fmt.Printf("总字符=%d  总单词=%d  总行=%d  平均词长=%.1f\n",
		stats.TotalChars, stats.TotalWords, stats.TotalLines, stats.AvgWordLen)

	// 3. 演示：类型选择 + 类型断言
	fmt.Println("\n--- 2. 类型选择（type switch）---")
	analyzerlib.PrintResult(stats)
	analyzerlib.PrintResult(42)
	analyzerlib.PrintResult("hello")
	analyzerlib.PrintResult(stats.TopWords)

	fmt.Println("\n--- 3. 类型断言（type assertion）---")
	n, err := analyzerlib.ExtractInt(100)
	if err != nil {
		fmt.Println("断言失败:", err)
	} else {
		fmt.Printf("从 any 中提取 int: %d\n", n)
	}
	// 断言失败的例子
	_, err = analyzerlib.ExtractInt("不是int")
	fmt.Printf("断言失败用例: %v\n", err)

	// 4. 演示：可变参数（Merge）
	fmt.Println("\n--- 4. 可变参数 ---")
	merged := analyzerlib.Merge(" | ", "Go", "Rust", "Zig", "C")
	fmt.Printf("合并结果: %s\n", merged)
	// 也可以不传可变部分
	fmt.Printf("空合并: '%s'\n", analyzerlib.Merge("-"))

	// 5. 演示：函数作为值 —— Pipeline
	fmt.Println("\n--- 5. 函数作为值（Pipeline）---")
	steps := customPipeline()
	results, err := analyzerlib.RunPipeline(text, steps...)
	if err != nil {
		fmt.Printf("Pipeline 错误: %v\n", err)
	} else {
		for i, r := range results {
			fmt.Printf("  步骤 %d: ", i+1)
			analyzerlib.PrintResult(r)
		}
	}

	// 6. 演示：make vs new
	fmt.Println("\n--- 6. make vs new ---")
	a := analyzerlib.NewAnalyzer(analyzerlib.DefaultConfig) // 内部用 make 创建 map
	fmt.Printf("make 创建的 Analyzer: config=%+v\n", a.Config())

	wc := analyzerlib.NewBlankWordCount() // 内部用 new
	fmt.Printf("new 创建的 WordCount: %+v（零值）\n", *wc)

	// 7. 演示：错误包装 %w
	fmt.Println("\n--- 7. 错误包装 %%w ---")
	_, err = analyzerlib.RunPipeline("", steps...)
	if err != nil {
		fmt.Printf("包装后的错误: %v\n", err)
	}

	// 8. 演示：slice 操作
	fmt.Println("\n--- 8. slice 操作 ---")
	words := []string{"Go", "Rust", "Zig", "C", "Python"}
	fmt.Printf("原始切片: %v, len=%d, cap=%d\n", words, len(words), cap(words))
	// 截取
	first3 := words[:3]
	fmt.Printf("截取前 3: %v\n", first3)
	// append 触发扩容
	bigger := append(words, "JavaScript", "TypeScript", "Java", "Kotlin", "Swift")
	fmt.Printf("append 后: %v, len=%d, cap=%d（容量可能翻倍）\n", bigger, len(bigger), cap(bigger))
	// copy
	dst := make([]string, 3)
	copy(dst, words)
	fmt.Printf("copy 3 个: %v\n", dst)

	// 9. 演示：接收者值 vs 指针
	fmt.Println("\n--- 9. 接收者：值 vs 指针 ---")
	a.AddWord("hello")
	a.AddWord("hello")
	a.AddWord("world")
	fmt.Printf("添加了 hello×2, world×1 → TopWords(5): %+v\n", a.TopWords(5))

	fmt.Println("\n===== v0.1 结束 =====")
}
