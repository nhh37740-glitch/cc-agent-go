package tool

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	defaultCommandTimeoutSeconds = 60
	maxCommandTimeoutSeconds     = 300
	defaultCommandOutputBytes    = 256 << 10 // 256 KiB
	minCommandOutputBytes        = 4 << 10   // 4 KiB
	maxCommandOutputBytes        = 1 << 20   // 1 MiB
	maxWindowsCommandLineUnits   = 30_000    // 给 Windows 32767 UTF-16 单元限制留余量
)

// NativeCommandTool 在 Windows 上直接启动白名单内的原生 .exe。
//
// 它不会启动 Git Bash、WSL、PowerShell、cmd.exe 或任何其他命令解释器。
// program 与 args 分开传递给 exec.CommandContext，因此不存在 shell 管道、重定向、
// 变量展开、通配符展开或命令替换。
type NativeCommandTool struct{}

// BashTool 是旧类型名的兼容别名。
//
// Deprecated: 新代码请使用 NativeCommandTool 和 NewNativeCommandTool。
type BashTool = NativeCommandTool

// NewNativeCommandTool 创建 Windows 原生命令工具。
func NewNativeCommandTool() *NativeCommandTool {
	return &NativeCommandTool{}
}

// NewBashTool 保留旧注册代码的兼容性，但返回的工具不会运行 Bash。
//
// Deprecated: 新代码请使用 NewNativeCommandTool。
func NewBashTool() *NativeCommandTool {
	return NewNativeCommandTool()
}

// Name 返回工具名。名称改为 command，避免模型继续生成 Bash 语法。
func (t *NativeCommandTool) Name() string {
	return "command"
}

// nativeProgramDoc 描述一个允许直接启动的 Windows 原生程序。
type nativeProgramDoc struct {
	Name          string
	Executable    []string
	Category      string
	Description   string
	Guidance      string
	Examples      [][]string
	PreferTool    string
	UnavailableOK bool
}

