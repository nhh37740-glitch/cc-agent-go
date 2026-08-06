package tool

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	defaultReadMaxBytes   = 1 << 20 // 1 MiB
	absoluteReadMaxBytes  = 4 << 20 // 4 MiB
	absoluteWriteMaxBytes = 16 << 20
	defaultListMaxItems   = 200
	absoluteListMaxItems  = 2000
)

// FileTool 在 Agent 工作目录内提供不依赖 Bash、PowerShell、cmd.exe 或 WSL 的
// 文件增删改查能力。所有操作均直接使用 Go 标准库完成。
type FileTool struct{}

// NewFileTool 创建文件增删改查工具。
func NewFileTool() *FileTool {
	return &FileTool{}
}

// Name 返回工具名。
func (f *FileTool) Name() string {
	return "file"
}

const fileToolDescription = `在当前 Windows Agent 工作目录内直接操作文件，不启动 Bash、WSL、PowerShell 或 cmd.exe。

【什么时候使用】
- 新建、读取、编辑或删除源码和配置文件时，优先使用本工具，不要用 bash 拼接 cat、sed、echo、rm 等命令。
- 支持 Go 源文件以及其他 UTF-8 文本文件。
- path 使用工作目录内的相对路径，例如 internal/server/server.go；也允许指向工作目录内部的绝对 Windows 路径。
- 所有路径都会限制在 WorkingDirectory 内；禁止 .. 越界、符号链接越界、Windows 设备名和 NTFS ADS 路径。

【operation 分类】
1. create：新增文件。目标必须不存在；content 可为空；默认自动创建父目录。
   例：{"operation":"create","path":"internal/app/app.go","content":"package app\n"}

2. read：读取 UTF-8 文本。可用 start_line/end_line 读取行区间，默认带行号并限制输出大小。
   例：{"operation":"read","path":"internal/app/app.go","start_line":1,"end_line":120}

3. update：修改已有 UTF-8 文件，由 update_mode 指定方式。
   - replace：精确替换 old_text。默认要求 old_text 在文件中只出现一次；replace_all=true 才替换全部。
   - append：在文件末尾追加 content。
   - prepend：在文件开头插入 content。
   - overwrite：用 content 覆盖整个文件。
   例：{"operation":"update","path":"main.go","update_mode":"replace","old_text":"oldName","new_text":"newName"}

4. delete：删除文件或空目录。删除非空目录必须显式 recursive=true；禁止删除工作目录根目录。
   例：{"operation":"delete","path":"tmp/generated.go"}

5. list：列出目录内容。默认只列一层；recursive=true 可递归，但不会跟随符号链接。
   例：{"operation":"list","path":"internal","recursive":true,"max_entries":300}

6. stat：查询文件或目录的类型、大小、权限和修改时间。
   例：{"operation":"stat","path":"go.mod"}

【重要规则】
- 一次调用只执行一个 operation。
- create 使用 content；update 根据 update_mode 使用 content 或 old_text/new_text。
- read 的行号从 1 开始，end_line 为包含式结束行。
- 对已有文件进行 update 时会保留原权限，并通过同目录临时文件替换，降低写入中断风险。
- 该工具只处理文本修改；二进制文件读取和修改会被拒绝。`

// Description 返回发送给模型的结构化工具说明。
func (f *FileTool) Description() string {
	return fileToolDescription
}

