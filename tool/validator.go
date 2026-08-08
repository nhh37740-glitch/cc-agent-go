package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidatePath 校验给定路径是否在本次工作目录内，防止目录逃逸。
//
// 三步校验：
//  1. filepath.Abs — 把 workingDirectory 解析为绝对路径
//  2. filepath.Join + filepath.Clean — 把用户输入拼到工作目录下
//  3. strings.HasPrefix — 确认清理后的结果仍在工作目录内
//
// 返回安全的绝对路径，或者 error。
func ValidatePath(workingDirectory string, relativePath string) (string, error) {
	absoluteWorkingDirectory, err := filepath.Abs(workingDirectory)
	if err != nil {
		return "", fmt.Errorf("解析工作目录失败: %w", err)
	}

	absoluteWorkingDirectory = filepath.Clean(absoluteWorkingDirectory)

	fullPath := filepath.Join(absoluteWorkingDirectory, relativePath)
	fullPath = filepath.Clean(fullPath)

	if !strings.HasPrefix(
		strings.ToLower(fullPath),
		strings.ToLower(absoluteWorkingDirectory+string(os.PathSeparator)),
	) && fullPath != absoluteWorkingDirectory {
		return "", fmt.Errorf("路径逃逸: %s 不在工作目录内", relativePath)
	}

	return fullPath, nil
}
