package tool

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// BashTool 提供在本次 Agent 工作目录内执行白名单命令的能力。
// 通过实现 Name()、Description()、Execute() 三个方法，
// 隐式地实现了 Tool 接口。
type BashTool struct{}

// NewBashTool 创建一个不保存固定工作目录的 BashTool。
func NewBashTool() *BashTool {
	return &BashTool{}
}

// Name 返回工具名。
func (b *BashTool) Name() string {
	return "bash"
}

// Description 返回工具描述，会发给 LLM。
func (b *BashTool) Description() string {
	return `在本次 Agent 的项目工作目录内执行白名单 shell 命令。运行环境: Windows + Git Bash。

可用命令一览:
- rg: 超快文本搜索。rg "关键词" . 搜索所有文件；rg -n "关键词" . 显示行号；rg -l "关键词" . 只列文件名
- python: Python 3，适合复杂文本处理。python -c "一行代码"
- uv: Python 包管理。uv run script.py 运行脚本；uv pip install pkg 安装包
- cat: 显示文件内容。cat file.txt；cat -n file.txt 带行号
- head/tail: 看文件头尾。head -100 file.txt；tail -50 file.txt
- wc: 统计。wc -l file.txt 行数；wc -c file.txt 字节数
- grep: 文本搜索。grep -n "关键词" file.txt 带行号搜索
- ls: 列目录。ls -la；ls dir/
- find: 查找文件。find . -name "*.txt"
- git: 版本控制。git status；git log --oneline -20
- mkdir/rm/cp/mv/touch/echo: 基本文件操作

路径注意: Windows 路径用正斜杠 /，不要用反斜杠 \。相对路径基于工作目录。
大文件策略: 先用 rg 搜索关键词定位，再用 head/tail/python 读片段，不要 cat 整个大文件。
`
}

// InputSchema 返回 bash 工具的参数 schema，发给 API。
func (b *BashTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "要执行的命令（Windows Git Bash，路径用 /）。示例: rg \"关键词\" .、python -c \"...\"、cat -n file.txt、head -100 file.txt、ls -la",
			},
		},
		"required": []string{"command"},
	}
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
func (b *BashTool) Execute(
	input map[string]any,
	executionEnvironment ToolExecutionEnvironment,
) (string, error) {
	if executionEnvironment.WorkingDirectory == "" {
		return "", fmt.Errorf("BashTool 缺少 WorkingDirectory")
	}
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

	// 通过 bash -c 执行，让 echo/cat/ls 等 shell 内置命令也能用
	cmd := exec.CommandContext(ctx, "bash", "-c", cmdStr)
	cmd.Dir = executionEnvironment.WorkingDirectory
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
