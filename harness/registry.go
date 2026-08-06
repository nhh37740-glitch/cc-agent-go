package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ManagedAgentStatus 是被管理 Agent 的运行状态。
type ManagedAgentStatus string

const (
	ManagedAgentStatusIdle      ManagedAgentStatus = "idle"
	ManagedAgentStatusRunning   ManagedAgentStatus = "running"
	ManagedAgentStatusCompleted ManagedAgentStatus = "completed"
	ManagedAgentStatusFailed    ManagedAgentStatus = "failed"
)

// ManagedAgentRecord 是被管理 Agent 的运行元数据。
// 角色与职责不存注册表——由 Agent 自己的会话记忆持久保存。
type ManagedAgentRecord struct {
	Name            string             `json:"name"`
	Slug            string             `json:"slug"`
	ConversationID  string             `json:"conversationId"`
	Status          ManagedAgentStatus `json:"status"`
	Result          string             `json:"result,omitempty"`
	ResultCollected bool               `json:"resultCollected"`
	LastError       string             `json:"lastError,omitempty"`
	TaskCount       int                `json:"taskCount"`
	CreatedAt       float64            `json:"createdAt"`
	UpdatedAt       float64            `json:"updatedAt"`
}

type agentRegistryFile struct {
	Agents []*ManagedAgentRecord `json:"agents"`
}

// AgentRegistry 保存一个项目目录下的全部被管理 Agent，整体落盘到
// <workingDirectory>/.cc-agent/harness/agents.json。
type AgentRegistry struct {
	registryMutex    sync.RWMutex
	registryFilePath string
	maximumAgents    int
	agentsByName     map[string]*ManagedAgentRecord
	creationOrder    []string
}

// LoadAgentRegistry 从磁盘加载注册表；文件不存在时返回空注册表。
// maximumAgents 小于 1 时回退 DefaultMaximumAgents。
func LoadAgentRegistry(
	workingDirectory string,
	maximumAgents int,
) (*AgentRegistry, error) {
	if maximumAgents < 1 {
		maximumAgents = DefaultMaximumAgents
	}
	registryFilePath, buildRegistryFilePathError :=
		AgentRegistryFilePath(workingDirectory)
	if buildRegistryFilePathError != nil {
		return nil, buildRegistryFilePathError
	}
	loadedRegistry := &AgentRegistry{
		registryFilePath: registryFilePath,
		maximumAgents:    maximumAgents,
		agentsByName:     make(map[string]*ManagedAgentRecord),
		creationOrder:    make([]string, 0),
	}

	registryFileContent, readRegistryFileError := os.ReadFile(registryFilePath)
	if os.IsNotExist(readRegistryFileError) {
		return loadedRegistry, nil
	}
	if readRegistryFileError != nil {
		return nil, fmt.Errorf("读取 Agent 注册表失败: %w", readRegistryFileError)
	}
	var decodedRegistryFile agentRegistryFile
	if decodeRegistryError := json.Unmarshal(
		registryFileContent,
		&decodedRegistryFile,
	); decodeRegistryError != nil {
		return nil, fmt.Errorf("Agent 注册表 JSON 无效: %w", decodeRegistryError)
	}
	for _, agentRecord := range decodedRegistryFile.Agents {
		if agentRecord == nil || agentRecord.Name == "" {
			continue
		}
		loadedRegistry.agentsByName[agentRecord.Name] = agentRecord
		loadedRegistry.creationOrder = append(
			loadedRegistry.creationOrder,
			agentRecord.Name,
		)
	}
	return loadedRegistry, nil
}

func currentEpochSeconds() float64 {
	return float64(time.Now().UnixNano()) / 1e9
}

