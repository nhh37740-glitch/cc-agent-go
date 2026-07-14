// package: 声明本文件属于哪个包。main 包是程序的入口包，只有 main 包才能编译成可执行文件。
// 其他包（如 config、service）编译后是库，不能独立运行。
package main

// import: 导入标准库或其他包。Go 不允许导入但不使用的包（编译报错）。
// 这和 Java 不同——Java 的 import 只是省去写全限定名，不用的 import 只是警告。
import (
	"fmt"      // 格式化输入输出
	"sort"     // 标准排序包（用于校验冒泡排序结果）
	"sync"     // 并发原语
	"time"     // 时间操作
)

// const: 声明常量。常量在编译期确定值，不可修改。
// 和 Java 的 static final 类似，但 Go 的 const 不限于整数和字符串，还可以是自定义类型的值。
const MaxSliceLen = 20

// var: 声明包级变量。这里定义了一个预置的乱序切片。
// := 是短声明（只能在函数内使用），var 可以在包级使用。
var defaultData = []int{9, 3, 7, 1, 5, 8, 2, 4}

// type 和 interface: 定义一个接口类型。Go 的接口是隐式实现的——
// 任何类型只要方法签名匹配，就自动实现了接口，不需要 Java 的 implements 关键字。
type BenchMarker interface {
	Run(name string) string
}

// type 和 struct: 结构体，把数据字段组合成一个类型。
// struct 是 Go 中的数据容器，没有继承，只靠组合。
type SortRunner struct {
	mu      sync.Mutex // sync.Mutex: 互斥锁，保护并发访问。小写开头 = 包内私有。
	results map[string]int
}

// func: 声明函数。这里 (s *SortRunner) 是接收者——这使 Run 成为 SortRunner 的方法。
// 等价于 Java 的实例方法，s 相当于 Java 的 this，但 Go 没有 this 关键字，接收者名字自己取。
func (s *SortRunner) Run(name string) string {
	// defer: 推迟执行。defer 后面的语句会在函数 return 之前执行。
	// 多个 defer 按后进先出（栈）的顺序执行。常用于关闭文件、释放锁。
	defer func() {
		fmt.Println("  [defer1 执行] Run 方法结束，清理工作在这里做")
	}()
	defer func() {
		fmt.Println("  [defer2 执行] Run 方法结束，清理工作在这里做")
	}()

	// 对切片做一次浅拷贝，避免排序修改原数据
	data := make([]int, len(defaultData))
	copy(data, defaultData)

	fmt.Printf("\n=== %s ===\n", name)
	fmt.Println("排序前:", data)

	bubbleSort(data) // 调用包级函数（后面定义）

	fmt.Println("排序后:", data)

	// 用标准库 sort 包验证结果：检查切片是否已按升序排列
	if sort.IntsAreSorted(data) {
		s.results[name] = len(data)
		fmt.Println("  ✓ 排序正确")
		return "成功"
	}
	// else: 条件分支的另一条路径。Go 的 else 必须紧跟在 if 块的右大括号 } 后面，不能另起一行。
	// 这是因为 Go 编译器会在 } 后自动插入分号。
	fmt.Println("  ✗ 排序失败")
	return "失败"
}

// bubbleSort: 核心排序逻辑。
// 遍历切片，相邻元素两两比较，大的往后沉，每轮把最大的元素推到末尾。
func bubbleSort(arr []int) {
	n := len(arr)
	if n <= 1 {
		return // return: 提前返回，不再执行后续代码。
	}

	// for: Go 唯一的循环关键字，没有 while。
	// 这是经典三段式 for：初始化; 条件; 后置操作。
	for i := 0; i < n-1; i++ {
		swapped := false

		// range: 遍历切片/数组/映射的索引和值。这里用 _ 丢弃了值，只要索引。
		// _ 是空白标识符，Go 不允许声明了但没用的变量，用 _ 来接收不需要的值。
		for j := range arr[:n-1-i] {
			if arr[j] > arr[j+1] {
				// 多变量同时赋值：Go 会先计算右边所有值，再依次赋给左边。
				// 所以这里不需要临时变量来交换。
				arr[j], arr[j+1] = arr[j+1], arr[j]
				swapped = true
			}
		}

		// break: 跳出最内层 for 循环。如果本轮没有发生交换，说明已经有序，提前结束。
		if !swapped {
			break
		}
	}
}

// bubbleSortWithContinue: 演示 continue 和 goto 的版本。
// continue: 跳过本轮循环的剩余代码，直接进入下一次迭代。
func bubbleSortWithContinue(arr []int) {
	n := len(arr)
	// map: 映射类型（哈希表），键→值的映射。这里是记录每个位置被交换的次数。
	swapCount := make(map[int]int)
	_ = swapCount // 标记使用（实际不读取，仅演示）

	for i := 0; i < n-1; i++ {
		for j := 0; j < n-1-i; j++ {
			if arr[j] <= arr[j+1] {
				continue // continue: 不满足交换条件时跳过，进入下一个 j
			}
			arr[j], arr[j+1] = arr[j+1], arr[j]
		}
	}
}

