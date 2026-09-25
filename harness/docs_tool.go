package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"cc-agent-go/tool"
)

const (
	docsToolMaxReadBytes = 64 << 10 // 64 KiB
	docsToolMaxListItems = 200
)

// harnessDocsTool 让主管理在严格白名单内读取 AGENTS.md 与 shared 文件。
// 禁止读取 residents/*/docs 与 sessions，避免上下文爆炸。
type harnessDocsTool struct {
	runtime *Runtime
}

func newHarnessDocsTool(runtime *Runtime) *harnessDocsTool {
	return &harnessDocsTool{runtime: runtime}
}

func (harnessDocsTool) Name() string { return "docs" }

func (harnessDocsTool) Description() string {
	return `在白名单内读取 Harness 文档，避免把无关文件塞进上下文。
允许：
- .cc-agent/harness/AGENTS.md（主管理规则）
- .cc-agent/harness/shared/**（共享规则、共享记忆、共享文档）
- .cc-agent/harness/residents/*/AGENTS.md（常驻身份文件）
禁止：
- residents/*/docs/**
- .cc-agent/sessions/**
- 项目业务源码
operation=list 列出目录；operation=read 读取文件。path 使用相对项目根路径。`
}

func (harnessDocsTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"operation": map[string]any{
				"type":        "string",
				"description": "list 或 read",
				"enum":        []string{"list", "read"},
			},
			"path": map[string]any{
				"type":        "string",
				"description": "相对项目根路径，例如 .cc-agent/harness/shared/memory.md",
			},
		},
		"required": []string{"operation", "path"},
	}
}

func (docsToolForHarness *harnessDocsTool) Execute(
	toolArguments map[string]any,
	executionEnvironment tool.ToolExecutionEnvironment,
) (string, error) {
	operation, _ := toolArguments["operation"].(string)
	operation = strings.TrimSpace(strings.ToLower(operation))
	relativePath, _ := toolArguments["path"].(string)
	relativePath = filepath.ToSlash(strings.TrimSpace(relativePath))
	if operation == "" {
		return "", fmt.Errorf("缺少 operation（list 或 read）")
	}
	if relativePath == "" {
		return "", fmt.Errorf("缺少 path")
	}

	absolutePath, allowError := resolveAllowedHarnessDocPath(
		docsToolForHarness.runtime.workingDirectory,
		relativePath,
	)
	if allowError != nil {
		return "", allowError
	}

	switch operation {
	case "list":
		return listAllowedHarnessDocPath(relativePath, absolutePath)
	case "read":
		return readAllowedHarnessDocPath(relativePath, absolutePath)
	default:
		return "", fmt.Errorf("不支持的 operation %q，仅允许 list 或 read", operation)
	}
}

func resolveAllowedHarnessDocPath(
	workingDirectory string,
	relativePath string,
) (string, error) {
	cleanedRelative := filepath.Clean(filepath.FromSlash(relativePath))
	if strings.HasPrefix(cleanedRelative, "..") {
		return "", fmt.Errorf("path 不允许跳出项目目录: %s", relativePath)
	}
	absolutePath := filepath.Join(workingDirectory, cleanedRelative)
	absoluteWorkingDirectory, absWorkError := filepath.Abs(workingDirectory)
	if absWorkError != nil {
		return "", absWorkError
	}
	absolutePath, absPathError := filepath.Abs(absolutePath)
	if absPathError != nil {
		return "", absPathError
	}
	if !strings.HasPrefix(
		strings.ToLower(absolutePath),
		strings.ToLower(absoluteWorkingDirectory)+string(filepath.Separator),
	) && !strings.EqualFold(absolutePath, absoluteWorkingDirectory) {
		return "", fmt.Errorf("path 越界: %s", relativePath)
	}

	slashRelative := filepath.ToSlash(cleanedRelative)
	if !isAllowedHarnessManagerDocPath(slashRelative) {
		return "", fmt.Errorf(
			"path 不在主管理可读白名单内: %s；只允许 harness/AGENTS.md、shared/**、residents/*/AGENTS.md",
			slashRelative,
		)
	}
	return absolutePath, nil
}