// nativeProgramDocs 是执行白名单，也是发送给模型的结构化命令目录。
//
// 文件增删改查不放在这里，统一由 file 工具完成；Git 和 Go 的高层操作应优先
// 由对应结构化工具完成，这里仅保留直接调用原生 CLI 的兼容和兜底能力。
var nativeProgramDocs = []nativeProgramDoc{
	{
		Name:        "rg",
		Executable:  []string{"rg.exe"},
		Category:    "代码搜索",
		Description: "使用 ripgrep 在工作区内搜索文本或列出匹配文件。",
		Guidance:    "优先限制目录、文件类型和匹配数量；不要依赖管道截断输出。未找到匹配时 rg 返回退出码 1，这是正常结果，不要重复重试。",
		Examples: [][]string{
			{"-n", "--max-count", "50", "TODO", "."},
			{"-l", "--glob", "*.go", "NewNativeCommandTool", "."},
			{"--files", "--glob", "*.go"},
		},
	},
	{
		Name:        "git",
		Executable:  []string{"git.exe"},
		Category:    "版本控制",
		Description: "调用本机 Git for Windows 的 git.exe，不启动 Git Bash。",
		Guidance:    "优先使用结构化 Git 工具；直接调用时限制输出，并避免交互式子命令。",
		PreferTool:  "git",
		Examples: [][]string{
			{"status", "--short"},
			{"diff", "--", "internal/app/app.go"},
			{"log", "--oneline", "-20"},
		},
	},
	{
		Name:        "go",
		Executable:  []string{"go.exe"},
		Category:    "Go 开发",
		Description: "调用本机 Go 工具链进行构建、测试、检查和依赖查询。",
		Guidance:    "优先使用结构化 Go 工具；直接调用时让参数保持单一职责。",
		PreferTool:  "go",
		Examples: [][]string{
			{"test", "./..."},
			{"build", "./..."},
			{"vet", "./..."},
			{"list", "./..."},
		},
	},
	{
		Name:        "gofmt",
		Executable:  []string{"gofmt.exe"},
		Category:    "Go 开发",
		Description: "格式化指定 Go 源文件。",
		Guidance:    "修改文件后只格式化相关文件；批量格式化优先由结构化 Go 工具完成。",
		PreferTool:  "go",
		Examples: [][]string{
			{"-w", "internal/app/app.go"},
			{"-d", "internal/app/app.go"},
		},
	},
	{
		Name:          "python",
		Executable:    []string{"python.exe"},
		Category:      "脚本与辅助开发",
		Description:   "运行已知 Python 脚本或短小的一次性辅助逻辑。",
		Guidance:      "优先运行工作区内已有脚本；代码参数必须作为一个完整 args 元素传入。",
		UnavailableOK: true,
		Examples: [][]string{
			{"--version"},
			{"scripts/check.py"},
			{"-c", "from pathlib import Path; print(Path('README.md').exists())"},
		},
	},
	{
		Name:          "py",
		Executable:    []string{"py.exe"},
		Category:      "脚本与辅助开发",
		Description:   "使用 Windows Python Launcher 选择并运行 Python。",
		Guidance:      "仅在 python.exe 不可用或需要选择版本时使用。",
		UnavailableOK: true,
		Examples: [][]string{
			{"-3", "--version"},
			{"-3", "scripts/check.py"},
		},
	},
	{
		Name:          "uv",
		Executable:    []string{"uv.exe"},
		Category:      "脚本与辅助开发",
		Description:   "运行 Python 项目、脚本和依赖相关命令。",
		Guidance:      "安装或升级依赖会修改环境，应只在任务明确需要时执行。",
		UnavailableOK: true,
		Examples: [][]string{
			{"run", "scripts/check.py"},
			{"run", "python", "-m", "pytest"},
		},
	},
	{
		Name:          "curl",
		Executable:    []string{"curl.exe"},
		Category:      "网络诊断",
		Description:   "调用 Windows 自带或本机安装的 curl.exe 发起 HTTP 请求。",
		Guidance:      "明确 URL、方法和输出规模；下载文件优先交给专门的网络或文件工具。",
		UnavailableOK: true,
		Examples: [][]string{
			{"--silent", "--show-error", "https://example.com"},
			{"--head", "https://example.com"},
		},
	},
	{
		Name:        "where",
		Executable:  []string{"where.exe"},
		Category:    "Windows 诊断",
		Description: "在 Windows PATH 中查找可执行文件。",
		Guidance:    "只用于确认本机程序位置，不要传递 shell 语法。",
		Examples: [][]string{
			{"git.exe"},
			{"go.exe"},
		},
	},
	{
		Name:        "tasklist",
		Executable:  []string{"tasklist.exe"},
		Category:    "Windows 诊断",
		Description: "列出 Windows 进程。",
		Guidance:    "使用 /FI 过滤结果，避免输出完整进程列表。",
		Examples: [][]string{
			{"/FI", "IMAGENAME eq go.exe"},
			{"/FI", "PID eq 1234"},
		},
	},
	{
		Name:        "netstat",
		Executable:  []string{"netstat.exe"},
		Category:    "Windows 诊断",
		Description: "查看 Windows 网络连接、监听端口和 PID。",
		Guidance:    "没有 shell 管道；使用 -ano 获取数据后由调用方分析返回文本。",
		Examples: [][]string{
			{"-ano"},
		},
	},
	{
		Name:        "ipconfig",
		Executable:  []string{"ipconfig.exe"},
		Category:    "Windows 诊断",
		Description: "查看 Windows 网络适配器和 IP 配置。",
		Guidance:    "通常先使用 /all；不要用 cmd.exe 的管道或重定向。",
		Examples: [][]string{
			{"/all"},
		},
	},
}

var nativeProgramByName = buildNativeProgramIndex()

func buildNativeProgramIndex() map[string]nativeProgramDoc {
	result := make(map[string]nativeProgramDoc, len(nativeProgramDocs))
	for _, doc := range nativeProgramDocs {
		result[doc.Name] = doc
	}
	return result
}