// InputSchema 返回单工具、多操作的 JSON Schema。
// operation 决定实际动作，其余参数只在对应操作中生效。
func (f *FileTool) InputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"title":                "Windows 工作区文件增删改查",
		"description":          "直接使用 Go 标准库操作当前工作区文件，不启动 Bash、WSL、PowerShell 或 cmd.exe。通过 operation 选择 create/read/update/delete/list/stat。",
		"additionalProperties": false,
		"properties": map[string]any{
			"operation": map[string]any{
				"type":        "string",
				"title":       "操作类型",
				"description": "create=新增；read=读取；update=修改；delete=删除；list=列目录；stat=查询元数据。",
				"enum":        []string{"create", "read", "update", "delete", "list", "stat"},
			},
			"path": map[string]any{
				"type":        "string",
				"title":       "工作区内路径",
				"minLength":   1,
				"description": "目标文件或目录路径。优先使用相对路径和正斜杠，例如 internal/app/app.go。禁止访问 WorkingDirectory 之外。",
			},
			"content": map[string]any{
				"type":        "string",
				"title":       "写入内容",
				"description": "create 的新文件内容；update_mode 为 append、prepend 或 overwrite 时的内容。允许空字符串。",
			},
			"update_mode": map[string]any{
				"type":        "string",
				"title":       "修改方式",
				"description": "仅 operation=update 使用。replace 精确替换；append 末尾追加；prepend 开头插入；overwrite 整体覆盖。默认 replace。",
				"enum":        []string{"replace", "append", "prepend", "overwrite"},
			},
			"old_text": map[string]any{
				"type":        "string",
				"title":       "待替换原文",
				"minLength":   1,
				"description": "operation=update 且 update_mode=replace 时必填。可以是多行文本，必须精确匹配。",
			},
			"new_text": map[string]any{
				"type":        "string",
				"title":       "替换后文本",
				"description": "operation=update 且 update_mode=replace 时使用。允许空字符串，表示删除 old_text。",
			},
			"replace_all": map[string]any{
				"type":        "boolean",
				"title":       "替换全部匹配",
				"description": "仅 replace 使用。false 时要求 old_text 恰好出现一次；true 时替换所有匹配。默认 false。",
			},
			"create_parents": map[string]any{
				"type":        "boolean",
				"title":       "自动创建父目录",
				"description": "仅 create 使用。默认 true。",
			},
			"start_line": map[string]any{
				"type":        "integer",
				"title":       "读取起始行",
				"minimum":     1,
				"description": "仅 read 使用，行号从 1 开始。默认 1。",
			},
			"end_line": map[string]any{
				"type":        "integer",
				"title":       "读取结束行",
				"minimum":     1,
				"description": "仅 read 使用，包含该行；省略表示读取到文件末尾或 max_bytes 限制。",
			},
			"line_numbers": map[string]any{
				"type":        "boolean",
				"title":       "显示行号",
				"description": "仅 read 使用。默认 true。",
			},
			"max_bytes": map[string]any{
				"type":        "integer",
				"title":       "最大返回字节数",
				"minimum":     1,
				"maximum":     absoluteReadMaxBytes,
				"description": "仅 read 使用。默认 1 MiB，最大 4 MiB；超出时截断输出并给出提示。",
			},
			"recursive": map[string]any{
				"type":        "boolean",
				"title":       "递归处理",
				"description": "delete 时允许删除非空目录；list 时递归列出子目录。默认 false。",
			},
			"max_entries": map[string]any{
				"type":        "integer",
				"title":       "最大目录条目数",
				"minimum":     1,
				"maximum":     absoluteListMaxItems,
				"description": "仅 list 使用。默认 200，最大 2000。",
			},
		},
		"required": []string{"operation", "path"},
		"examples": []map[string]any{
			{"operation": "create", "path": "internal/app/app.go", "content": "package app\n"},
			{"operation": "read", "path": "internal/app/app.go", "start_line": 1, "end_line": 120},
			{"operation": "update", "path": "main.go", "update_mode": "replace", "old_text": "oldName", "new_text": "newName"},
			{"operation": "update", "path": "CHANGELOG.md", "update_mode": "append", "content": "\n- Added feature\n"},
			{"operation": "delete", "path": "tmp/generated.go"},
			{"operation": "list", "path": "internal", "recursive": true, "max_entries": 300},
			{"operation": "stat", "path": "go.mod"},
		},
	}
}

func (f *FileTool) Execute(
	input map[string]any,
	executionEnvironment ToolExecutionEnvironment,
) (string, error) {
	if strings.TrimSpace(executionEnvironment.WorkingDirectory) == "" {
		return "", fmt.Errorf("FileTool 缺少 WorkingDirectory")
	}

	operation, err := requiredString(input, "operation", false)
	if err != nil {
		return "", err
	}
	operation = strings.ToLower(strings.TrimSpace(operation))

	requestedPath, err := requiredString(input, "path", false)
	if err != nil {
		return "", err
	}

	workspace, target, relativePath, err := resolveWorkspacePath(
		executionEnvironment.WorkingDirectory,
		requestedPath,
	)
	if err != nil {
		return "", err
	}

	switch operation {
	case "create":
		return executeFileCreate(input, workspace, target, relativePath)
	case "read":
		return executeFileRead(input, target, relativePath)
	case "update":
		return executeFileUpdate(input, target, relativePath)
	case "delete":
		return executeFileDelete(input, target, relativePath)
	case "list":
		return executeFileList(input, target, relativePath)
	case "stat":
		return executeFileStat(target, relativePath)
	default:
		return "", fmt.Errorf("不支持的 operation %q；允许 create、read、update、delete、list、stat", operation)
	}
}

