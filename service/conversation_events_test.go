package service

import (
	"bytes"
	"sync"
	"testing"
)

func TestConversationEventReceiversSendToEveryReceiverInSelectedConversation(
	t *testing.T,
) {
	conversationEventReceivers := NewConversationEventReceivers()
	firstConversationFirstReceiver :=
		conversationEventReceivers.AddReceiver("conversation-a")
	firstConversationSecondReceiver :=
		conversationEventReceivers.AddReceiver("conversation-a")
	secondConversationReceiver :=
		conversationEventReceivers.AddReceiver("conversation-b")

	eventJSON := []byte(`{"type":"background_reply_started"}`)
	successfulReceiverCount :=
		conversationEventReceivers.SendEventJSON(
			"conversation-a",
			eventJSON,
		)

	if successfulReceiverCount != 2 {
		t.Fatalf(
			"SendEventJSON receiver count = %d, want 2",
			successfulReceiverCount,
		)
	}

	for receiverIndex, conversationEventChannel := range []chan []byte{
		firstConversationFirstReceiver,
		firstConversationSecondReceiver,
	} {
		select {
		case receivedEventJSON := <-conversationEventChannel:
			if !bytes.Equal(receivedEventJSON, eventJSON) {
				t.Fatalf(
					"receiver %d received %q, want %q",
					receiverIndex,
					receivedEventJSON,
					eventJSON,
				)
			}
		default:
			t.Fatalf("receiver %d did not receive event", receiverIndex)
		}
	}

	select {
	case receivedEventJSON := <-secondConversationReceiver:
		t.Fatalf(
			"conversation-b unexpectedly received %q",
			receivedEventJSON,
		)
	default:
	}
}

func TestConversationEventReceiversRemoveReceiver(
	t *testing.T,
) {
	conversationEventReceivers := NewConversationEventReceivers()
	conversationEventChannel :=
		conversationEventReceivers.AddReceiver("conversation-a")

	conversationEventReceivers.RemoveReceiver(
		"conversation-a",
		conversationEventChannel,
	)

	if receiverCount :=
		conversationEventReceivers.ReceiverCount("conversation-a"); receiverCount != 0 {
		t.Fatalf("ReceiverCount = %d, want 0", receiverCount)
	}
	if successfulReceiverCount :=
		conversationEventReceivers.SendEventJSON(
			"conversation-a",
			[]byte(`{"type":"ignored"}`),
		); successfulReceiverCount != 0 {
		t.Fatalf(
			"SendEventJSON receiver count = %d, want 0",
			successfulReceiverCount,
		)
	}
}

func TestConversationEventReceiversConcurrentAddSendAndRemove(
	t *testing.T,
) {
	conversationEventReceivers := NewConversationEventReceivers()
	var receiverOperations sync.WaitGroup

	for receiverIndex := 0; receiverIndex < 50; receiverIndex++ {
		receiverOperations.Add(1)
		go func() {
			defer receiverOperations.Done()
			conversationEventChannel :=
				conversationEventReceivers.AddReceiver("conversation-a")
			conversationEventReceivers.SendEventJSON(
				"conversation-a",
				[]byte(`{"type":"concurrent"}`),
			)
			conversationEventReceivers.RemoveReceiver(
				"conversation-a",
				conversationEventChannel,
			)
		}()
	}

	receiverOperations.Wait()

	if receiverCount :=
		conversationEventReceivers.ReceiverCount("conversation-a"); receiverCount != 0 {
		t.Fatalf("ReceiverCount = %d, want 0", receiverCount)
	}
}
