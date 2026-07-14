package tool

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// BashTool 提供在 workspace 内执行白名单命令的能力。
// 通过实现 Name()、Description()、Execute() 三个方法，
// 隐式地实现了 Tool 接口。
type BashTool struct {
	workspace string
}

// NewBashTool 创建一个新的 BashTool，workspace 是命令执行的工作目录。
func NewBashTool(workspace string) *BashTool {
	return &BashTool{workspace: workspace}
}

// Name 返回工具名。
func (b *BashTool) Name() string {
	return "bash"
}

// Description 返回工具描述，会发给 LLM。
func (b *BashTool) Description() string {
	return `在 workspace 内执行白名单 shell 命令。

允许的命令：git, rg, cat, ls, find, mkdir, rm, cp, mv, touch, echo, head, tail, wc, sort, uniq, grep, uv, python, go

安全限制：
- 禁止分号(;)、管道(|)、逻辑与(&&)、逻辑或(||)、命令替换($()或反引号)、重定向(>或<)
- 命令必须在 workspace 内执行
- 30 秒超时`
}

// allowedCommands 白名单 —— 只有在此列表中的命令才能执行。
var allowedCommands = map[string]bool{
	"git":    true,
	"rg":     true,
	"cat":    true,
	"ls":     true,
	"find":   true,
	"mkdir":  true,
	"rm":     true,
	"cp":     true,
	"mv":     true,
	"touch":  true,
	"echo":   true,
	"head":   true,
	"tail":   true,
	"wc":     true,
	"sort":   true,
	"uniq":   true,
	"grep":   true,
	"uv":     true,
	"python": true,
	"go":     true,
}

// forbiddenChars 禁止出现在命令中的字符。
// strings.ContainsAny 会检查命令串是否包含这里任意一个字符。
const forbiddenChars = ";|&$`><"

// Execute 执行 bash 命令。
func (b *BashTool) Execute(input map[string]any) (string, error) {
	cmdStr, ok := input["command"].(string)
	if !ok || cmdStr == "" {
		return "", fmt.Errorf("bash 工具需要 command 参数")
	}

	// 安全检查 1：黑名单字符
	if strings.ContainsAny(cmdStr, forbiddenChars) {
		return "", fmt.Errorf("命令包含禁止字符: %s", cmdStr)
	}

	// 安全检查 2：提取命令名，校验白名单
	// strings.Fields 按空白字符（空格/tab/换行）切分字符串
	parts := strings.Fields(cmdStr)
	if len(parts) == 0 {
		return "", fmt.Errorf("空命令")
	}
	cmdName := parts[0]

	// 命令名可能包含路径（如 /usr/bin/git），取最后一段
	if i := strings.LastIndex(cmdName, "/"); i >= 0 {
		cmdName = cmdName[i+1:]
	}
	if i := strings.LastIndex(cmdName, "\\"); i >= 0 {
		cmdName = cmdName[i+1:]
	}

	if !allowedCommands[cmdName] {
		return "", fmt.Errorf("不在白名单中: %s", cmdName)
	}

	// 创建带超时的 context
	// context.WithTimeout 返回一个新 context 和一个 cancel 函数
	// 30 秒后 context 自动超时，ExecCommandContext 会 kill 进程
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel() // 函数返回时释放 context 资源

	// exec.CommandContext 创建命令对象，context 超时后自动 kill
	cmd := exec.CommandContext(ctx, cmdName, parts[1:]...)
	cmd.Dir = b.workspace // 强制工作目录为 workspace
	// os.Environ() 返回当前进程的所有环境变量
	cmd.Env = os.Environ()

	// CombinedOutput 执行命令并返回 stdout + stderr 合并的字节数组
	output, err := cmd.CombinedOutput()

	if err != nil {
		// 检查是否超时
		if ctx.Err() == context.DeadlineExceeded {
			return string(output), fmt.Errorf("命令超时（30s）: %w", err)
		}
		// 其他错误：返回输出 + 错误信息
		return string(output), fmt.Errorf("命令执行失败: %w", err)
	}

	return string(output), nil
}