func executeFileCreate(
	input map[string]any,
	workspace workspacePaths,
	target string,
	relativePath string,
) (string, error) {
	content, err := requiredString(input, "content", true)
	if err != nil {
		return "", fmt.Errorf("create: %w", err)
	}
	if len(content) > absoluteWriteMaxBytes {
		return "", fmt.Errorf("create 内容过大：%d 字节，最大允许 %d 字节", len(content), absoluteWriteMaxBytes)
	}

	createParents, err := optionalBool(input, "create_parents", true)
	if err != nil {
		return "", err
	}

	if _, err := os.Lstat(target); err == nil {
		return "", fmt.Errorf("create 拒绝覆盖已有路径: %s；请改用 update", relativePath)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("检查目标路径失败 %s: %w", relativePath, err)
	}

	parent := filepath.Dir(target)
	if createParents {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return "", fmt.Errorf("创建父目录失败 %s: %w", filepath.ToSlash(parent), err)
		}
	} else if info, err := os.Stat(parent); err != nil || !info.IsDir() {
		if err != nil {
			return "", fmt.Errorf("父目录不存在 %s: %w", filepath.ToSlash(parent), err)
		}
		return "", fmt.Errorf("父路径不是目录: %s", filepath.ToSlash(parent))
	}

	// MkdirAll 后重新检查路径，防止并发创建的符号链接把目标引出工作区。
	_, checkedTarget, checkedRelative, err := resolveWorkspacePath(workspace.rootOriginal, target)
	if err != nil {
		return "", err
	}
	if !samePath(target, checkedTarget) {
		return "", fmt.Errorf("目标路径在创建父目录后发生变化: %s", relativePath)
	}
	relativePath = checkedRelative

	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", fmt.Errorf("创建文件失败 %s: %w", relativePath, err)
	}

	writeErr := writeAll(file, []byte(content))
	closeErr := file.Close()
	if writeErr != nil {
		_ = os.Remove(target)
		return "", fmt.Errorf("写入新文件失败 %s: %w", relativePath, writeErr)
	}
	if closeErr != nil {
		_ = os.Remove(target)
		return "", fmt.Errorf("关闭新文件失败 %s: %w", relativePath, closeErr)
	}

	return fmt.Sprintf("created: %s\nbytes: %d", relativePath, len(content)), nil
}

func executeFileRead(input map[string]any, target, relativePath string) (string, error) {
	info, err := requireRegularFile(target, relativePath)
	if err != nil {
		return "", err
	}

	startLine, err := optionalInt(input, "start_line", 1, 1, int(^uint(0)>>1))
	if err != nil {
		return "", err
	}
	endLine, hasEnd, err := optionalIntPresence(input, "end_line", 1, int(^uint(0)>>1))
	if err != nil {
		return "", err
	}
	if hasEnd && endLine < startLine {
		return "", fmt.Errorf("end_line (%d) 不能小于 start_line (%d)", endLine, startLine)
	}

	lineNumbers, err := optionalBool(input, "line_numbers", true)
	if err != nil {
		return "", err
	}
	maxBytes, err := optionalInt(input, "max_bytes", defaultReadMaxBytes, 1, absoluteReadMaxBytes)
	if err != nil {
		return "", err
	}

	body, firstReturned, lastReturned, totalSeen, truncated, err := readTextRange(
		target,
		startLine,
		endLine,
		hasEnd,
		lineNumbers,
		maxBytes,
	)
	if err != nil {
		return "", fmt.Errorf("读取文件失败 %s: %w", relativePath, err)
	}

	var out strings.Builder
	fmt.Fprintf(&out, "path: %s\nsize: %d bytes\n", relativePath, info.Size())
	if firstReturned == 0 {
		fmt.Fprintf(&out, "lines: none (file ended at line %d)\n", totalSeen)
	} else {
		fmt.Fprintf(&out, "lines: %d-%d\n", firstReturned, lastReturned)
	}
	out.WriteString("---\n")
	out.WriteString(body)
	if truncated {
		if body != "" && !strings.HasSuffix(body, "\n") {
			out.WriteByte('\n')
		}
		fmt.Fprintf(&out, "---\noutput truncated at %d bytes; narrow start_line/end_line or raise max_bytes\n", maxBytes)
	}
	return out.String(), nil
}