func buildNativeCommandDescription() string {
	var out strings.Builder
	out.WriteString("在当前 Windows Agent 工作目录中直接执行一个白名单内的原生 .exe。")
	out.WriteString("不启动 Git Bash、WSL、PowerShell、cmd.exe 或其他命令解释器。\n\n")

	out.WriteString("【参数模型】\n")
	out.WriteString("- program：只填写白名单程序名，例如 rg、git、go；不要填写路径或 .bat/.cmd/.ps1。\n")
	out.WriteString("- args：字符串数组，每个元素就是传给程序的一个独立参数。不要把整条命令写成一个字符串。\n")
	out.WriteString("- 含空格的参数仍然只占一个数组元素，不要额外添加引号。\n")
	out.WriteString("- timeout_seconds：可选，1 到 300 秒；默认 60 秒。\n")
	out.WriteString("- max_output_bytes：可选，4 KiB 到 1 MiB；默认 256 KiB，超出后截断。\n\n")

	out.WriteString("【与 Shell 的关键差异】\n")
	out.WriteString("- 不支持 |、>、>>、<、&&、||、;、通配符展开、$变量、反引号或命令替换。\n")
	out.WriteString("- 这些字符出现在 args 中时只是普通字符，不会被解释为操作符。\n")
	out.WriteString("- 需要组合多个步骤时分多次调用工具；需要搜索过滤时优先使用 rg 自身参数。\n")
	out.WriteString("- stdout 和 stderr 由工具直接捕获，不需要 2>&1 或重定向。\n")
	out.WriteString("- 子进程原始输出会在工具边界统一转换为有效 UTF-8；UTF-16 会解码，无效字节会转义为 \\xNN，绝不会把非法字节送入 API。\n")
	out.WriteString("- 子进程返回非零退出码时，工具仍会正常返回结构化结果，包含 status、exit_code 和 stdout/stderr；这表示命令本身失败，不是工具调用失败。\n")
	out.WriteString("- rg 未找到匹配项时退出码为 1，结果会明确标记为 no_match，不应使用相同参数重复重试。\n")
	out.WriteString("- 文件 create/read/update/delete/list/stat 一律优先使用 file 工具。\n")
	out.WriteString("- Git 和 Go 高层操作优先使用结构化 git/go 工具；command 只作为原生 CLI 兜底。\n\n")

	categories := make([]string, 0)
	seenCategory := make(map[string]bool)
	for _, doc := range nativeProgramDocs {
		if !seenCategory[doc.Category] {
			seenCategory[doc.Category] = true
			categories = append(categories, doc.Category)
		}
	}

	out.WriteString("【按任务分类的程序目录】\n")
	for categoryIndex, category := range categories {
		fmt.Fprintf(&out, "\n%d. %s\n", categoryIndex+1, category)
		for _, doc := range nativeProgramDocs {
			if doc.Category != category {
				continue
			}
			fmt.Fprintf(&out, "- %s：%s\n", doc.Name, doc.Description)
			fmt.Fprintf(&out, "  使用建议：%s\n", doc.Guidance)
			if doc.PreferTool != "" {
				fmt.Fprintf(&out, "  优先工具：%s\n", doc.PreferTool)
			}
			for _, exampleArgs := range doc.Examples {
				fmt.Fprintf(&out, "  例：%s\n", formatNativeExample(doc.Name, exampleArgs))
			}
		}
	}

	out.WriteString("\n【禁止事项】\n")
	out.WriteString("- 禁止启动 bash、sh、wsl、pwsh、powershell、cmd、cscript、wscript 等解释器。\n")
	out.WriteString("- 禁止把 executable 路径、脚本路径或多条命令塞进 program。\n")
	out.WriteString("- 不要使用 ls/cat/find/grep/sed/awk/rm/cp/mv 等 Git Bash 命令；文件操作使用 file，搜索使用 rg。\n")
	out.WriteString("- 不要假设可选程序已经安装；找不到时应根据错误信息选择其他工具。\n")

	return out.String()
}

func formatNativeExample(program string, args []string) string {
	var out strings.Builder
	fmt.Fprintf(&out, `{"program":%q,"args":[`, program)
	for i, arg := range args {
		if i > 0 {
			out.WriteByte(',')
		}
		fmt.Fprintf(&out, "%q", arg)
	}
	out.WriteString("]}")
	return out.String()
}

var nativeCommandDescription = buildNativeCommandDescription()

// Description 返回结构化工具说明。
func (t *NativeCommandTool) Description() string {
	if runtime.GOOS == "linux" {
		return linuxCommandDescription
	}
	return nativeCommandDescription
}

func allowedProgramNames() []string {
	names := make([]string, 0, len(nativeProgramDocs))
	for _, doc := range nativeProgramDocs {
		names = append(names, doc.Name)
	}
	sort.Strings(names)
	return names
}

