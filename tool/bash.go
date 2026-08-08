package tool

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
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

// bashCommandDoc 描述一个或一组用途相近的白名单命令。
// 文档数据与格式化逻辑分离，后续增删命令时只需要维护下面的分类表。
type bashCommandDoc struct {
	Names       string
	Description string
	Examples    []string
}

// bashCommandCategory 按 Agent 的工作阶段组织命令，而不是按字母顺序堆叠命令名。
type bashCommandCategory struct {
	Title    string
	Guidance string
	Commands []bashCommandDoc
}

// bashCommandCategories 是发送给模型的结构化命令目录。
// 分类顺序刻意遵循“定位 → 搜索/读取 → 处理 → 修改 → 验证/诊断 → 联网”，
// 让模型优先选择低风险、低输出量的命令。
var bashCommandCategories = []bashCommandCategory{
	{
		Title:    "工作区与文件定位（优先只读）",
		Guidance: "先确认当前目录、目录结构和目标文件位置，再读取或修改内容。",
		Commands: []bashCommandDoc{
			{
				Names:       "pwd",
				Description: "显示当前 Agent 工作目录。",
				Examples:    []string{`pwd`},
			},
			{
				Names:       "ls",
				Description: "列出目录内容；优先指定目标目录，避免递归输出过多。",
				Examples:    []string{`ls -la`, `ls src/`},
			},
			{
				Names:       "find",
				Description: "按文件名或路径查找文件；禁止 -exec、-execdir、-ok、-okdir。",
				Examples:    []string{`find . -name "*.go"`, `find src -type f -name "*_test.go"`},
			},
		},
	},
	{
		Title:    "文本搜索与内容读取（优先使用）",
		Guidance: "优先搜索定位，再只读取必要片段；不要一开始就输出整个大文件。",
		Commands: []bashCommandDoc{
			{
				Names:       "rg",
				Description: "首选全文搜索工具。命令名必须写 rg，不能写 ripgrep。",
				Examples:    []string{`rg -n "NewBashTool" .`, `rg -l "TODO" . | head -50`},
			},
			{
				Names:       "grep",
				Description: "基础文本搜索，适合过滤文件或管道输出。",
				Examples:    []string{`grep -n "error" app.log | head -50`},
			},
			{
				Names:       "cat",
				Description: "读取较小文件；使用 -n 显示行号。大文件优先改用 head、tail 或 sed。",
				Examples:    []string{`cat -n README.md | head -120`},
			},
			{
				Names:       "head / tail",
				Description: "读取文件或管道输出的开头/结尾，用于限制返回内容规模。",
				Examples:    []string{`head -100 README.md`, `tail -80 app.log`},
			},
			{
				Names:       "sed",
				Description: "读取指定行区间或执行文本替换；使用 -i 时会修改文件。",
				Examples:    []string{`sed -n '40,90p' src/main.go`, `sed 's/foo/bar/g' input.txt`},
			},
			{
				Names:       "wc",
				Description: "统计行数、单词数或字节数。",
				Examples:    []string{`wc -l src/main.go`, `rg -l "TODO" . | wc -l`},
			},
		},
	},
	{
		Title:    "文本转换与管道处理",
		Guidance: "通过单个 | 连接多个小步骤；每个管道段都必须以白名单命令开头。",
		Commands: []bashCommandDoc{
			{
				Names:       "sort",
				Description: "对文本行排序。",
				Examples:    []string{`sort names.txt`},
			},
			{
				Names:       "uniq",
				Description: "对相邻重复行去重或计数，通常接在 sort 后面。",
				Examples:    []string{`sort names.txt | uniq -c`},
			},
			{
				Names:       "awk",
				Description: "按字段或规则处理结构化文本。包含 $ 的 awk 程序必须用单引号包裹。",
				Examples:    []string{`awk '{print $1}' input.txt | sort | uniq`},
			},
			{
				Names:       "xargs",
				Description: "把管道输入作为参数传给另一个命令；目标命令也必须在白名单内。",
				Examples:    []string{`find . -name "*.log" | xargs rm`},
			},
		},
	},
	{
		Title:    "兼容性文件操作（CRUD 优先使用 file 工具）",
		Guidance: "新增、读取、修改、删除文件时优先调用 file 工具；这里只保留复制、移动和少量批处理兼容能力。删除、覆盖或批量操作前先缩小路径范围。",
		Commands: []bashCommandDoc{
			{
				Names:       "mkdir",
				Description: "创建目录。",
				Examples:    []string{`mkdir -p tmp/output`},
			},
			{
				Names:       "touch",
				Description: "创建空文件或更新时间。",
				Examples:    []string{`touch tmp/empty.txt`},
			},
			{
				Names:       "cp",
				Description: "复制文件或目录。",
				Examples:    []string{`cp config.example.json config.json`},
			},
			{
				Names:       "mv",
				Description: "移动文件或重命名。",
				Examples:    []string{`mv old_name.txt new_name.txt`},
			},
			{
				Names:       "rm",
				Description: "删除文件或目录；使用前确认目标路径，不要无必要地扩大匹配范围。",
				Examples:    []string{`rm -f tmp/empty.txt`},
			},
			{
				Names:       "echo",
				Description: "输出短文本。普通文件重定向被禁止，不能用 echo ... > file 写文件。",
				Examples:    []string{`echo "build complete"`},
			},
		},
	},
	{
		Title:    "版本控制、构建与脚本",
		Guidance: "用于检查变更、构建、测试或执行确定的脚本；修改后优先进行验证。",
		Commands: []bashCommandDoc{
			{
				Names:       "git",
				Description: "版本控制、状态检查、日志和差异查看。",
				Examples:    []string{`git status --short`, `git diff -- src/main.go`, `git log --oneline -20`},
			},
			{
				Names:       "go",
				Description: "Go 构建、测试、格式化和其他 Go 工具链操作。",
				Examples:    []string{`go test ./...`, `go fmt ./...`, `go build ./...`},
			},
			{
				Names:       "python",
				Description: "运行 Python 3 脚本或一次性处理逻辑；代码中的 shell 元字符仍须遵守引号规则。",
				Examples:    []string{`python script.py`, `python -c "from pathlib import Path; print(Path('README.md').exists())"`},
			},
			{
				Names:       "uv",
				Description: "运行 Python 项目、脚本或管理 Python 包。",
				Examples:    []string{`uv run script.py`, `uv pip install requests`},
			},
		},
	},
	{
		Title:    "Windows 系统与进程诊断",
		Guidance: "这些命令运行在 Windows 宿主机上，但仍通过 Git Bash 调用。",
		Commands: []bashCommandDoc{
			{
				Names:       "which",
				Description: "在 Git Bash 的 PATH 中查找命令。",
				Examples:    []string{`which python`},
			},
			{
				Names:       "where",
				Description: "调用 Windows where.exe 查找可执行文件。",
				Examples:    []string{`where git.exe`},
			},
			{
				Names:       "tasklist",
				Description: "查看 Windows 进程列表。",
				Examples:    []string{`tasklist | rg "python|go"`},
			},
			{
				Names:       "netstat",
				Description: "查看 Windows 网络连接、监听端口和进程 PID。",
				Examples:    []string{`netstat -ano | rg ":8080"`},
			},
			{
				Names:       "diff",
				Description: "比较两个文件。",
				Examples:    []string{`diff expected.txt actual.txt`},
			},
		},
	},
	{
		Title:    "网络访问与下载（仅在任务需要联网时）",
		Guidance: "网络命令可能访问外部服务或创建下载文件，应明确目标地址和输出路径。",
		Commands: []bashCommandDoc{
			{
				Names:       "curl",
				Description: "发起 HTTP 请求或获取远程内容。",
				Examples:    []string{`curl -s https://example.com`},
			},
		},
	},
}