func executeFileUpdate(input map[string]any, target, relativePath string) (string, error) {
	info, err := requireRegularFile(target, relativePath)
	if err != nil {
		return "", err
	}
	if info.Size() > absoluteWriteMaxBytes {
		return "", fmt.Errorf("文件过大，拒绝整体加载后修改: %s (%d bytes，最大 %d)", relativePath, info.Size(), absoluteWriteMaxBytes)
	}

	originalBytes, err := os.ReadFile(target)
	if err != nil {
		return "", fmt.Errorf("读取待修改文件失败 %s: %w", relativePath, err)
	}
	if !utf8.Valid(originalBytes) {
		return "", fmt.Errorf("update 只支持 UTF-8 文本文件，目标疑似二进制或编码无效: %s", relativePath)
	}
	original := string(originalBytes)

	updateMode, err := optionalString(input, "update_mode", "replace")
	if err != nil {
		return "", err
	}
	updateMode = strings.ToLower(strings.TrimSpace(updateMode))

	var (
		updated      string
		replacements int
	)

	switch updateMode {
	case "replace":
		oldText, err := requiredString(input, "old_text", false)
		if err != nil {
			return "", fmt.Errorf("update replace: %w", err)
		}
		newText, err := requiredString(input, "new_text", true)
		if err != nil {
			return "", fmt.Errorf("update replace: %w", err)
		}
		if oldText == "" {
			return "", fmt.Errorf("update replace 的 old_text 不能为空")
		}

		replaceAll, err := optionalBool(input, "replace_all", false)
		if err != nil {
			return "", err
		}
		matches := strings.Count(original, oldText)
		if matches == 0 {
			return "", fmt.Errorf("old_text 在 %s 中未找到；文件未修改", relativePath)
		}
		if !replaceAll && matches != 1 {
			return "", fmt.Errorf("old_text 在 %s 中出现 %d 次；为避免误改，请提供更长的唯一上下文，或显式设置 replace_all=true", relativePath, matches)
		}
		if replaceAll {
			updated = strings.ReplaceAll(original, oldText, newText)
			replacements = matches
		} else {
			updated = strings.Replace(original, oldText, newText, 1)
			replacements = 1
		}

	case "append":
		content, err := requiredString(input, "content", true)
		if err != nil {
			return "", fmt.Errorf("update append: %w", err)
		}
		updated = original + content

	case "prepend":
		content, err := requiredString(input, "content", true)
		if err != nil {
			return "", fmt.Errorf("update prepend: %w", err)
		}
		updated = content + original

	case "overwrite":
		content, err := requiredString(input, "content", true)
		if err != nil {
			return "", fmt.Errorf("update overwrite: %w", err)
		}
		updated = content

	default:
		return "", fmt.Errorf("不支持的 update_mode %q；允许 replace、append、prepend、overwrite", updateMode)
	}

	if len(updated) > absoluteWriteMaxBytes {
		return "", fmt.Errorf("修改后内容过大：%d 字节，最大允许 %d 字节", len(updated), absoluteWriteMaxBytes)
	}
	if updated == original {
		return fmt.Sprintf("unchanged: %s\nbytes: %d", relativePath, len(original)), nil
	}

	if err := rewriteFileSafely(target, []byte(updated), info.Mode()); err != nil {
		return "", fmt.Errorf("写回文件失败 %s: %w", relativePath, err)
	}

	var out strings.Builder
	fmt.Fprintf(&out, "updated: %s\nmode: %s\nbytes_before: %d\nbytes_after: %d", relativePath, updateMode, len(original), len(updated))
	if updateMode == "replace" {
		fmt.Fprintf(&out, "\nreplacements: %d", replacements)
	}
	return out.String(), nil
}