// InputSchema 返回原生命令工具的 JSON Schema。
func (t *NativeCommandTool) InputSchema() map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"title":                "Windows 原生 EXE 命令",
		"description":          "直接执行一个白名单内的 Windows 原生 .exe，不启动 Git Bash、WSL、PowerShell 或 cmd.exe。program 与 args 必须分开填写。文件操作优先使用 file；Git/Go 高层操作优先使用对应结构化工具。",
		"additionalProperties": false,
		"properties": map[string]any{
			"program": map[string]any{
				"type":        "string",
				"title":       "原生程序",
				"description": "白名单程序名，不含路径。只允许 enum 中的值；工具会在 Windows PATH 中解析对应 .exe。",
				"enum":        allowedProgramNames(),
			},
			"args": map[string]any{
				"type":        "array",
				"title":       "参数数组",
				"description": "每个数组元素对应一个独立命令行参数。不要加入 shell 引号，不要把整条命令放进一个元素，也不要使用管道或重定向。",
				"default":     []string{},
				"maxItems":    256,
				"items": map[string]any{
					"type":      "string",
					"maxLength": 16_384,
				},
			},
			"timeout_seconds": map[string]any{
				"type":        "integer",
				"title":       "超时秒数",
				"description": "可选。程序运行超时，默认 60 秒。",
				"minimum":     1,
				"maximum":     maxCommandTimeoutSeconds,
				"default":     defaultCommandTimeoutSeconds,
			},
			"max_output_bytes": map[string]any{
				"type":        "integer",
				"title":       "最大输出字节数",
				"description": "可选。stdout 与 stderr 合并后的最大保留字节数；超出部分会丢弃并追加截断提示。",
				"minimum":     minCommandOutputBytes,
				"maximum":     maxCommandOutputBytes,
				"default":     defaultCommandOutputBytes,
			},
		},
		"required": []string{"program", "args"},
		"examples": []map[string]any{
			{"program": "rg", "args": []string{"-n", "--max-count", "50", "TODO", "."}},
			{"program": "git", "args": []string{"status", "--short"}},
			{"program": "go", "args": []string{"test", "./..."}, "timeout_seconds": 120},
			{"program": "where", "args": []string{"git.exe"}},
			{"program": "tasklist", "args": []string{"/FI", "IMAGENAME eq go.exe"}},
		},
	}
	if runtime.GOOS == "linux" {
		schema["title"] = "Linux 原生命令"
		schema["description"] = linuxCommandDescription
		properties := schema["properties"].(map[string]any)
		programProperty := properties["program"].(map[string]any)
		programProperty["title"] = "Linux 程序"
		programProperty["description"] = "白名单程序名，不含路径；直接执行程序，不启动 shell。"
		programProperty["enum"] = allowedLinuxProgramNames()
		schema["examples"] = []map[string]any{
			{"program": "rg", "args": []string{"-n", "TODO", "."}},
			{"program": "git", "args": []string{"status", "--short"}},
			{"program": "go", "args": []string{"test", "./..."}, "timeout_seconds": 120},
		}
	}
	return schema
}

// blockedNativePrograms 即使误加入白名单，也不能作为二级解释器启动。
var blockedNativePrograms = map[string]bool{
	"bash":       true,
	"sh":         true,
	"zsh":        true,
	"wsl":        true,
	"wslconfig":  true,
	"pwsh":       true,
	"powershell": true,
	"cmd":        true,
	"cscript":    true,
	"wscript":    true,
	"mshta":      true,
	"rundll32":   true,
	"regsvr32":   true,
}

func normalizeProgramName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	name = strings.TrimSuffix(name, ".exe")
	return name
}

func resolveNativeExecutable(doc nativeProgramDoc) (string, error) {
	for _, executableName := range doc.Executable {
		path, err := exec.LookPath(executableName)
		if err != nil {
			continue
		}

		absolutePath, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		info, err := os.Stat(absolutePath)
		if err != nil || info.IsDir() {
			continue
		}

		// 只执行真正的 Windows EXE；不接受 .bat、.cmd、.ps1、.com 等会引入
		// 额外解释层或不同执行语义的文件类型。
		if !strings.EqualFold(filepath.Ext(absolutePath), ".exe") {
			continue
		}
		return absolutePath, nil
	}

	if doc.UnavailableOK {
		return "", fmt.Errorf("本机未找到可选程序 %s.exe；请安装后重试，或改用现有结构化工具", doc.Name)
	}
	return "", fmt.Errorf("本机未找到必需程序 %s.exe；请确认它已安装并位于 Windows PATH 中", doc.Name)
}

// validateNativeUTF8Input 在调用任何系统 API 前校验所有来自工具输入的字符串。
// JSON 理论上应当是 Unicode，但这里仍然在执行边界做一次防御性检查，避免
// 非标准上游把包含非法 UTF-8 字节的 Go string 传进来。
func validateNativeUTF8Input(field, value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s 包含无效 UTF-8，已拒绝执行", field)
	}
	return nil
}

func readOptionalInt(input map[string]any, key string, defaultValue, minimum, maximum int) (int, error) {
	value, exists := input[key]
	if !exists || value == nil {
		return defaultValue, nil
	}

	var parsed int
	switch typed := value.(type) {
	case int:
		parsed = typed
	case int32:
		parsed = int(typed)
	case int64:
		parsed = int(typed)
	case float64:
		if typed != float64(int(typed)) {
			return 0, fmt.Errorf("%s 必须是整数", key)
		}
		parsed = int(typed)
	default:
		return 0, fmt.Errorf("%s 必须是整数", key)
	}

	if parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("%s 必须在 %d 到 %d 之间", key, minimum, maximum)
	}
	return parsed, nil
}