const bashToolEnvironmentDescription = `【执行环境】
- 宿主系统：Windows。
- Shell：仅使用 Git for Windows 自带的 bash.exe；绝不调用 WSL、wsl.exe、System32\bash.exe、PowerShell 或 cmd.exe。
- 当前目录：本次 Agent 的项目工作目录；相对路径均从这里解析。
- 路径写法：优先使用相对路径和正斜杠，例如 src/main.go、C:/work/project、/c/work/project。不要使用 WSL 的 /mnt/c/... 路径。
- 不要使用 wsl、bash、sh、PowerShell 命令（如 Get-ChildItem、Select-String）或 cmd.exe 专用语法（如 dir、copy、%VAR%）。
- 命令名按白名单精确匹配，大小写不敏感；允许路径前缀以及 .exe/.bat 后缀，但不支持别名、项目名或模糊纠正。

【推荐工作流】
1. 定位：pwd、ls、find。
2. 搜索：优先 rg，先找位置再读内容。
3. 阅读：head、tail、sed、cat，只读取必要片段。
4. 处理：grep、sort、uniq、wc、awk、sed，需要时使用管道。
5. 修改：仅在任务要求时使用文件操作、脚本或带写入效果的参数。
6. 文件 CRUD：新增、读取、精确替换和删除优先使用 file 工具，避免为简单文件操作启动 shell。
7. 验证：使用 diff、git status、git diff、go test 等检查结果。
`