func executeFileDelete(input map[string]any, target, relativePath string) (string, error) {
	if relativePath == "." {
		return "", fmt.Errorf("禁止删除 WorkingDirectory 根目录")
	}
	info, err := os.Lstat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("目标不存在: %s", relativePath)
		}
		return "", fmt.Errorf("检查待删除路径失败 %s: %w", relativePath, err)
	}

	recursive, err := optionalBool(input, "recursive", false)
	if err != nil {
		return "", err
	}

	if info.IsDir() && recursive {
		if err := os.RemoveAll(target); err != nil {
			return "", fmt.Errorf("递归删除目录失败 %s: %w", relativePath, err)
		}
		return fmt.Sprintf("deleted recursively: %s", relativePath), nil
	}

	if err := os.Remove(target); err != nil {
		if info.IsDir() && !recursive {
			return "", fmt.Errorf("删除目录失败 %s: %w；若目录非空，必须显式设置 recursive=true", relativePath, err)
		}
		return "", fmt.Errorf("删除失败 %s: %w", relativePath, err)
	}
	return fmt.Sprintf("deleted: %s", relativePath), nil
}

func executeFileList(input map[string]any, target, relativePath string) (string, error) {
	info, err := os.Stat(target)
	if err != nil {
		return "", fmt.Errorf("读取目录失败 %s: %w", relativePath, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("list 的目标不是目录: %s", relativePath)
	}

	recursive, err := optionalBool(input, "recursive", false)
	if err != nil {
		return "", err
	}
	maxEntries, err := optionalInt(input, "max_entries", defaultListMaxItems, 1, absoluteListMaxItems)
	if err != nil {
		return "", err
	}

	entries := make([]listedEntry, 0, minInt(maxEntries, 64))
	truncated := false

	if recursive {
		err = filepath.WalkDir(target, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if samePath(path, target) {
				return nil
			}
			if len(entries) >= maxEntries {
				truncated = true
				return filepath.SkipAll
			}

			rel, relErr := filepath.Rel(target, path)
			if relErr != nil {
				return relErr
			}
			listed, infoErr := makeListedEntry(filepath.ToSlash(rel), entry)
			if infoErr != nil {
				return infoErr
			}
			entries = append(entries, listed)
			return nil
		})
	} else {
		var dirEntries []os.DirEntry
		dirEntries, err = os.ReadDir(target)
		if err == nil {
			if len(dirEntries) > maxEntries {
				dirEntries = dirEntries[:maxEntries]
				truncated = true
			}
			for _, entry := range dirEntries {
				listed, infoErr := makeListedEntry(entry.Name(), entry)
				if infoErr != nil {
					return "", fmt.Errorf("读取目录条目失败 %s/%s: %w", relativePath, entry.Name(), infoErr)
				}
				entries = append(entries, listed)
			}
		}
	}
	if err != nil {
		return "", fmt.Errorf("列出目录失败 %s: %w", relativePath, err)
	}

	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].path) < strings.ToLower(entries[j].path)
	})

	var out strings.Builder
	fmt.Fprintf(&out, "path: %s\nrecursive: %t\nentries: %d\n---\n", relativePath, recursive, len(entries))
	for _, entry := range entries {
		fmt.Fprintf(&out, "%s\t%10d\t%s\n", entry.kind, entry.size, entry.path)
	}
	if truncated {
		fmt.Fprintf(&out, "---\nlist truncated at %d entries; narrow path or raise max_entries\n", maxEntries)
	}
	return out.String(), nil
}

func executeFileStat(target, relativePath string) (string, error) {
	info, err := os.Lstat(target)
	if err != nil {
		return "", fmt.Errorf("stat 失败 %s: %w", relativePath, err)
	}

	kind := fileInfoKind(info)
	var out strings.Builder
	fmt.Fprintf(&out, "path: %s\ntype: %s\nsize: %d\nmode: %s\nmodified: %s",
		relativePath,
		kind,
		info.Size(),
		info.Mode().String(),
		info.ModTime().Format(time.RFC3339),
	)
	if info.Mode()&os.ModeSymlink != 0 {
		if linkTarget, linkErr := os.Readlink(target); linkErr == nil {
			fmt.Fprintf(&out, "\nsymlink_target: %s", filepath.ToSlash(linkTarget))
		}
	}
	return out.String(), nil
}

