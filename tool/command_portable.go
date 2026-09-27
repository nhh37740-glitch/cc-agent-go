package tool

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Linux uses the same structured program/args contract as Windows. No shell is
// started, and the whitelist is intentionally smaller than the Windows list.
var linuxExecutables = map[string]string{
	"git":    "git",
	"rg":     "rg",
	"go":     "go",
	"gofmt":  "gofmt",
	"python": "python3",
	"curl":   "curl",
}

const linuxCommandDescription = "在当前 Linux Agent 工作目录中直接执行白名单程序。program 与 args 分开传递，不启动 shell；文件操作优先使用 file 工具。"

func allowedLinuxProgramNames() []string {
	programNames := make([]string, 0, len(linuxExecutables))
	for programName := range linuxExecutables {
		programNames = append(programNames, programName)
	}
	sort.Strings(programNames)
	return programNames
}

func executableLabel(program string) string {
	if runtime.GOOS == "windows" {
		return program + ".exe"
	}
	return program
}

func executeLinuxCommand(
	input map[string]any,
	executionEnvironment ToolExecutionEnvironment,
) (string, error) {
	if err := validateNativeUTF8Input("WorkingDirectory", executionEnvironment.WorkingDirectory); err != nil {
		return "", err
	}
	if strings.TrimSpace(executionEnvironment.WorkingDirectory) == "" {
		return "", fmt.Errorf("command 工具缺少 WorkingDirectory")
	}

	workingDirectory, err := filepath.Abs(executionEnvironment.WorkingDirectory)
	if err != nil {
		return "", fmt.Errorf("解析 WorkingDirectory 失败: %w", err)
	}
	workingDirectoryInfo, err := os.Stat(workingDirectory)
	if err != nil {
		return "", fmt.Errorf("访问 WorkingDirectory 失败: %w", err)
	}
	if !workingDirectoryInfo.IsDir() {
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

	program := strings.ToLower(strings.TrimSpace(programValue))
	if blockedNativePrograms[program] {
		return "", fmt.Errorf("程序 %s 已禁用：不允许启动命令解释器", programValue)
	}
	executableName, allowed := linuxExecutables[program]
	if !allowed {
		return "", fmt.Errorf("程序不在白名单中: %s；允许值为 %s", programValue, strings.Join(allowedLinuxProgramNames(), ", "))
	}

	args, err := readStringArguments(input)
	if err != nil {
		return "", err
	}
	timeoutSeconds, err := readOptionalInt(
		input, "timeout_seconds", defaultCommandTimeoutSeconds, 1, maxCommandTimeoutSeconds,
	)
	if err != nil {
		return "", err
	}
	maxOutputBytes, err := readOptionalInt(
		input, "max_output_bytes", defaultCommandOutputBytes, minCommandOutputBytes, maxCommandOutputBytes,
	)
	if err != nil {
		return "", err
	}

	executablePath, err := exec.LookPath(executableName)
	if err != nil {
		return "", fmt.Errorf("容器内未找到程序 %s: %w", executableName, err)
	}
	executablePath, err = filepath.Abs(executablePath)
	if err != nil {
		return "", fmt.Errorf("解析程序路径失败: %w", err)
	}
	args = prepareNativeArgs(program, args)

	parentContext := executionEnvironment.Context
	if parentContext == nil {
		parentContext = context.Background()
	}
	if parentContext.Err() != nil {
		return "", formatNativeInfrastructureError(
			program, args, executablePath, workingDirectory,
			"启动前会话已取消", "", parentContext.Err(),
		)
	}
	commandContext, cancelCommand := context.WithTimeout(
		parentContext, time.Duration(timeoutSeconds)*time.Second,
	)
	defer cancelCommand()

	command := exec.CommandContext(commandContext, executablePath, args...)
	command.Dir = workingDirectory
	command.Env = sanitizedNativeEnvironment(program)
	output := newCappedOutputWriter(maxOutputBytes)
	command.Stdout = output
	command.Stderr = output
	runError := command.Run()
	return classifyNativeCommandResult(
		program, args, executablePath, workingDirectory,
		runError, output.UTF8String(), commandContext,
	)
}
