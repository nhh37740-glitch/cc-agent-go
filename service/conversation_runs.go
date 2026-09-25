package service

import (
	"context"
	"sync"
)

type conversationRunCancelEntry struct {
	generation uint64
	cancel     context.CancelFunc
}

// ConversationRunRegistry 保存每个会话当前正在执行的取消函数。
// 前端点“停止”、或 HTTP 请求断开时，调用 CancelRun 结束该会话本次 Agent.Run。
type ConversationRunRegistry struct {
	registryMutex          sync.Mutex
	nextGeneration         uint64
	cancelByConversationID map[string]conversationRunCancelEntry
}

// NewConversationRunRegistry 创建空的会话运行取消登记表。
func NewConversationRunRegistry() *ConversationRunRegistry {
	return &ConversationRunRegistry{
		cancelByConversationID: make(map[string]conversationRunCancelEntry),
	}
}

// BeginRun 为 conversationID 创建可取消的运行上下文。
// parentContext 通常是 HTTP 请求上下文：客户端断开时自动取消。
// 返回的 endRun 必须 defer：仅当仍是本次运行时才从登记表移除，并调用 cancel。
func (
	conversationRunRegistry *ConversationRunRegistry,
) BeginRun(
	conversationID string,
	parentContext context.Context,
) (context.Context, func()) {
	if parentContext == nil {
		parentContext = context.Background()
	}
	runContext, cancelRun := context.WithCancel(parentContext)

	conversationRunRegistry.registryMutex.Lock()
	conversationRunRegistry.nextGeneration++
	runGeneration := conversationRunRegistry.nextGeneration
	previousEntry := conversationRunRegistry.cancelByConversationID[conversationID]
	conversationRunRegistry.cancelByConversationID[conversationID] =
		conversationRunCancelEntry{
			generation: runGeneration,
			cancel:     cancelRun,
		}
	conversationRunRegistry.registryMutex.Unlock()

	// 理论上同会话有执行锁，不应重叠；若仍有旧 cancel，先取消旧的。
	if previousEntry.cancel != nil {
		previousEntry.cancel()
	}

	endRun := func() {
		conversationRunRegistry.registryMutex.Lock()
		currentEntry := conversationRunRegistry.cancelByConversationID[conversationID]
		if currentEntry.generation == runGeneration {
			delete(conversationRunRegistry.cancelByConversationID, conversationID)
		}
		conversationRunRegistry.registryMutex.Unlock()
		cancelRun()
	}
	return runContext, endRun
}

// CancelRun 取消指定会话当前正在执行的 Agent.Run。
// 返回 true 表示当时存在可取消的运行。
func (
	conversationRunRegistry *ConversationRunRegistry,
) CancelRun(conversationID string) bool {
	conversationRunRegistry.registryMutex.Lock()
	currentEntry := conversationRunRegistry.cancelByConversationID[conversationID]
	conversationRunRegistry.registryMutex.Unlock()
	if currentEntry.cancel == nil {
		return false
	}
	currentEntry.cancel()
	return true
}