type workspacePaths struct {
	rootOriginal string
	rootResolved string
}

// resolveWorkspacePath 同时执行词法边界检查和符号链接边界检查。
func resolveWorkspacePath(workingDirectory, requestedPath string) (workspacePaths, string, string, error) {
	requestedPath = strings.TrimSpace(requestedPath)
	if requestedPath == "" {
		return workspacePaths{}, "", "", fmt.Errorf("path 不能为空")
	}
	if strings.IndexByte(requestedPath, 0) >= 0 {
		return workspacePaths{}, "", "", fmt.Errorf("path 包含 NUL 字符")
	}

	// 对相对路径使用 ValidatePath 做第一道防线校验
	if !filepath.IsAbs(requestedPath) {
		if _, err := ValidatePath(workingDirectory, requestedPath); err != nil {
			return workspacePaths{}, "", "", err
		}
	}

	rootOriginal, err := filepath.Abs(filepath.Clean(workingDirectory))
	if err != nil {
		return workspacePaths{}, "", "", fmt.Errorf("解析 WorkingDirectory 失败: %w", err)
	}
	rootInfo, err := os.Stat(rootOriginal)
	if err != nil {
		return workspacePaths{}, "", "", fmt.Errorf("访问 WorkingDirectory 失败 %s: %w", rootOriginal, err)
	}
	if !rootInfo.IsDir() {
		return workspacePaths{}, "", "", fmt.Errorf("WorkingDirectory 不是目录: %s", rootOriginal)
	}
	rootResolved, err := filepath.EvalSymlinks(rootOriginal)
	if err != nil {
		return workspacePaths{}, "", "", fmt.Errorf("解析 WorkingDirectory 符号链接失败: %w", err)
	}
	rootResolved, err = filepath.Abs(rootResolved)
	if err != nil {
		return workspacePaths{}, "", "", err
	}

	var target string
	if filepath.IsAbs(requestedPath) {
		target = filepath.Clean(requestedPath)
	} else {
		target = filepath.Join(rootOriginal, requestedPath)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return workspacePaths{}, "", "", fmt.Errorf("解析 path 失败 %q: %w", requestedPath, err)
	}

	relative, err := filepath.Rel(rootOriginal, target)
	if err != nil || !relativePathIsInside(relative) {
		return workspacePaths{}, "", "", fmt.Errorf("禁止访问 WorkingDirectory 之外的路径: %s", requestedPath)
	}
	if err := validateWindowsRelativePath(relative); err != nil {
		return workspacePaths{}, "", "", err
	}

	// 从目标向上找到最近的已有祖先并解析符号链接。这样目标尚不存在时，
	// 仍能阻止通过父目录符号链接写到工作区之外。
	probe := target
	for {
		_, statErr := os.Lstat(probe)
		if statErr == nil {
			resolvedProbe, resolveErr := filepath.EvalSymlinks(probe)
			if resolveErr != nil {
				return workspacePaths{}, "", "", fmt.Errorf("解析路径符号链接失败 %s: %w", requestedPath, resolveErr)
			}
			resolvedProbe, resolveErr = filepath.Abs(resolvedProbe)
			if resolveErr != nil {
				return workspacePaths{}, "", "", resolveErr
			}
			resolvedRelative, relErr := filepath.Rel(rootResolved, resolvedProbe)
			if relErr != nil || !relativePathIsInside(resolvedRelative) {
				return workspacePaths{}, "", "", fmt.Errorf("禁止通过符号链接访问 WorkingDirectory 之外: %s", requestedPath)
			}
			break
		}
		if !os.IsNotExist(statErr) {
			return workspacePaths{}, "", "", fmt.Errorf("检查路径失败 %s: %w", requestedPath, statErr)
		}
		parent := filepath.Dir(probe)
		if samePath(parent, probe) {
			return workspacePaths{}, "", "", fmt.Errorf("无法找到 path 的已有父目录: %s", requestedPath)
		}
		probe = parent
	}

	displayPath := filepath.ToSlash(relative)
	if displayPath == "" {
		displayPath = "."
	}
	return workspacePaths{rootOriginal: rootOriginal, rootResolved: rootResolved}, target, displayPath, nil
}