func isAllowedHarnessManagerDocPath(slashRelativePath string) bool {
	const harnessPrefix = ".cc-agent/harness/"
	if !strings.HasPrefix(slashRelativePath, harnessPrefix) &&
		slashRelativePath != ".cc-agent/harness" {
		return false
	}
	if slashRelativePath == ".cc-agent/harness/AGENTS.md" {
		return true
	}
	if strings.HasPrefix(slashRelativePath, ".cc-agent/harness/shared") {
		return true
	}
	// residents/<slug>/AGENTS.md only
	if strings.HasPrefix(slashRelativePath, ".cc-agent/harness/residents/") {
		rest := strings.TrimPrefix(slashRelativePath, ".cc-agent/harness/residents/")
		parts := strings.Split(rest, "/")
		if len(parts) == 1 && parts[0] != "" {
			// list residents or residents/<slug>
			return true
		}
		if len(parts) == 2 && parts[1] == "AGENTS.md" {
			return true
		}
		// list residents/<slug> directory is allowed (to see AGENTS.md), but not docs
		if len(parts) == 1 {
			return true
		}
		return false
	}
	if slashRelativePath == ".cc-agent/harness" {
		return true
	}
	return false
}

func listAllowedHarnessDocPath(relativePath string, absolutePath string) (string, error) {
	info, statError := os.Stat(absolutePath)
	if statError != nil {
		return "", fmt.Errorf("list 失败 %s: %w", relativePath, statError)
	}
	if !info.IsDir() {
		return fmt.Sprintf("path: %s\ntype: file\nsize: %d\n", relativePath, info.Size()), nil
	}
	entries, readError := os.ReadDir(absolutePath)
	if readError != nil {
		return "", fmt.Errorf("list 失败 %s: %w", relativePath, readError)
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "path: %s\ntype: directory\nentries:\n", relativePath)
	count := 0
	for _, entry := range entries {
		// 隐藏 docs 目录，避免主管理误入专属文档。
		if entry.IsDir() && entry.Name() == "docs" &&
			strings.Contains(filepath.ToSlash(relativePath), "/residents/") {
			continue
		}
		entryType := "file"
		if entry.IsDir() {
			entryType = "dir"
		}
		fmt.Fprintf(&builder, "- %s (%s)\n", entry.Name(), entryType)
		count++
		if count >= docsToolMaxListItems {
			builder.WriteString("... truncated ...\n")
			break
		}
	}
	if count == 0 {
		builder.WriteString("(empty)\n")
	}
	return builder.String(), nil
}

func readAllowedHarnessDocPath(relativePath string, absolutePath string) (string, error) {
	info, statError := os.Stat(absolutePath)
	if statError != nil {
		return "", fmt.Errorf("read 失败 %s: %w", relativePath, statError)
	}
	if info.IsDir() {
		return "", fmt.Errorf("path 是目录，请用 operation=list: %s", relativePath)
	}
	// residents 下只允许 AGENTS.md
	slashRelative := filepath.ToSlash(relativePath)
	if strings.Contains(slashRelative, "/residents/") &&
		!strings.HasSuffix(slashRelative, "/AGENTS.md") &&
		slashRelative != ".cc-agent/harness/AGENTS.md" {
		return "", fmt.Errorf("只能读取常驻身份 AGENTS.md，不能读取专属 docs: %s", relativePath)
	}
	content, readError := os.ReadFile(absolutePath)
	if readError != nil {
		return "", fmt.Errorf("read 失败 %s: %w", relativePath, readError)
	}
	if len(content) > docsToolMaxReadBytes {
		content = content[:docsToolMaxReadBytes]
	}
	if !utf8.Valid(content) {
		return "", fmt.Errorf("文件不是有效 UTF-8: %s", relativePath)
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "path: %s\nsize: %d bytes\n---\n", relativePath, info.Size())
	builder.Write(content)
	if info.Size() > int64(docsToolMaxReadBytes) {
		fmt.Fprintf(&builder, "\n---\ntruncated at %d bytes\n", docsToolMaxReadBytes)
	}
	return builder.String(), nil
}
