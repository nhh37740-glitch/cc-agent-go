package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnsureHarnessWorkspace 在项目目录下创建 Harness 文档树，并写入缺失的默认 AGENTS.md。
// 已有文件不覆盖，避免抹掉用户或 Agent 已维护的内容。
//
// 布局：
//
//	.cc-agent/harness/
//	  AGENTS.md
//	  shared/AGENTS.md
//	  shared/memory.md
//	  shared/docs/
//	  residents/<slug>/AGENTS.md
//	  residents/<slug>/docs/
func EnsureHarnessWorkspace(workingDirectory string) error {
	harnessDirectoryPath, buildHarnessDirectoryError :=
		HarnessDirectoryPath(workingDirectory)
	if buildHarnessDirectoryError != nil {
		return buildHarnessDirectoryError
	}
	directoriesToCreate := []string{
		harnessDirectoryPath,
		filepath.Join(harnessDirectoryPath, "shared"),
		filepath.Join(harnessDirectoryPath, "shared", "docs"),
		filepath.Join(harnessDirectoryPath, "residents"),
	}
	for _, residentDefinition := range PermanentResidents {
		directoriesToCreate = append(
			directoriesToCreate,
			filepath.Join(harnessDirectoryPath, "residents", residentDefinition.Slug),
			filepath.Join(harnessDirectoryPath, "residents", residentDefinition.Slug, "docs"),
		)
	}
	for _, directoryPath := range directoriesToCreate {
		if makeDirectoryError := os.MkdirAll(directoryPath, 0o755); makeDirectoryError != nil {
			return fmt.Errorf("创建 Harness 目录失败 %s: %w", directoryPath, makeDirectoryError)
		}
	}

	defaultFiles := map[string]string{
		filepath.Join(harnessDirectoryPath, "AGENTS.md"):           defaultHarnessManagerAgentsMarkdown(),
		filepath.Join(harnessDirectoryPath, "shared", "AGENTS.md"): defaultSharedAgentsMarkdown(),
		filepath.Join(harnessDirectoryPath, "shared", "memory.md"): defaultSharedMemoryMarkdown(),
	}
	for _, residentDefinition := range PermanentResidents {
		defaultFiles[filepath.Join(
			harnessDirectoryPath,
			"residents",
			residentDefinition.Slug,
			"AGENTS.md",
		)] = defaultResidentAgentsMarkdown(residentDefinition)
	}
	for filePath, defaultContent := range defaultFiles {
		if writeDefaultFileError := writeFileIfMissing(filePath, defaultContent); writeDefaultFileError != nil {
			return writeDefaultFileError
		}
	}
	return nil
}

func writeFileIfMissing(filePath string, content string) error {
	_, statError := os.Stat(filePath)
	if statError == nil {
		return nil
	}
	if !os.IsNotExist(statError) {
		return fmt.Errorf("检查文件失败 %s: %w", filePath, statError)
	}
	if writeError := os.WriteFile(filePath, []byte(content), 0o644); writeError != nil {
		return fmt.Errorf("写入默认文件失败 %s: %w", filePath, writeError)
	}
	return nil
}

func defaultHarnessManagerAgentsMarkdown() string {
	return strings.TrimSpace(`
# Harness 主管理 Agent 规则

你是本项目的编排者，不直接改业务代码或跑长命令。

## 可读取范围（严格）

只能读取：
1. 自己的会话记忆（memory 工具）
2. 本文件：.cc-agent/harness/AGENTS.md
3. 共享规则与共享文件：.cc-agent/harness/shared/**
4. 常驻 Agent 的身份文件：.cc-agent/harness/residents/*/AGENTS.md

禁止读取：
- 常驻 Agent 专属 docs（residents/*/docs/**）
- 任意 Agent 会话 JSON（.cc-agent/sessions/**）
- 项目其他业务源码（需要时派常驻/临时 Agent 去读）

## 派工规则

- 优先复用 4 个常驻 Agent，不要为同一专长重复创建新名字。
- 临时 Agent 只用于一次性、边界清晰、不必沉淀专属文档的任务。
- 每次派工 request 必须写清：目标、约束、交付物路径、完成定义。
- 收到子 Agent 汇报后，把稳定结论写入 shared/memory.md 或 shared/docs/。

## 记忆检索

不要假设模型会自动带上旧对话。需要旧信息时：
1. 用 memory 工具查自己的会话
2. 用 docs 工具读 shared 与身份 AGENTS.md
3. 再决定派谁执行
`) + "\n"
}

func defaultSharedAgentsMarkdown() string {
	return strings.TrimSpace(`
# 项目共享规则（Harness Shared）

本文件所有常驻 Agent 与主管理可读。临时 Agent 只在 request 中接收必要摘要，不维护专属文档。

## 协作约定

- 稳定结论写入 shared/docs/，短记忆追加 shared/memory.md。
- 文件路径一律写相对项目根的路径。
- 失败也要写清：已完成步骤、失败点、下一步建议。

## 常驻分工
`) + "\n\n" + ResidentNamesText() + "\n"
}

func defaultSharedMemoryMarkdown() string {
	return "# 共享记忆\n\n按时间追加稳定事实、决策和未完成事项。不要粘贴大段源码。\n\n"
}

func defaultResidentAgentsMarkdown(residentDefinition ResidentDefinition) string {
	return fmt.Sprintf(
		strings.TrimSpace(`
# 常驻 Agent：%s

## 专长
%s

## 身份说明
%s

## 可读写范围

- 自己的身份：.cc-agent/harness/residents/%s/AGENTS.md
- 自己的专属文档：.cc-agent/harness/residents/%s/docs/
- 共享：.cc-agent/harness/shared/**
- 自己的会话文件（按 system 提示用 command/file 读取必要片段）

禁止读取其他常驻 Agent 的专属 docs 与其他会话 JSON。

## 汇报要求

无论成功失败，最终回复必须包含：
1. 身份与任务
2. 已完成进度
3. 结果或失败原因
4. 产物路径（如有）
5. 建议主管理下一步
`)+"\n",
		residentDefinition.Name,
		residentDefinition.Specialty,
		strings.TrimSpace(residentDefinition.DefaultBrief),
		residentDefinition.Slug,
		residentDefinition.Slug,
	)
}
