package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidatePath 校验给定路径是否在 workspace 目录内，防止目录逃逸。
//
// 三步校验：
//  1. filepath.Abs — 把 workspace 解析为绝对路径（如 "workspace" → "/abs/path/workspace"）
//  2. filepath.Join + filepath.Clean — 把用户输入拼到 workspace 下，清理 ../ 和 ./
//  3. strings.HasPrefix — 确认清理后的结果仍然以 workspace 绝对路径开头
//
// 返回安全的绝对路径，或者 error。
func ValidatePath(workspace string, relPath string) (string, error) {
	// 第一步：workspace 转绝对路径
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("解析 workspace 路径失败: %w", err)
	}

	// 同时再 Clean 一次，保证 workspace 本身是干净的
	absWorkspace = filepath.Clean(absWorkspace)

	// 第二步：拼接用户输入路径，再 Clean
	fullPath := filepath.Join(absWorkspace, relPath)
	fullPath = filepath.Clean(fullPath)

	// 第三步：确认结果在 workspace 范围内
	// Windows 盘符大小写不一致也会导致 HasPrefix 失败，这里先转小写再比较
	if !strings.HasPrefix(strings.ToLower(fullPath), strings.ToLower(absWorkspace+string(os.PathSeparator))) &&
		fullPath != absWorkspace {
		return "", fmt.Errorf("路径逃逸: %s 不在 workspace 内", relPath)
	}

	return fullPath, nil
}