func readStringArguments(input map[string]any) ([]string, error) {
	value, exists := input["args"]
	if !exists || value == nil {
		return nil, fmt.Errorf("command 工具需要 args 参数；没有参数时请传空数组 []")
	}

	var args []string
	switch typed := value.(type) {
	case []string:
		args = append([]string(nil), typed...)
	case []any:
		args = make([]string, 0, len(typed))
		for index, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("args[%d] 必须是字符串", index)
			}
			args = append(args, text)
		}
	default:
		return nil, fmt.Errorf("args 必须是字符串数组")
	}

	if len(args) > 256 {
		return nil, fmt.Errorf("args 最多允许 256 个参数")
	}

	commandLineUnits := 0
	for index, arg := range args {
		if strings.IndexByte(arg, 0) >= 0 {
			return nil, fmt.Errorf("args[%d] 包含 NUL 字符", index)
		}
		if !utf8.ValidString(arg) {
			return nil, fmt.Errorf("args[%d] 不是有效 UTF-8 字符串", index)
		}
		if len(arg) > 16_384 {
			return nil, fmt.Errorf("args[%d] 过长，最多允许 16384 字节", index)
		}
		commandLineUnits += len([]rune(arg)) + 3 // 参数本身，加引号和分隔符的保守估算
	}
	if commandLineUnits > maxWindowsCommandLineUnits {
		return nil, fmt.Errorf("参数总长度过大，可能超过 Windows 命令行限制")
	}

	return args, nil
}

func prepareNativeArgs(program string, args []string) []string {
	prepared := append([]string(nil), args...)

	// 禁用 Git 分页器，避免等待交互或启动额外程序。
	if program == "git" {
		for _, arg := range prepared {
			if arg == "--no-pager" || arg == "--paginate" || arg == "-p" {
				return prepared
			}
		}
		prepared = append([]string{"--no-pager"}, prepared...)
	}

	return prepared
}

func sanitizedNativeEnvironment(program string) []string {
	env := append([]string(nil), os.Environ()...)
	env = append(env,
		"NO_COLOR=1",
		"TERM=dumb",
		"PYTHONUTF8=1",
		"PYTHONIOENCODING=utf-8",
		"RUST_BACKTRACE=0",
	)

	if program == "git" {
		env = append(env,
			"GIT_TERMINAL_PROMPT=0",
			"GIT_PAGER=",
			"PAGER=",
		)
	}
	return env
}

// formatNativeCommandOutcome 将子进程的退出状态和输出整理成稳定、可读的结果。
//
// 子进程非零退出码表示“命令已经运行，但命令所做的事情没有成功”，例如：
//   - go test 编译失败或测试失败；
//   - git diff --exit-code 检测到差异；
//   - where 没有找到程序。
//
// 这类情况不属于工具基础设施错误，因此应作为普通工具结果返回给 Agent，
// 避免上层只显示 error.Error() 而丢弃 stdout/stderr。
func formatNativeCommandOutcome(
	program string,
	args []string,
	executablePath string,
	workingDirectory string,
	status string,
	exitCode int,
	output string,
) string {
	var result strings.Builder
	result.WriteString("[command_result]\n")
	fmt.Fprintf(&result, "status: %s\n", status)
	fmt.Fprintf(&result, "program: %s\n", executableLabel(program))
	fmt.Fprintf(&result, "exit_code: %d\n", exitCode)
	fmt.Fprintf(&result, "executable: %s\n", executablePath)
	fmt.Fprintf(&result, "working_directory: %s\n", workingDirectory)
	fmt.Fprintf(&result, "args: %q\n", args)
	result.WriteString("output:\n")

	output = escapeInvalidNativeUTF8([]byte(output))
	if strings.TrimSpace(output) == "" {
		result.WriteString("(no output)\n")
	} else {
		result.WriteString(output)
		if !strings.HasSuffix(output, "\n") {
			result.WriteByte('\n')
		}
	}
	return escapeInvalidNativeUTF8([]byte(result.String()))
}

// formatNativeInfrastructureError 构造真正的工具执行错误。
// 必须把已经捕获的输出写进 error message，因为部分工具调度器在 error != nil 时
// 只展示 error.Error()，会丢弃 Execute 返回的第一个 string。
func formatNativeInfrastructureError(
	program string,
	args []string,
	executablePath string,
	workingDirectory string,
	message string,
	output string,
	err error,
) error {
	var detail strings.Builder
	fmt.Fprintf(&detail, "%s %s", executableLabel(program), message)
	fmt.Fprintf(&detail, "\nexecutable: %s", executablePath)
	fmt.Fprintf(&detail, "\nworking_directory: %s", workingDirectory)
	fmt.Fprintf(&detail, "\nargs: %q", args)
	if err != nil {
		fmt.Fprintf(&detail, "\nerror: %v", err)
	}
	output = escapeInvalidNativeUTF8([]byte(output))
	if strings.TrimSpace(output) != "" {
		detail.WriteString("\noutput:\n")
		detail.WriteString(output)
	}
	messageText := escapeInvalidNativeUTF8([]byte(detail.String()))
	if err != nil {
		// 保留原始 err 链，让 Agent 能用 errors.Is 识别 context.Canceled。
		return fmt.Errorf("%s: %w", messageText, err)
	}
	return errors.New(messageText)
}