const bashToolSafetyDescription = `【管道、引号与安全规则】
- 一次调用只提交一条命令行；不要附带 Markdown 代码块、命令提示符、解释文字、备用命令或未转义换行。
- 允许用单个 | 连接白名单命令；每个管道段都会单独校验。禁止 ||。
- 大输出必须主动限制，例如 rg ... | head -50、git log --oneline -20。
- 只允许以下输出重定向：>/dev/null、1>/dev/null、2>/dev/null，以及 2>&1。
- 禁止 ;、&&、&、<、普通文件重定向 >、追加重定向 >>、here-doc 和多行命令。
- 含空格或 shell 元字符的参数应加引号。单引号内容按字面量处理。
- 双引号内的 $ 和反引号仍会触发展开，因此会被拒绝；需要搜索这些字符时使用单引号或反斜杠转义。
- 禁止 find 的 -exec、-execdir、-ok、-okdir。
- xargs 的目标命令必须仍在白名单中。
- 命令被拒绝时，根据错误信息修改命令，不要原样重复失败调用。

安全输出示例：
  rg -n "关键词" . 2>/dev/null | head -100
  go test ./... 2>&1 | tail -80
  rg -n 'price$' .
`

// buildBashToolDescription 把结构化分类表格式化为稳定、便于模型扫描的文本。
func buildBashToolDescription() string {
	var out strings.Builder
	out.WriteString("在当前 Agent 项目工作目录中执行一条经过白名单校验的 Git for Windows 命令。该工具只启动 Git for Windows 自带的 bash.exe，绝不启动 WSL。\n\n")
	out.WriteString(bashToolEnvironmentDescription)
	out.WriteString("\n【按任务分类的命令目录】\n")

	for categoryIndex, category := range bashCommandCategories {
		fmt.Fprintf(&out, "\n%d. %s\n", categoryIndex+1, category.Title)
		fmt.Fprintf(&out, "用途：%s\n", category.Guidance)

		for _, command := range category.Commands {
			fmt.Fprintf(&out, "- %s：%s\n", command.Names, command.Description)
			for _, example := range command.Examples {
				fmt.Fprintf(&out, "  例：%s\n", example)
			}
		}
	}

	out.WriteString("\n")
	out.WriteString(bashToolSafetyDescription)
	return out.String()
}

// collectBashCommandExamples 从各分类轮流提取示例，避免 Schema 示例被前几个
// 只读命令占满，保证定位、搜索、修改、构建、Windows 诊断和联网任务都有覆盖。
func collectBashCommandExamples(limit int) []string {
	seen := make(map[string]bool)
	examples := make([]string, 0, limit)

	maxCommands := 0
	for _, category := range bashCommandCategories {
		if len(category.Commands) > maxCommands {
			maxCommands = len(category.Commands)
		}
	}

	for commandIndex := 0; commandIndex < maxCommands; commandIndex++ {
		for _, category := range bashCommandCategories {
			if commandIndex >= len(category.Commands) {
				continue
			}

			command := category.Commands[commandIndex]
			if len(command.Examples) == 0 {
				continue
			}

			example := command.Examples[0]
			if seen[example] {
				continue
			}
			seen[example] = true
			examples = append(examples, example)
			if limit > 0 && len(examples) >= limit {
				return examples
			}
		}
	}

	return examples
}

var (
	bashToolDescription = buildBashToolDescription()
	bashCommandExamples = collectBashCommandExamples(16)
)

// Description 返回工具描述，会发给 LLM。
func (b *BashTool) Description() string {
	return bashToolDescription
}