func relativePathIsInside(relative string) bool {
	if relative == "" || relative == "." {
		return true
	}
	if filepath.IsAbs(relative) || relative == ".." {
		return false
	}
	return !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func validateWindowsRelativePath(relative string) error {
	if runtime.GOOS != "windows" || relative == "." {
		return nil
	}
	if strings.Contains(relative, ":") {
		return fmt.Errorf("Windows 路径不允许 NTFS ADS 冒号语法: %s", filepath.ToSlash(relative))
	}

	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		if strings.TrimRight(component, " .") != component {
			return fmt.Errorf("Windows 路径组件不能以空格或点结尾: %s", component)
		}
		name := strings.ToUpper(strings.SplitN(component, ".", 2)[0])
		if isWindowsReservedName(name) {
			return fmt.Errorf("Windows 保留设备名不能作为路径组件: %s", component)
		}
	}
	return nil
}

func isWindowsReservedName(name string) bool {
	switch name {
	case "CON", "PRN", "AUX", "NUL", "CLOCK$":
		return true
	}
	if len(name) == 4 {
		prefix := name[:3]
		last := name[3]
		if (prefix == "COM" || prefix == "LPT") && last >= '1' && last <= '9' {
			return true
		}
	}
	return false
}

func requireRegularFile(path, displayPath string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("访问文件失败 %s: %w", displayPath, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("目标不是普通文件: %s (%s)", displayPath, fileInfoKind(info))
	}
	return info, nil
}

func readTextRange(
	path string,
	startLine int,
	endLine int,
	hasEnd bool,
	lineNumbers bool,
	maxBytes int,
) (body string, firstReturned, lastReturned, totalSeen int, truncated bool, err error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, 0, 0, false, err
	}
	defer file.Close()

	reader := bufio.NewReaderSize(file, 64*1024)
	var out strings.Builder
	lineNumber := 0

	for {
		line, readErr := reader.ReadString('\n')
		if line != "" {
			lineNumber++
			totalSeen = lineNumber
			if lineNumber >= startLine && (!hasEnd || lineNumber <= endLine) {
				if !utf8.ValidString(line) {
					return "", 0, 0, totalSeen, false, fmt.Errorf("文件不是有效 UTF-8 文本（第 %d 行附近）", lineNumber)
				}
				formatted := line
				if lineNumbers {
					formatted = fmt.Sprintf("%6d | %s", lineNumber, line)
				}
				remaining := maxBytes - out.Len()
				if len(formatted) > remaining {
					if remaining > 0 {
						out.WriteString(validUTF8Prefix(formatted, remaining))
					}
					truncated = true
					if firstReturned == 0 {
						firstReturned = lineNumber
					}
					lastReturned = lineNumber
					break
				}
				out.WriteString(formatted)
				if firstReturned == 0 {
					firstReturned = lineNumber
				}
				lastReturned = lineNumber
			}
		}

		if hasEnd && lineNumber >= endLine {
			break
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", 0, 0, totalSeen, false, readErr
		}
	}

	return out.String(), firstReturned, lastReturned, totalSeen, truncated, nil
}

func validUTF8Prefix(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	prefix := value[:maxBytes]
	for len(prefix) > 0 && !utf8.ValidString(prefix) {
		prefix = prefix[:len(prefix)-1]
	}
	return prefix
}

func rewriteFileSafely(path string, content []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	temp, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	cleanupTemp := true
	defer func() {
		if cleanupTemp {
			_ = os.Remove(tempPath)
		}
	}()

	if err := temp.Chmod(mode.Perm()); err != nil {
		_ = temp.Close()
		return err
	}
	if err := writeAll(temp, content); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}

	if runtime.GOOS != "windows" {
		if err := os.Rename(tempPath, path); err != nil {
			return err
		}
		cleanupTemp = false
		return nil
	}

	// Windows 的 os.Rename 不能直接覆盖已有文件。先把原文件改名为备份，
	// 再把临时文件移入；第二步失败时尽力回滚原文件。
	backupFile, err := os.CreateTemp(dir, "."+base+".backup-*")
	if err != nil {
		return err
	}
	backupPath := backupFile.Name()
	if err := backupFile.Close(); err != nil {
		_ = os.Remove(backupPath)
		return err
	}
	if err := os.Remove(backupPath); err != nil {
		return err
	}

	if err := os.Rename(path, backupPath); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		rollbackErr := os.Rename(backupPath, path)
		if rollbackErr != nil {
			return fmt.Errorf("替换失败: %v；回滚原文件也失败: %w", err, rollbackErr)
		}
		return err
	}
	cleanupTemp = false
	if err := os.Remove(backupPath); err != nil {
		return fmt.Errorf("文件已更新，但删除临时备份失败 %s: %w", filepath.Base(backupPath), err)
	}
	return nil
}

