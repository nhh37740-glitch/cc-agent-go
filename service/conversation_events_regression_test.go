package service

import (
	"testing"
	"time"
)

// TestConversationEventReceiversFanOutRegression 固定事件扇出行为
// （v16 任务 1.2）：同会话多接收者都收到、跨会话隔离、按 ID 移除、
// 缓冲满时丢弃而不是阻塞。
func TestConversationEventReceiversFanOutRegression(t *testing.T) {
	receivers := NewConversationEventReceivers()
	firstChannel := receivers.AddReceiver("conv-1")
	secondChannel := receivers.AddReceiver("conv-1")
	otherConversationChannel := receivers.AddReceiver("conv-2")
	defer receivers.RemoveReceiver("conv-1", firstChannel)
	defer receivers.RemoveReceiver("conv-1", secondChannel)
	defer receivers.RemoveReceiver("conv-2", otherConversationChannel)

	if count := receivers.ReceiverCount("conv-1"); count != 2 {
		t.Fatalf("conv-1 接收者数量 = %d，期望 2", count)
	}

	sentCount := receivers.SendEventJSON("conv-1", []byte(`{"type":"ping"}`))
	if sentCount != 2 {
		t.Fatalf("SendEventJSON 返回 = %d，期望 2", sentCount)
	}
	for channelIndex, eventChannel := range []chan []byte{firstChannel, secondChannel} {
		select {
		case receivedJSON := <-eventChannel:
			if string(receivedJSON) != `{"type":"ping"}` {
				t.Fatalf("接收者 %d 收到 %q", channelIndex, receivedJSON)
			}
		case <-time.After(time.Second):
			t.Fatalf("接收者 %d 一秒内没有收到事件", channelIndex)
		}
	}
	select {
	case unexpected := <-otherConversationChannel:
		t.Fatalf("conv-2 不应收到事件，却收到 %q", unexpected)
	case <-time.After(50 * time.Millisecond):
	}

	if count := receivers.SendEventJSON("missing-conv", []byte("{}")); count != 0 {
		t.Fatalf("无接收者会话 SendEventJSON 返回 = %d，期望 0", count)
	}

	receivers.RemoveReceiver("conv-1", secondChannel)
	if count := receivers.ReceiverCount("conv-1"); count != 1 {
		t.Fatalf("移除一个接收者后数量 = %d，期望 1", count)
	}

	// 缓冲容量为 256：连续发送 300 次必须全部立即返回（满后丢弃）。
	fullChannel := receivers.AddReceiver("conv-full")
	defer receivers.RemoveReceiver("conv-full", fullChannel)
	for sendIndex := 0; sendIndex < 300; sendIndex++ {
		receivers.SendEventJSON("conv-full", []byte("{}"))
	}
	if count := receivers.SendEventJSON("conv-full", []byte("{}")); count != 0 {
		t.Fatalf("缓冲已满后 SendEventJSON 返回 = %d，期望 0（丢弃而非阻塞）", count)
	}
}
