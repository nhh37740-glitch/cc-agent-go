package service

import "sync"

// ConversationExecutionLocks 保存每个 conversationId 的主 Agent执行锁。
type ConversationExecutionLocks struct {
	conversationLocksMutex sync.Mutex
	locksByConversationID  map[string]*sync.Mutex
}

// NewConversationExecutionLocks 创建空的主 Agent执行锁保存表。
func NewConversationExecutionLocks() *ConversationExecutionLocks {
	return &ConversationExecutionLocks{
		locksByConversationID: make(map[string]*sync.Mutex),
	}
}

// LockConversation 等待并取得指定会话的主 Agent执行锁。
// 返回的函数负责解除这把锁。
func (
	conversationExecutionLocks *ConversationExecutionLocks,
) LockConversation(conversationID string) func() {
	conversationExecutionLocks.conversationLocksMutex.Lock()
	conversationExecutionLock :=
		conversationExecutionLocks.locksByConversationID[conversationID]
	if conversationExecutionLock == nil {
		conversationExecutionLock = &sync.Mutex{}
		conversationExecutionLocks.locksByConversationID[conversationID] =
			conversationExecutionLock
	}
	conversationExecutionLocks.conversationLocksMutex.Unlock()

	conversationExecutionLock.Lock()
	return conversationExecutionLock.Unlock
}

// TryLockConversation 尝试立即取得指定会话的主 Agent执行锁，不等待。
// 成功时返回解锁函数和 true；失败（锁被其他执行持有）时返回 nil 和 false。
// 用于完成队列消费者：主管理对话持锁时不死等，而是稍后重试或交给对话前处理。
func (
	conversationExecutionLocks *ConversationExecutionLocks,
) TryLockConversation(conversationID string) (func(), bool) {
	conversationExecutionLocks.conversationLocksMutex.Lock()
	conversationExecutionLock :=
		conversationExecutionLocks.locksByConversationID[conversationID]
	if conversationExecutionLock == nil {
		conversationExecutionLock = &sync.Mutex{}
		conversationExecutionLocks.locksByConversationID[conversationID] =
			conversationExecutionLock
	}
	conversationExecutionLocks.conversationLocksMutex.Unlock()

	if !conversationExecutionLock.TryLock() {
		return nil, false
	}
	return conversationExecutionLock.Unlock, true
}