// InputSchema 返回 bash 工具的参数 schema，发给 API。
func (b *BashTool) InputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"title":                "Windows Git Bash 白名单命令",
		"description":          "在当前 Agent 项目目录执行一条 Git for Windows Bash 命令。只使用 Git for Windows 自带 bash.exe，绝不启动 WSL。先按任务分类选择命令，再遵守白名单、Windows 路径、引号、管道和重定向规则。",
		"additionalProperties": false,
		"properties": map[string]any{
			"command": map[string]any{
				"type":      "string",
				"title":     "单条 Git Bash 命令行",
				"minLength": 1,
				"description": `只填写要执行的命令本身，不要包含 Markdown、解释、提示符或换行。
环境是 Windows + Git for Windows Bash，绝不使用 WSL；路径优先使用相对路径或正斜杠，不要使用 /mnt/c/...。
选择顺序：定位（pwd/ls/find）→ 搜索（rg）→ 阅读 → 构建/测试 → 验证。新增、读取、修改、删除文件优先使用 file 工具。
多阶段任务使用单个 |；每段必须以白名单命令开头，并限制大输出。
仅允许 >/dev/null、1>/dev/null、2>/dev/null 和 2>&1；禁止 ;、&&、&、||、<、普通 >、>> 和多行命令。
包含 $ 或反引号的字面内容使用单引号或反斜杠转义。`,
				"examples": bashCommandExamples,
			},
		},
		"required": []string{"command"},
		"examples": []map[string]any{
			{"command": `rg -n "TODO" . | head -50`},
			{"command": `sed -n '40,90p' src/main.go`},
			{"command": `git status --short`},
			{"command": `go test ./... 2>&1 | tail -80`},
			{"command": `netstat -ano | rg ":8080"`},
		},
	}
}

// allowedCommands 白名单 —— 只有在此列表中的命令才能执行。
var allowedCommands = map[string]bool{
	"git":      true,
	"rg":       true,
	"cat":      true,
	"ls":       true,
	"find":     true,
	"mkdir":    true,
	"rm":       true,
	"cp":       true,
	"mv":       true,
	"touch":    true,
	"echo":     true,
	"head":     true,
	"tail":     true,
	"wc":       true,
	"sort":     true,
	"uniq":     true,
	"grep":     true,
	"uv":       true,
	"python":   true,
	"go":       true,
	"pwd":      true, // 打印当前工作目录，纯只读
	"which":    true, // 定位命令路径（unix 风格），纯只读
	"where":    true, // 定位命令路径（windows 原生），纯只读
	"sed":      true, // 流式文本编辑/替换，和 grep/rg 配套使用
	"awk":      true, // 文本处理
	"diff":     true, // 比较文件差异，纯只读
	"tasklist": true, // 查看进程列表（windows 原生），纯只读
	"netstat":  true, // 查看网络连接/端口占用（windows 原生），纯只读
	"xargs":    true, // 管道批量执行；实际调用的子命令在 Execute 里单独做白名单校验
	"curl":     true, // HTTP 请求，涉及联网，谨慎使用
}

// blockedShellCommands 明确列出禁止启动的二级 shell、WSL 启动器和其他命令解释器。
// 即使未来白名单被误改，这些命令也会优先拒绝，避免再次拉起 WSL 或绕过校验。
var blockedShellCommands = map[string]bool{
	"wsl":           true,
	"wslconfig":     true,
	"bash":          true,
	"sh":            true,
	"zsh":           true,
	"cmd":           true,
	"powershell":    true,
	"pwsh":          true,
	"ubuntu":        true,
	"ubuntu2004":    true,
	"ubuntu2204":    true,
	"ubuntu2404":    true,
	"debian":        true,
	"kali":          true,
	"opensuse":      true,
	"opensuse-leap": true,
}

var (
	gitBashLookupOnce sync.Once
	gitBashExecutable string
	gitBashLookupErr  error
)

// findGitBashExecutable 只查找 Git for Windows 安装目录中的 bash.exe。
// 故意不调用 exec.LookPath("bash")，因为 Windows PATH 中的 bash.exe 可能是
// System32 下的旧 WSL 启动器，从而造成高延迟和额外内存占用。
func findGitBashExecutable() (string, error) {
	gitBashLookupOnce.Do(func() {
		gitBashExecutable, gitBashLookupErr = discoverGitBashExecutable()
	})
	return gitBashExecutable, gitBashLookupErr
}