// classifyNativeCommandResult 按程序自身的退出码约定解释执行结果。
//
// 只有“程序无法启动/等待”等基础设施问题才返回 error。只要进程成功启动并产生
// 退出码，即使退出码非零，也作为结构化结果返回 nil error，让 Agent 能看到完整
// stdout/stderr 并自行判断，而不是把命令失败误判为工具故障后机械重试。
func classifyNativeCommandResult(
	program string,
	args []string,
	executablePath string,
	workingDirectory string,
	runErr error,
	output string,
	commandContext context.Context,
) (string, error) {
	if runErr == nil {
		return formatNativeCommandOutcome(
			program,
			args,
			executablePath,
			workingDirectory,
			"ok",
			0,
			output,
		), nil
	}

	// 用户停止 / 会话取消优先于超时与退出码解释。
	if commandContext != nil && errors.Is(commandContext.Err(), context.Canceled) {
		return "", formatNativeInfrastructureError(
			program,
			args,
			executablePath,
			workingDirectory,
			"已被用户停止或会话取消",
			output,
			commandContext.Err(),
		)
	}
	if commandContext != nil && errors.Is(commandContext.Err(), context.DeadlineExceeded) {
		return "", formatNativeInfrastructureError(
			program,
			args,
			executablePath,
			workingDirectory,
			"执行超时",
			output,
			commandContext.Err(),
		)
	}

	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) {
		return "", formatNativeInfrastructureError(
			program,
			args,
			executablePath,
			workingDirectory,
			"启动或等待失败",
			output,
			runErr,
		)
	}

	exitCode := exitErr.ExitCode()

	// ripgrep：退出码 1 只表示没有匹配项，不是搜索错误。
	if program == "rg" && exitCode == 1 {
		return formatNativeCommandOutcome(
			program,
			args,
			executablePath,
			workingDirectory,
			"no_match",
			exitCode,
			output,
		), nil
	}

	// 进程已经成功启动，非零退出码属于命令结果而非工具故障。
	// 对 go test/build 来说，编译器或测试失败详情就在 output 中。
	return formatNativeCommandOutcome(
		program,
		args,
		executablePath,
		workingDirectory,
		"command_failed",
		exitCode,
		output,
	), nil
}

// cappedOutputWriter 保留指定字节数以内的输出，超出后继续接收但丢弃数据，
// 避免子进程因为输出管道阻塞。Stdout 和 Stderr 可能并发写入，因此需要加锁。
type cappedOutputWriter struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func newCappedOutputWriter(limit int) *cappedOutputWriter {
	return &cappedOutputWriter{limit: limit}
}

func (w *cappedOutputWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	originalLength := len(p)
	remaining := w.limit - w.buffer.Len()
	if remaining <= 0 {
		w.truncated = true
		return originalLength, nil
	}

	if len(p) > remaining {
		_, _ = w.buffer.Write(p[:remaining])
		w.truncated = true
		return originalLength, nil
	}

	_, _ = w.buffer.Write(p)
	return originalLength, nil
}

func (w *cappedOutputWriter) UTF8String() string {
	w.mu.Lock()
	raw := append([]byte(nil), w.buffer.Bytes()...)
	rawTruncated := w.truncated
	limit := w.limit
	w.mu.Unlock()

	text, encodingNote := normalizeNativeOutputUTF8(raw)
	text, normalizedTruncated := truncateNativeUTF8(text, limit)

	var result strings.Builder
	result.WriteString(text)
	if encodingNote != "" {
		if result.Len() > 0 && !strings.HasSuffix(result.String(), "\n") {
			result.WriteByte('\n')
		}
		result.WriteString("[输出编码处理：")
		result.WriteString(encodingNote)
		result.WriteString("]\n")
	}
	if rawTruncated || normalizedTruncated {
		if result.Len() > 0 && !strings.HasSuffix(result.String(), "\n") {
			result.WriteByte('\n')
		}
		fmt.Fprintf(&result, "[输出已截断：最多保留 %d 字节的 UTF-8 文本]\n", limit)
	}

	// 最后的不变量检查。即使以后修改了解码逻辑，也不允许非法 UTF-8 离开工具。
	finalText := result.String()
	if utf8.ValidString(finalText) {
		return finalText
	}
	return escapeInvalidNativeUTF8([]byte(finalText))
}

