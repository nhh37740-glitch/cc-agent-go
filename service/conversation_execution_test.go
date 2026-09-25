package service

import (
	"testing"
	"time"
)

func TestConversationExecutionLocksWaitForSameConversation(
	t *testing.T,
) {
	conversationExecutionLocks := NewConversationExecutionLocks()
	unlockFirstExecution :=
		conversationExecutionLocks.LockConversation("conversation-a")

	secondExecutionStarted := make(chan struct{})
	secondExecutionFinished := make(chan struct{})
	go func() {
		close(secondExecutionStarted)
		unlockSecondExecution :=
			conversationExecutionLocks.LockConversation("conversation-a")
		unlockSecondExecution()
		close(secondExecutionFinished)
	}()

	<-secondExecutionStarted
	select {
	case <-secondExecutionFinished:
		t.Fatal("second execution acquired the same conversation lock early")
	case <-time.After(30 * time.Millisecond):
	}

	unlockFirstExecution()
	select {
	case <-secondExecutionFinished:
	case <-time.After(2 * time.Second):
		t.Fatal("second execution did not acquire the released lock")
	}
}

func TestConversationExecutionLocksAllowDifferentConversations(
	t *testing.T,
) {
	conversationExecutionLocks := NewConversationExecutionLocks()
	unlockFirstConversation :=
		conversationExecutionLocks.LockConversation("conversation-a")
	defer unlockFirstConversation()

	secondConversationFinished := make(chan struct{})
	go func() {
		unlockSecondConversation :=
			conversationExecutionLocks.LockConversation("conversation-b")
		unlockSecondConversation()
		close(secondConversationFinished)
	}()

	select {
	case <-secondConversationFinished:
	case <-time.After(2 * time.Second):
		t.Fatal("different conversation was blocked")
	}
}

func TestConversationExecutionLocksTryLock(t *testing.T) {
	conversationExecutionLocks := NewConversationExecutionLocks()

	// 空闲时 TryLock 成功。
	unlockExecution, locked :=
		conversationExecutionLocks.TryLockConversation("conversation-c")
	if !locked {
		t.Fatal("空闲会话 TryLock 应成功")
	}

	// 已持有时 TryLock 失败。
	if _, lockedAgain :=
		conversationExecutionLocks.TryLockConversation("conversation-c"); lockedAgain {
		t.Fatal("已持有会话的 TryLock 应失败")
	}

	unlockExecution()

	// 释放后可再次 TryLock。
	if _, lockedAfterRelease :=
		conversationExecutionLocks.TryLockConversation("conversation-c"); !lockedAfterRelease {
		t.Fatal("释放后 TryLock 应成功")
	}
}