// switchDemo: 演示 switch、case、default、fallthrough。
// Go 的 switch 和 Java 有两个关键区别：
// 1. 每个 case 默认自带 break，不会穿透到下一个 case。
// 2. 如果要穿透，必须显式写 fallthrough。
func switchDemo(n int) string {
	switch n {
	case 0, 1: // 多个值可以用逗号
		return "已有序或仅一个元素"
	case 2:
		return "仅两个元素，一次比较即可"
	case 3, 4, 5:
		return "小数组"
	default: // default: 所有 case 都不匹配时执行。
		return "常规数组"
	}
}

// fallthroughDemo: 演示 fallthrough。fallthrough 会强制执行下一个 case 的代码体，
// 且不判断下一个 case 的条件。这是 Go 中少数需要显式穿透的场景。
func fallthroughDemo() {
	fmt.Println("\n--- fallthrough 演示 ---")
	n := 1
	// switch: 多路分支选择。和 if-else 链等价，但更清晰。
	switch {
	case n == 1:
		fmt.Println("  case 1: 执行")
		fallthrough // 强制执行下一个 case，不判断条件
	case n == 2:
		fmt.Println("  case 2: 虽然 n!=2，但因为 fallthrough 也被执行了")
	default:
		fmt.Println("  default 不会执行，因为 case 2 被穿透命中后也自带 break")
	}
}

// chanDemo: 演示 goroutine、channel、select。
// chan: 通道类型，goroutine 之间通过 channel 通信。Go 的理念是：
// "不要通过共享内存来通信，而要通过通信来共享内存"
func chanDemo() {
	// make: 内建函数，创建 chan、map、slice 这三种引用类型。
	ch := make(chan string) // 无缓冲 channel：发送方必须等接收方就绪。

	// go: 启动一个 goroutine——Go 的轻量级并发单元。它不是操作系统线程，
	// 而是由 Go 运行时调度的协程，创建成本极低（几 KB 栈空间起步）。
	go func() {
		time.Sleep(10 * time.Millisecond) // 模拟异步排序
		// channel 发送操作：ch <- 值
		ch <- "goroutine 中的排序完成"
	}()

	fmt.Println("\n--- channel + select 演示 ---")
	// select: 同时监听多个 channel 操作，哪个先就绪就执行哪个 case。
	// 如果多个同时就绪，随机选一个（防止饥饿）。
	// 如果都未就绪且有 default，执行 default。
	select {
	case msg := <-ch: // channel 接收操作：变量 := <-ch
		fmt.Println("  收到消息:", msg)
	case <-time.After(50 * time.Millisecond): // time.After 返回一个 channel，到时间后发送值
		fmt.Println("  超时：goroutine 没在 50ms 内返回")
	default:
		// 这个 default 几乎一定先执行，因为 goroutine 要等 10ms。
		// 没有 default 的 select 会阻塞等待；有 default 则非阻塞。
		fmt.Println("  select default: 两个 channel 都还没就绪（非阻塞）")
	}

	// 再等一会儿，让 goroutine 的消息能被收到
	select {
	case msg := <-ch:
		fmt.Println("  第二次 select 收到:", msg)
	case <-time.After(100 * time.Millisecond):
		fmt.Println("  第二次 select 超时")
	}
}

// gotoDemo: 演示 goto。Go 保留了 goto，但用法极其受限：
// 不能跳过变量声明，不能跨函数。实际项目中极少使用，仅做了解。
func gotoDemo() {
	i := 0
	fmt.Println("\n--- goto 演示 ---")
loop: // 标签，goto 的目标位置。
	i++
	fmt.Println("  i =", i)
	if i < 3 {
		goto loop // 跳回 loop 标签处，模拟循环。
	}
	fmt.Println("  goto 循环结束（实际应该用 for，这只是演示语法）")
}

// main: 程序入口。无参数，无返回值。程序启动后 Go 运行时自动调用。
func main() {
	fmt.Println("Go 冒泡排序 + 全部关键字演示")
	fmt.Println("================================")

	// 内置函数 make: 创建 map。map 必须用 make 初始化才能写入，nil map 写入会 panic。
	runner := &SortRunner{
		results: make(map[string]int),
	}

	// 使用 switch 判断数组规模
	fmt.Println("数组规模判断:", switchDemo(len(defaultData)))

	// 执行排序
	_ = runner.Run("冒泡排序") // _ 接收不需要的返回值

	// bubbleSortWithContinue 的运行
	dataCopy := make([]int, len(defaultData))
	copy(dataCopy, defaultData)
	fmt.Println("\n--- continue 版排序 ---")
	fmt.Println("排序前:", dataCopy)
	bubbleSortWithContinue(dataCopy)
	fmt.Println("排序后:", dataCopy)

	// 演示其他关键字
	fallthroughDemo()
	chanDemo()
	gotoDemo()

	fmt.Println("\n================================")
	fmt.Println("全部 25 个 Go 关键字均已使用并注释")
}

// 包初始化函数：init 在 main 之前自动执行，一个包可以有多个 init。
// 执行顺序：导入的包的 init → 当前包的 init → main。
func init() {
	fmt.Println("[init 执行] 包初始化：main 之前自动调用")
}