// normalizeNativeOutputUTF8 把任意子进程字节流转换成 API 可安全接收的 UTF-8。
//
// 处理顺序：
//  1. 识别 UTF-8 BOM；
//  2. 识别 UTF-16LE/BE BOM，以及明显的无 BOM UTF-16 文本；
//  3. 有效 UTF-8 直接保留；
//  4. 其余字节流保留其中的有效 UTF-8 片段，并把非法字节逐个转义为 \\xNN。
//
// 第 4 步故意不猜测 GBK、Shift-JIS 等本地代码页。猜错编码会静默改变日志内容；
// 转义原始字节虽然不如正确解码美观，但信息无损、行为确定，而且绝不会再次造成
// "invalid UTF-8 in input message"。
func normalizeNativeOutputUTF8(raw []byte) (string, string) {
	if len(raw) == 0 {
		return "", ""
	}

	if bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		text := string(raw[3:])
		if utf8.ValidString(text) {
			return escapeNativeControlRunes(text), "已移除 UTF-8 BOM"
		}
		raw = raw[3:]
	}

	if bytes.HasPrefix(raw, []byte{0xFF, 0xFE}) {
		return escapeNativeControlRunes(decodeNativeUTF16(raw[2:], binary.LittleEndian)), "UTF-16LE 已转换为 UTF-8"
	}
	if bytes.HasPrefix(raw, []byte{0xFE, 0xFF}) {
		return escapeNativeControlRunes(decodeNativeUTF16(raw[2:], binary.BigEndian)), "UTF-16BE 已转换为 UTF-8"
	}

	if order, ok := detectNativeUTF16WithoutBOM(raw); ok {
		name := "UTF-16BE"
		if order == binary.LittleEndian {
			name = "UTF-16LE"
		}
		return escapeNativeControlRunes(decodeNativeUTF16(raw, order)), name + "（无 BOM）已转换为 UTF-8"
	}

	if utf8.Valid(raw) {
		return escapeNativeControlRunes(string(raw)), ""
	}

	return escapeInvalidNativeUTF8(raw), "检测到非 UTF-8 原始字节，已逐字节转义为 \\xNN"
}

func detectNativeUTF16WithoutBOM(raw []byte) (binary.ByteOrder, bool) {
	if len(raw) < 8 {
		return nil, false
	}

	sampleLength := len(raw)
	if sampleLength > 4096 {
		sampleLength = 4096
	}
	sampleLength -= sampleLength % 2
	if sampleLength < 8 {
		return nil, false
	}

	var evenZero, oddZero int
	pairs := sampleLength / 2
	for i := 0; i < sampleLength; i += 2 {
		if raw[i] == 0 {
			evenZero++
		}
		if raw[i+1] == 0 {
			oddZero++
		}
	}

	// ASCII/拉丁文本的 UTF-16 通常在高字节位置有大量 NUL。阈值故意保守，
	// 避免把普通二进制或本地代码页文本误判为 UTF-16。
	if oddZero*100 >= pairs*60 && evenZero*100 <= pairs*10 {
		return binary.LittleEndian, true
	}
	if evenZero*100 >= pairs*60 && oddZero*100 <= pairs*10 {
		return binary.BigEndian, true
	}
	return nil, false
}

func decodeNativeUTF16(raw []byte, order binary.ByteOrder) string {
	unitCount := len(raw) / 2
	units := make([]uint16, unitCount)
	for i := 0; i < unitCount; i++ {
		units[i] = order.Uint16(raw[i*2 : i*2+2])
	}

	text := string(utf16.Decode(units))
	if len(raw)%2 == 1 {
		text += fmt.Sprintf(`\x%02X`, raw[len(raw)-1])
	}
	return text
}

func escapeInvalidNativeUTF8(raw []byte) string {
	const hexDigits = "0123456789ABCDEF"
	var out strings.Builder
	out.Grow(len(raw))

	for len(raw) > 0 {
		r, size := utf8.DecodeRune(raw)
		if r == utf8.RuneError && size == 1 {
			b := raw[0]
			out.WriteString(`\x`)
			out.WriteByte(hexDigits[b>>4])
			out.WriteByte(hexDigits[b&0x0F])
			raw = raw[1:]
			continue
		}

		writeNativeSafeRune(&out, r)
		raw = raw[size:]
	}
	return out.String()
}

func escapeNativeControlRunes(text string) string {
	var out strings.Builder
	out.Grow(len(text))
	for _, r := range text {
		writeNativeSafeRune(&out, r)
	}
	return out.String()
}

func writeNativeSafeRune(out *strings.Builder, r rune) {
	switch r {
	case '\n', '\r', '\t':
		out.WriteRune(r)
	default:
		if r < 0x20 || r == 0x7F {
			if r <= 0xFF {
				fmt.Fprintf(out, `\x%02X`, r)
			} else {
				fmt.Fprintf(out, `\u%04X`, r)
			}
			return
		}
		out.WriteRune(r)
	}
}

