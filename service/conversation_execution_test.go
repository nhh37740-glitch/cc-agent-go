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