// UpsertAgent 按名字取得或创建 Agent 记录；created=true 表示本次新建。
// 达到池容量上限时返回包含当前名单和补救引导的错误。
func (agentRegistry *AgentRegistry) UpsertAgent(
	agentName string,
) (ManagedAgentRecord, bool, error) {
	trimmedAgentName := strings.TrimSpace(agentName)
	if trimmedAgentName == "" {
		return ManagedAgentRecord{}, false, fmt.Errorf("agent 名称不能为空")
	}

	agentRegistry.registryMutex.Lock()
	defer agentRegistry.registryMutex.Unlock()

	existingRecord, alreadyExists := agentRegistry.agentsByName[trimmedAgentName]
	if alreadyExists {
		existingRecord.UpdatedAt = currentEpochSeconds()
		if saveError := agentRegistry.saveLocked(); saveError != nil {
			return ManagedAgentRecord{}, false, saveError
		}
		return *existingRecord, false, nil
	}

	if len(agentRegistry.agentsByName) >= agentRegistry.maximumAgents {
		return ManagedAgentRecord{}, false, fmt.Errorf(
			"Agent 数量已达上限 %d（当前 %d 个：%s）。"+
				"请先用 forget 释放不再需要的 Agent，或复用现有 Agent",
			agentRegistry.maximumAgents,
			len(agentRegistry.agentsByName),
			strings.Join(agentRegistry.agentNamesLocked(), ", "),
		)
	}

	agentSlug := agentRegistry.availableSlugLocked(trimmedAgentName)
	now := currentEpochSeconds()
	newRecord := &ManagedAgentRecord{
		Name:            trimmedAgentName,
		Slug:            agentSlug,
		ConversationID:  ManagedAgentConversationID(agentSlug),
		Status:          ManagedAgentStatusIdle,
		ResultCollected: true,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	agentRegistry.agentsByName[trimmedAgentName] = newRecord
	agentRegistry.creationOrder = append(
		agentRegistry.creationOrder,
		trimmedAgentName,
	)
	if saveError := agentRegistry.saveLocked(); saveError != nil {
		return ManagedAgentRecord{}, false, saveError
	}
	return *newRecord, true, nil
}

// availableSlugLocked 生成不与其他名称冲突的 slug（冲突时追加 -2、-3……）。
// 调用前必须持有写锁。
func (agentRegistry *AgentRegistry) availableSlugLocked(agentName string) string {
	baseSlug := SlugForAgentName(agentName, len(agentRegistry.creationOrder)+1)
	candidateSlug := baseSlug
	conflictIndex := 2
	for {
		slugUsedByOtherName := false
		for existingName, existingRecord := range agentRegistry.agentsByName {
			if existingRecord.Slug == candidateSlug && existingName != agentName {
				slugUsedByOtherName = true
				break
			}
		}
		if !slugUsedByOtherName {
			return candidateSlug
		}
		candidateSlug = fmt.Sprintf("%s-%d", baseSlug, conflictIndex)
		conflictIndex++
	}
}

// ForgetAgent 从注册表移除一个 Agent 并释放名额；会话文件保留，
// 同名 Agent 再次创建时记忆自动延续。
func (agentRegistry *AgentRegistry) ForgetAgent(
	agentName string,
) (ManagedAgentRecord, error) {
	agentRegistry.registryMutex.Lock()
	defer agentRegistry.registryMutex.Unlock()

	existingRecord, alreadyExists := agentRegistry.agentsByName[agentName]
	if !alreadyExists {
		return ManagedAgentRecord{}, fmt.Errorf("没有名为 %q 的 Agent", agentName)
	}
	delete(agentRegistry.agentsByName, agentName)
	for index, orderedName := range agentRegistry.creationOrder {
		if orderedName == agentName {
			agentRegistry.creationOrder = append(
				agentRegistry.creationOrder[:index],
				agentRegistry.creationOrder[index+1:]...,
			)
			break
		}
	}
	if saveError := agentRegistry.saveLocked(); saveError != nil {
		return ManagedAgentRecord{}, saveError
	}
	return *existingRecord, nil
}

// GetAgent 按名字读取一条记录的副本。
func (agentRegistry *AgentRegistry) GetAgent(
	agentName string,
) (ManagedAgentRecord, bool) {
	agentRegistry.registryMutex.RLock()
	defer agentRegistry.registryMutex.RUnlock()
	agentRecord, found := agentRegistry.agentsByName[agentName]
	if !found {
		return ManagedAgentRecord{}, false
	}
	return *agentRecord, true
}

// ListAgents 按创建顺序返回全部记录的副本。
func (agentRegistry *AgentRegistry) ListAgents() []ManagedAgentRecord {
	agentRegistry.registryMutex.RLock()
	defer agentRegistry.registryMutex.RUnlock()
	orderedRecords := make([]ManagedAgentRecord, 0, len(agentRegistry.creationOrder))
	for _, agentName := range agentRegistry.creationOrder {
		orderedRecords = append(
			orderedRecords,
			*agentRegistry.agentsByName[agentName],
		)
	}
	return orderedRecords
}

// agentNamesLocked 按创建顺序返回名称（调用前必须持有锁）。
func (agentRegistry *AgentRegistry) agentNamesLocked() []string {
	agentNames := make([]string, 0, len(agentRegistry.creationOrder))
	agentNames = append(agentNames, agentRegistry.creationOrder...)
	return agentNames
}

// MarkRunning 标记 Agent 开始执行新任务，清空上一次结果。
func (agentRegistry *AgentRegistry) MarkRunning(agentName string) error {
	agentRegistry.registryMutex.Lock()
	defer agentRegistry.registryMutex.Unlock()
	agentRecord, found := agentRegistry.agentsByName[agentName]
	if !found {
		return fmt.Errorf("没有名为 %q 的 Agent", agentName)
	}
	agentRecord.Status = ManagedAgentStatusRunning
	agentRecord.Result = ""
	agentRecord.ResultCollected = true
	agentRecord.LastError = ""
	agentRecord.UpdatedAt = currentEpochSeconds()
	return agentRegistry.saveLocked()
}

// MarkCompleted 标记完成并写入格式化结果，等待检查循环收取。
func (agentRegistry *AgentRegistry) MarkCompleted(
	agentName string,
	formattedResult string,
) error {
	agentRegistry.registryMutex.Lock()
	defer agentRegistry.registryMutex.Unlock()
	agentRecord, found := agentRegistry.agentsByName[agentName]
	if !found {
		return fmt.Errorf("没有名为 %q 的 Agent", agentName)
	}
	agentRecord.Status = ManagedAgentStatusCompleted
	agentRecord.Result = formattedResult
	agentRecord.ResultCollected = false
	agentRecord.LastError = ""
	agentRecord.TaskCount++
	agentRecord.UpdatedAt = currentEpochSeconds()
	return agentRegistry.saveLocked()
}

// MarkFailed 标记失败并写入错误，等待检查循环收取。
func (agentRegistry *AgentRegistry) MarkFailed(agentName string, cause error) error {
	agentRegistry.registryMutex.Lock()
	defer agentRegistry.registryMutex.Unlock()
	agentRecord, found := agentRegistry.agentsByName[agentName]
	if !found {
		return fmt.Errorf("没有名为 %q 的 Agent", agentName)
	}
	agentRecord.Status = ManagedAgentStatusFailed
	agentRecord.Result = ""
	agentRecord.ResultCollected = false
	if cause != nil {
		agentRecord.LastError = cause.Error()
	}
	agentRecord.TaskCount++
	agentRecord.UpdatedAt = currentEpochSeconds()
	return agentRegistry.saveLocked()
}

// CollectFinishedResults 取出全部「已完成/失败且未收取」的记录副本，
// 并把它们标记为已收取；没有待收取记录时返回空切片。
func (agentRegistry *AgentRegistry) CollectFinishedResults() ([]ManagedAgentRecord, error) {
	agentRegistry.registryMutex.Lock()
	defer agentRegistry.registryMutex.Unlock()
	finishedRecords := make([]ManagedAgentRecord, 0)
	for _, agentName := range agentRegistry.creationOrder {
		agentRecord := agentRegistry.agentsByName[agentName]
		if agentRecord.ResultCollected {
			continue
		}
		if agentRecord.Status != ManagedAgentStatusCompleted &&
			agentRecord.Status != ManagedAgentStatusFailed {
			continue
		}
		agentRecord.ResultCollected = true
		agentRecord.UpdatedAt = currentEpochSeconds()
		finishedRecords = append(finishedRecords, *agentRecord)
	}
	if len(finishedRecords) == 0 {
		return finishedRecords, nil
	}
	if saveError := agentRegistry.saveLocked(); saveError != nil {
		return nil, saveError
	}
	return finishedRecords, nil
}

// RunningAgentCount 返回当前处于 running 状态的 Agent 数量。
func (agentRegistry *AgentRegistry) RunningAgentCount() int {
	agentRegistry.registryMutex.RLock()
	defer agentRegistry.registryMutex.RUnlock()
	runningAgentCount := 0
	for _, agentRecord := range agentRegistry.agentsByName {
		if agentRecord.Status == ManagedAgentStatusRunning {
			runningAgentCount++
		}
	}
	return runningAgentCount
}

// MarkResultCollected 把一条记录标记为已收取，供完成队列消费者逐条收取。
func (agentRegistry *AgentRegistry) MarkResultCollected(agentName string) error {
	agentRegistry.registryMutex.Lock()
	defer agentRegistry.registryMutex.Unlock()
	agentRecord, found := agentRegistry.agentsByName[agentName]
	if !found {
		return fmt.Errorf("没有名为 %q 的 Agent", agentName)
	}
	agentRecord.ResultCollected = true
	agentRecord.UpdatedAt = currentEpochSeconds()
	return agentRegistry.saveLocked()
}

func (agentRegistry *AgentRegistry) AgentCount() int {
	agentRegistry.registryMutex.RLock()
	defer agentRegistry.registryMutex.RUnlock()
	return len(agentRegistry.agentsByName)
}

func (agentRegistry *AgentRegistry) MaximumAgents() int {
	return agentRegistry.maximumAgents
}

// saveLocked 把注册表整体写入 agents.json（调用前必须持有写锁）。
func (agentRegistry *AgentRegistry) saveLocked() error {
	recordsToSave := make([]*ManagedAgentRecord, 0, len(agentRegistry.creationOrder))
	for _, agentName := range agentRegistry.creationOrder {
		recordsToSave = append(recordsToSave, agentRegistry.agentsByName[agentName])
	}
	registryFileContent, encodeRegistryError := json.MarshalIndent(
		agentRegistryFile{Agents: recordsToSave},
		"",
		"  ",
	)
	if encodeRegistryError != nil {
		return fmt.Errorf("编码 Agent 注册表失败: %w", encodeRegistryError)
	}
	if makeDirectoryError := os.MkdirAll(
		filepath.Dir(agentRegistry.registryFilePath),
		0o755,
	); makeDirectoryError != nil {
		return fmt.Errorf("创建 harness 目录失败: %w", makeDirectoryError)
	}
	if writeFileError := os.WriteFile(
		agentRegistry.registryFilePath,
		registryFileContent,
		0o644,
	); writeFileError != nil {
		return fmt.Errorf("写入 Agent 注册表失败: %w", writeFileError)
	}
	return nil
}