func truncateNativeUTF8(text string, maxBytes int) (string, bool) {
	if maxBytes < 0 {
		maxBytes = 0
	}
	if len(text) <= maxBytes {
		return text, false
	}

	prefix := text[:maxBytes]
	for len(prefix) > 0 && !utf8.ValidString(prefix) {
		prefix = prefix[:len(prefix)-1]
	}

	// 避免把工具生成的可见字节转义截成 "\\"、"\\x" 或 "\\xA"。
	// 即使截断发生在一个非法原始字节中间，返回值仍保持可读且可解析。
	if slash := strings.LastIndexByte(prefix, '\\'); slash >= 0 {
		tail := prefix[slash:]
		if tail == `\` || tail == `\x` ||
			(len(tail) == 3 && strings.HasPrefix(tail, `\x`) && isNativeHexByte(tail[2])) {
			prefix = prefix[:slash]
		}
	}
	return prefix, true
}

func isNativeHexByte(value byte) bool {
	return value >= '0' && value <= '9' ||
		value >= 'a' && value <= 'f' ||
		value >= 'A' && value <= 'F'
}

var _ io.Writer = (*cappedOutputWriter)(nil)

// Execute 直接运行白名单内的 Windows 原生 .exe。
func (t *NativeCommandTool) Execute(
	input map[string]any,
	executionEnvironment ToolExecutionEnvironment,
) (string, error) {
	if runtime.GOOS == "linux" {
		return executeLinuxCommand(input, executionEnvironment)
	}
	if runtime.GOOS != "windows" {
		return "", fmt.Errorf("command 工具仅支持 Windows；当前运行平台为 %s", runtime.GOOS)
	}
	if err := validateNativeUTF8Input("WorkingDirectory", executionEnvironment.WorkingDirectory); err != nil {
		return "", err
	}
	if strings.TrimSpace(executionEnvironment.WorkingDirectory) == "" {
		return "", fmt.Errorf("NativeCommandTool 缺少 WorkingDirectory")
	}

	workingDirectory, err := filepath.Abs(executionEnvironment.WorkingDirectory)
	if err != nil {
		return "", fmt.Errorf("解析 WorkingDirectory 失败: %w", err)
	}
	info, err := os.Stat(workingDirectory)
	if err != nil {
		return "", fmt.Errorf("访问 WorkingDirectory 失败: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("WorkingDirectory 不是目录: %s", workingDirectory)
	}

	programValue, ok := input["program"].(string)
	if !ok || strings.TrimSpace(programValue) == "" {
		return "", fmt.Errorf("command 工具需要 program 参数")
	}
	if err := validateNativeUTF8Input("program", programValue); err != nil {
		return "", err
	}
	if strings.ContainsAny(programValue, `/\\:`) {
		return "", fmt.Errorf("program 只能填写白名单程序名，不能包含路径: %s", programValue)
	}

	program := normalizeProgramName(programValue)
	if blockedNativePrograms[program] {
		return "", fmt.Errorf("程序 %s 已禁用：不允许启动命令解释器、WSL 或脚本宿主", programValue)
	}
	doc, allowed := nativeProgramByName[program]
	if !allowed {
		return "", fmt.Errorf("程序不在白名单中: %s；允许值为 %s", programValue, strings.Join(allowedProgramNames(), ", "))
	}

	args, err := readStringArguments(input)
	if err != nil {
		return "", err
	}
	timeoutSeconds, err := readOptionalInt(
		input,
		"timeout_seconds",
		defaultCommandTimeoutSeconds,
		1,
		maxCommandTimeoutSeconds,
	)
	if err != nil {
		return "", err
	}
	maxOutputBytes, err := readOptionalInt(
		input,
		"max_output_bytes",
		defaultCommandOutputBytes,
		minCommandOutputBytes,
		maxCommandOutputBytes,
	)
	if err != nil {
		return "", err
	}

	executablePath, err := resolveNativeExecutable(doc)
	if err != nil {
		return "", err
	}
	args = prepareNativeArgs(program, args)

	parentContext := executionEnvironment.Context
	if parentContext == nil {
		parentContext = context.Background()
	}
	if parentContext.Err() != nil {
		return "", formatNativeInfrastructureError(
			program,
			args,
			executablePath,
			workingDirectory,
			"启动前会话已取消",
			"",
			parentContext.Err(),
		)
	}
	commandContext, cancelCommand := context.WithTimeout(
		parentContext,
		time.Duration(timeoutSeconds)*time.Second,
	)
	defer cancelCommand()

	cmd := exec.CommandContext(commandContext, executablePath, args...)
	cmd.Dir = workingDirectory
	cmd.Env = sanitizedNativeEnvironment(program)

	output := newCappedOutputWriter(maxOutputBytes)
	cmd.Stdout = output
	cmd.Stderr = output

	err = cmd.Run()
	result := output.UTF8String()

	return classifyNativeCommandResult(
		program,
		args,
		executablePath,
		workingDirectory,
		err,
		result,
		commandContext,
	)
}
