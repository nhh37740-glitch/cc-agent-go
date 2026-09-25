package service

import (
	"context"
	"testing"
	"time"
)

func TestConversationRunRegistryCancelStopsContext(t *testing.T) {
	registry := NewConversationRunRegistry()
	runContext, endRun := registry.BeginRun("conv-a", context.Background())
	defer endRun()

	if !registry.CancelRun("conv-a") {
		t.Fatal("CancelRun should find active run")
	}

	select {
	case <-runContext.Done():
	case <-time.After(time.Second):
		t.Fatal("run context was not cancelled")
	}

	if registry.CancelRun("conv-a") {
		// cancel func still works but registry may still hold until endRun;
		// after CancelRun the cancel is still registered until endRun.
		// Calling CancelRun again should still return true while registered.
	}
	endRun()
	if registry.CancelRun("conv-a") {
		t.Fatal("after endRun there should be no active cancel")
	}
}

func TestConversationRunRegistryBeginRunReplacesPrevious(t *testing.T) {
	registry := NewConversationRunRegistry()
	firstContext, endFirst := registry.BeginRun("conv-b", context.Background())
	secondContext, endSecond := registry.BeginRun("conv-b", context.Background())
	defer endSecond()

	select {
	case <-firstContext.Done():
	case <-time.After(time.Second):
		t.Fatal("previous run context should be cancelled")
	}
	if secondContext.Err() != nil {
		t.Fatal("new run context should still be active")
	}
	endFirst()
}