func discoverGitBashExecutable() (string, error) {
	var candidates []string

	// 允许部署环境显式指定 Git for Windows 的 bash.exe。
	if configured := strings.TrimSpace(os.Getenv("GIT_BASH_EXE")); configured != "" {
		candidates = append(candidates, configured)
	}

	// 优先从当前使用的 git.exe 反推出安装根目录，兼容系统安装和 PortableGit。
	gitPath, err := exec.LookPath("git.exe")
	if err != nil {
		gitPath, _ = exec.LookPath("git")
	}
	if gitPath != "" {
		gitPath, _ = filepath.Abs(gitPath)
		dir := filepath.Dir(gitPath)
		for depth := 0; depth < 6; depth++ {
			candidates = append(candidates,
				filepath.Join(dir, "bin", "bash.exe"),
				filepath.Join(dir, "usr", "bin", "bash.exe"),
			)
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	// Git for Windows 常见安装目录。这里只添加明确的 Git 安装路径，绝不添加
	// C:\\Windows\\System32\\bash.exe 或任何 WSL 路径。
	for _, base := range []string{
		os.Getenv("ProgramFiles"),
		os.Getenv("ProgramW6432"),
		os.Getenv("ProgramFiles(x86)"),
	} {
		if base == "" {
			continue
		}
		candidates = append(candidates,
			filepath.Join(base, "Git", "bin", "bash.exe"),
			filepath.Join(base, "Git", "usr", "bin", "bash.exe"),
		)
	}
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		candidates = append(candidates,
			filepath.Join(localAppData, "Programs", "Git", "bin", "bash.exe"),
			filepath.Join(localAppData, "Programs", "Git", "usr", "bin", "bash.exe"),
		)
	}

	seen := make(map[string]bool)
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		cleanKey := strings.ToLower(filepath.Clean(absolute))
		if seen[cleanKey] {
			continue
		}
		seen[cleanKey] = true

		// 双保险：明确排除 Windows 自带的 WSL bash 启动器位置。
		normalized := strings.ReplaceAll(cleanKey, "/", `\`)
		if strings.Contains(normalized, `\windows\system32\`) ||
			strings.Contains(normalized, `\windows\sysnative\`) {
			continue
		}

		info, err := os.Stat(absolute)
		if err == nil && !info.IsDir() && strings.EqualFold(filepath.Base(absolute), "bash.exe") {
			return absolute, nil
		}
	}

	return "", fmt.Errorf("未找到 Git for Windows 自带的 bash.exe；请安装 Git for Windows，或将 GIT_BASH_EXE 设置为其 bin/bash.exe 路径。为避免启动 WSL，本工具不会回退到 PATH 中的 bash.exe")
}

// isCharSafeInsideQuotes 判断字符 c 在当前引号状态下是否已经是普通字面量，
// 不会被 bash 特殊解释。
//   - 单引号内: 任何字符都是字面量（包括 $ 和反引号），bash 完全不做展开。
//   - 双引号内: ; & < > | 会失去特殊含义变成字面量，但 $ 和反引号仍然会触发
//     变量展开 / 命令替换，所以这两个字符即使在双引号里也必须继续拦截。
//
// 函数名和签名保持不变；反斜杠转义由外层扫描负责处理。
func isCharSafeInsideQuotes(c rune, inSingle, inDouble bool) bool {
	if inSingle {
		return true
	}
	if inDouble {
		return c != '$' && c != '`'
	}
	return false
}

// skipInlineShellSpaces 跳过重定向操作符后允许出现的横向空白。
// 不跳过换行，避免借换行拼接第二条命令。
func skipInlineShellSpaces(cmd string, pos int) int {
	for pos < len(cmd) {
		switch cmd[pos] {
		case ' ', '\t':
			pos++
		default:
			return pos
		}
	}
	return pos
}

// isShellTokenBoundary 判断 pos 是否位于一个 shell token 的安全边界。
// 即使边界字符本身是禁止操作符，也只用于确认前面的安全模式完整匹配；
// 外层扫描仍会继续检查并拒绝该操作符。
func isShellTokenBoundary(cmd string, pos int) bool {
	if pos >= len(cmd) {
		return true
	}

	r, _ := utf8.DecodeRuneInString(cmd[pos:])
	return unicode.IsSpace(r) || strings.ContainsRune("|;&<>", r)
}

// hasFD2ImmediatelyBefore 判断重定向操作符前是否紧邻独立的文件描述符 2。
// 例如 "2>&1" 返回 true，而 "12>&1"、"foo2>&1" 和 ">&1" 返回 false。
func hasFD2ImmediatelyBefore(cmd string, redirectPos int) bool {
	fdPos := redirectPos - 1
	if fdPos < 0 || cmd[fdPos] != '2' {
		return false
	}
	if fdPos == 0 {
		return true
	}

	previous, _ := utf8.DecodeLastRuneInString(cmd[:fdPos])
	return unicode.IsSpace(previous) || strings.ContainsRune("|;&<>", previous)
}

// consumeAllowedRedirection 检查 redirectPos 处是否为允许的安全输出重定向。
//
// 放行：
//   - >/dev/null、1>/dev/null、2>/dev/null，以及 > /dev/null
//     （实际上任意独立文件描述符写入 /dev/null 都是安全的）
//   - 2>&1
//
// 拒绝所有普通文件写入、输入重定向、追加写入、here-doc、进程替换等形式。
func consumeAllowedRedirection(cmd string, redirectPos int) (next int, ok bool) {
	if redirectPos < 0 || redirectPos >= len(cmd) || cmd[redirectPos] != '>' {
		return redirectPos, false
	}

	// 只允许标准错误重定向到标准输出：2>&1。
	if hasFD2ImmediatelyBefore(cmd, redirectPos) && strings.HasPrefix(cmd[redirectPos:], ">&1") {
		end := redirectPos + len(">&1")
		if isShellTokenBoundary(cmd, end) {
			return end, true
		}
	}

	// 不允许 >>/dev/null；当前策略只放行普通覆盖式输出丢弃。
	if redirectPos+1 < len(cmd) && cmd[redirectPos+1] == '>' {
		return redirectPos, false
	}

	targetPos := skipInlineShellSpaces(cmd, redirectPos+1)
	const nullDevice = "/dev/null"
	if !strings.HasPrefix(cmd[targetPos:], nullDevice) {
		return redirectPos, false
	}

	end := targetPos + len(nullDevice)
	if !isShellTokenBoundary(cmd, end) {
		return redirectPos, false
	}
	return end, true
}

// removeTrailingIONumber 从用于白名单校验的管道段中移除紧邻重定向符的独立
// 文件描述符编号。原始命令不会被修改，只是避免把 "2>/dev/null" 中的 "2"
// 错当成 xargs 的目标命令或前置命令名。
func removeTrailingIONumber(segment []rune) []rune {
	end := len(segment)
	start := end
	for start > 0 && segment[start-1] >= '0' && segment[start-1] <= '9' {
		start--
	}
	if start == end {
		return segment
	}
	if start == 0 || unicode.IsSpace(segment[start-1]) {
		return segment[:start]
	}
	return segment
}

// splitPipelineSegments 对命令字符串做“引号感知”的安全扫描，并按顶层 | 切分成
// 若干管道段。返回的管道段仅用于白名单校验，其中已允许的安全重定向会被替换
// 为空白；真正执行时仍使用未经修改的原始命令。
//
// 规则：
//   - 单/双引号内的 shell 操作符视为参数字面量；但双引号内未转义的 $ 和反引号
//     仍会触发变量展开或命令替换，因此继续拒绝。
//   - 支持反斜杠转义，转义后的操作符按字面量处理。
//   - 顶层 ;、&、< 直接拒绝。
//   - 顶层 > 仅放行输出到 /dev/null 和 2>&1；其他形式拒绝。
//   - 顶层 | 用作管道分隔符，每一段都必须单独通过命令白名单校验。
//   - 顶层换行和回车直接拒绝，避免在同一个 command 字符串中追加第二条命令。
func splitPipelineSegments(cmdStr string) ([]string, error) {
	var (
		segment  []rune
		result   []string
		inSingle bool
		inDouble bool
		escaped  bool
	)

	for i := 0; i < len(cmdStr); {
		c, size := utf8.DecodeRuneInString(cmdStr[i:])

		// 单引号内反斜杠没有转义作用，只有下一个单引号会结束单引号。
		if inSingle {
			segment = append(segment, c)
			if c == '\'' {
				inSingle = false
			}
			i += size
			continue
		}

		// 上一个反斜杠将当前字符变成普通字面量。反斜杠 + 换行是 shell 的
		// 行续接，校验字符串里也直接移除，避免误判成第二条命令。
		if escaped {
			if c == '\n' {
				if len(segment) > 0 && segment[len(segment)-1] == '\\' {
					segment = segment[:len(segment)-1]
				}
			} else {
				segment = append(segment, c)
			}
			escaped = false
			i += size
			continue
		}

		if c == '\\' {
			segment = append(segment, c)
			escaped = true
			i += size
			continue
		}

		switch c {
		case '\'':
			if !inDouble {
				inSingle = true
			}
			segment = append(segment, c)
			i += size
			continue
		case '"':
			inDouble = !inDouble
			segment = append(segment, c)
			i += size
			continue
		}

		if !inDouble {
			if c == '\n' || c == '\r' {
				return nil, fmt.Errorf("命令包含未转义的换行符，不允许在一次调用中执行多条命令: %s", cmdStr)
			}

			if c == '>' {
				if next, allowed := consumeAllowedRedirection(cmdStr, i); allowed {
					segment = removeTrailingIONumber(segment)
					segment = append(segment, ' ')
					i = next
					continue
				}
			}
		}

		if c == '|' && !inDouble && i+size < len(cmdStr) && cmdStr[i+size] == '|' {
			return nil, fmt.Errorf("命令包含禁止操作符 ||（不允许条件链式执行）: %s", cmdStr)
		}

		switch {
		case c == '|' && !inDouble:
			result = append(result, string(segment))
			segment = segment[:0]
			i += size
			continue
		case (c == ';' || c == '&' || c == '<' || c == '>') && !inDouble:
			return nil, fmt.Errorf("命令包含禁止字符 %q（引号外仅允许管道，以及输出到 /dev/null 或 2>&1）: %s", string(c), cmdStr)
		case (c == '$' || c == '`') && !isCharSafeInsideQuotes(c, inSingle, inDouble):
			return nil, fmt.Errorf("命令包含禁止字符 %q（可能触发变量展开或命令替换，如需作为字面量使用请改用单引号包裹或反斜杠转义）: %s", string(c), cmdStr)
		}

		segment = append(segment, c)
		i += size
	}

	if escaped {
		return nil, fmt.Errorf("命令末尾包含未完成的反斜杠转义: %s", cmdStr)
	}
	if inSingle || inDouble {
		return nil, fmt.Errorf("命令中的引号未闭合: %s", cmdStr)
	}

	result = append(result, string(segment))
	return result, nil
}

// normalizeCommandName 把命令名归一化成白名单里能匹配的形式：
// 去掉路径前缀（/usr/bin/git、C:\tools\git.exe），去掉 Windows 常见的
// .exe / .bat 后缀，并统一转小写。
func normalizeCommandName(cmdName string) string {
	if i := strings.LastIndex(cmdName, "/"); i >= 0 {
		cmdName = cmdName[i+1:]
	}
	if i := strings.LastIndex(cmdName, "\\"); i >= 0 {
		cmdName = cmdName[i+1:]
	}
	cmdNameKey := strings.ToLower(cmdName)
	cmdNameKey = strings.TrimSuffix(cmdNameKey, ".exe")
	cmdNameKey = strings.TrimSuffix(cmdNameKey, ".bat")
	return cmdNameKey
}

// xargsValueFlags 是 xargs 里"会额外占用一个独立 token 作为参数值"的短选项，
// 比如 -I{} 这种粘在一起写不受影响，但单独写 "-I" "{}" 两个 token 时，
// 第二个 token 是 -I 的参数值，不能被误判成 xargs 要执行的目标命令。
var xargsValueFlags = map[byte]bool{
	'I': true, 'L': true, 'n': true, 'P': true,
	's': true, 'a': true, 'd': true, 'E': true,
}

// findXargsTargetCommand 在 xargs 的参数列表里找出它实际会执行的子命令名。
// 例如 xargs -I{} -P4 rm {} 里，跳过 -I{}、-P4 两个选项后，第一个非选项
// token "rm" 就是目标命令。如果 xargs 没有显式指定命令，它默认执行 echo，
// echo 已经在白名单里，视为安全。
//
// 局限：只识别常见短选项，不识别 --long-option=value 之外更复杂的 GNU
// long option 形式；遇到无法确定的情况会保守地跳过该 token，不会因此放过
// 危险命令（因为最终仍然要求找到的目标命令必须在白名单内）。
func findXargsTargetCommand(xargsParts []string) (string, bool) {
	i := 1 // parts[0] 是 "xargs" 本身
	for i < len(xargsParts) {
		tok := xargsParts[i]
		switch {
		case strings.HasPrefix(tok, "--"):
			i++
		case strings.HasPrefix(tok, "-") && len(tok) > 1:
			flagChar := tok[1]
			if xargsValueFlags[flagChar] && len(tok) == 2 {
				i += 2 // 形如 "-I" "{}"：选项和值是两个独立 token
			} else {
				i++ // 形如 "-I{}" "-P4" 或纯布尔开关：值已经粘在一个 token 里
			}
		default:
			return tok, true
		}
	}
	return "", false
}

func (b *BashTool) Execute(
	input map[string]any,
	executionEnvironment ToolExecutionEnvironment,
) (string, error) {
	if executionEnvironment.WorkingDirectory == "" {
		return "", fmt.Errorf("BashTool 缺少 WorkingDirectory")
	}
	// 使用 ValidatePath 校验工作目录本身
	if _, err := ValidatePath(executionEnvironment.WorkingDirectory, "."); err != nil {
		return "", fmt.Errorf("BashTool 工作目录无效: %w", err)
	}
	cmdStr, ok := input["command"].(string)
	if !ok || strings.TrimSpace(cmdStr) == "" {
		return "", fmt.Errorf("bash 工具需要 command 参数")
	}

	// 安全检查 1+2：引号感知扫描危险元字符，并按顶层 | 切分成管道段
	segments, err := splitPipelineSegments(cmdStr)
	if err != nil {
		return "", err
	}

	// 安全检查 3：每一个管道段都必须以白名单命令开头
	// strings.Fields 按空白字符（空格/tab/换行）切分字符串，取第一个词作为命令名
	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		parts := strings.Fields(seg)
		if len(parts) == 0 {
			return "", fmt.Errorf("命令中存在空的管道段: %s", cmdStr)
		}

		cmdNameKey := normalizeCommandName(parts[0])
		if blockedShellCommands[cmdNameKey] {
			return "", fmt.Errorf("命令 %s 已禁用：不允许启动 WSL、二级 shell、PowerShell 或 cmd.exe（完整命令: %s）", parts[0], cmdStr)
		}
		if !allowedCommands[cmdNameKey] {
			return "", fmt.Errorf("不在白名单中: %s（完整命令: %s）。只能使用工具描述里列出的命令名，不接受项目名/别名等其他写法", parts[0], cmdStr)
		}

		// find 的 -exec/-execdir/-ok/-okdir 可以执行任意命令，且用 "+" 结尾时
		// 不含分号，能绕过上面的元字符扫描，因此单独禁止这几个参数。
		if cmdNameKey == "find" {
			for _, p := range parts[1:] {
				switch p {
				case "-exec", "-execdir", "-ok", "-okdir":
					return "", fmt.Errorf("find 不允许使用 %s 参数（可借此执行任意命令，绕过白名单）: %s", p, cmdStr)
				}
			}
		}

		// xargs 会把管道输入拼接成新命令去执行，必须额外校验它实际调用的
		// 目标子命令，否则可以借道 xargs 绕过白名单执行任意程序。
		if cmdNameKey == "xargs" {
			targetCmd, found := findXargsTargetCommand(parts)
			if found {
				targetCmdKey := normalizeCommandName(targetCmd)
				if !allowedCommands[targetCmdKey] {
					return "", fmt.Errorf("xargs 目标命令不在白名单中: %s（完整命令: %s）", targetCmd, cmdStr)
				}
			}
			// 没找到显式目标命令时 xargs 默认执行 echo，属于白名单内命令，放行
		}
	}

	// 创建带超时的 context
	// context.WithTimeout 返回一个新 context 和一个 cancel 函数
	// 30 秒后 context 自动超时，ExecCommandContext 会 kill 进程
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel() // 函数返回时释放 context 资源

	// 只使用 Git for Windows 自带的 bash.exe。绝不通过 PATH 查找 bash，
	// 避免误启动 C:\Windows\System32\bash.exe / WSL。
	gitBashPath, err := findGitBashExecutable()
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, gitBashPath, "--noprofile", "--norc", "-c", cmdStr)
	cmd.Dir = executionEnvironment.WorkingDirectory
	// CHERE_INVOKING 保持 cmd.Dir 指定的工作目录；禁用用户 profile 也能减少启动开销
	// 和不可控的别名/脚本注入。
	cmd.Env = append(os.Environ(), "CHERE_INVOKING=1")

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