func writeAll(writer io.Writer, content []byte) error {
	for len(content) > 0 {
		written, err := writer.Write(content)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		content = content[written:]
	}
	return nil
}

type listedEntry struct {
	path string
	kind string
	size int64
}

func makeListedEntry(path string, entry os.DirEntry) (listedEntry, error) {
	info, err := entry.Info()
	if err != nil {
		return listedEntry{}, err
	}
	return listedEntry{
		path: filepath.ToSlash(path),
		kind: fileInfoKind(info),
		size: info.Size(),
	}, nil
}

func fileInfoKind(info os.FileInfo) string {
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		return "symlink"
	case info.IsDir():
		return "dir"
	case info.Mode().IsRegular():
		return "file"
	default:
		return "other"
	}
}

func requiredString(input map[string]any, key string, allowEmpty bool) (string, error) {
	value, ok := input[key]
	if !ok {
		return "", fmt.Errorf("缺少参数 %s", key)
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("参数 %s 必须是字符串", key)
	}
	if !allowEmpty && strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("参数 %s 不能为空", key)
	}
	return text, nil
}

func optionalString(input map[string]any, key, defaultValue string) (string, error) {
	value, ok := input[key]
	if !ok {
		return defaultValue, nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("参数 %s 必须是字符串", key)
	}
	return text, nil
}

func optionalBool(input map[string]any, key string, defaultValue bool) (bool, error) {
	value, ok := input[key]
	if !ok {
		return defaultValue, nil
	}
	boolean, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("参数 %s 必须是布尔值", key)
	}
	return boolean, nil
}

func optionalInt(input map[string]any, key string, defaultValue, minimum, maximum int) (int, error) {
	value, ok := input[key]
	if !ok {
		return defaultValue, nil
	}
	integer, err := integerFromAny(value)
	if err != nil {
		return 0, fmt.Errorf("参数 %s %w", key, err)
	}
	if integer < minimum || integer > maximum {
		return 0, fmt.Errorf("参数 %s 必须在 %d 到 %d 之间", key, minimum, maximum)
	}
	return integer, nil
}

func optionalIntPresence(input map[string]any, key string, minimum, maximum int) (int, bool, error) {
	value, ok := input[key]
	if !ok {
		return 0, false, nil
	}
	integer, err := integerFromAny(value)
	if err != nil {
		return 0, true, fmt.Errorf("参数 %s %w", key, err)
	}
	if integer < minimum || integer > maximum {
		return 0, true, fmt.Errorf("参数 %s 必须在 %d 到 %d 之间", key, minimum, maximum)
	}
	return integer, true, nil
}

func integerFromAny(value any) (int, error) {
	switch number := value.(type) {
	case int:
		return number, nil
	case int8:
		return int(number), nil
	case int16:
		return int(number), nil
	case int32:
		return int(number), nil
	case int64:
		if int64(int(number)) != number {
			return 0, fmt.Errorf("超出整数范围")
		}
		return int(number), nil
	case uint:
		if uint(int(number)) != number {
			return 0, fmt.Errorf("超出整数范围")
		}
		return int(number), nil
	case uint64:
		if uint64(int(number)) != number {
			return 0, fmt.Errorf("超出整数范围")
		}
		return int(number), nil
	case float64:
		integer := int(number)
		if float64(integer) != number {
			return 0, fmt.Errorf("必须是整数")
		}
		return integer, nil
	case json.Number:
		parsed, err := strconv.ParseInt(string(number), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("必须是整数")
		}
		if int64(int(parsed)) != parsed {
			return 0, fmt.Errorf("超出整数范围")
		}
		return int(parsed), nil
	default:
		return 0, fmt.Errorf("必须是整数")
	}
}

func samePath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
