package service

import "sync"

const conversationEventChannelCapacity = 256

// ConversationEventReceivers 保存每个 conversationId 当前打开的网页事件 channel。
type ConversationEventReceivers struct {
	eventChannelsMutex            sync.RWMutex
	eventChannelsByConversationID map[string]map[chan []byte]struct{}
}

// NewConversationEventReceivers 创建空的网页事件 channel 保存表。
func NewConversationEventReceivers() *ConversationEventReceivers {
	return &ConversationEventReceivers{
		eventChannelsByConversationID: make(
			map[string]map[chan []byte]struct{},
		),
	}
}

// AddReceiver 为一个正在查看会话的网页创建并保存事件 channel。
func (
	conversationEventReceivers *ConversationEventReceivers,
) AddReceiver(conversationID string) chan []byte {
	conversationEventChannel := make(
		chan []byte,
		conversationEventChannelCapacity,
	)

	conversationEventReceivers.eventChannelsMutex.Lock()
	defer conversationEventReceivers.eventChannelsMutex.Unlock()

	eventChannelsForConversation :=
		conversationEventReceivers.
			eventChannelsByConversationID[conversationID]
	if eventChannelsForConversation == nil {
		eventChannelsForConversation = make(map[chan []byte]struct{})
		conversationEventReceivers.
			eventChannelsByConversationID[conversationID] =
			eventChannelsForConversation
	}
	eventChannelsForConversation[conversationEventChannel] = struct{}{}

	return conversationEventChannel
}

// RemoveReceiver 删除一个已经断开的网页事件 channel。
func (
	conversationEventReceivers *ConversationEventReceivers,
) RemoveReceiver(
	conversationID string,
	conversationEventChannel chan []byte,
) {
	conversationEventReceivers.eventChannelsMutex.Lock()
	defer conversationEventReceivers.eventChannelsMutex.Unlock()

	eventChannelsForConversation :=
		conversationEventReceivers.
			eventChannelsByConversationID[conversationID]
	if eventChannelsForConversation == nil {
		return
	}

	delete(eventChannelsForConversation, conversationEventChannel)
	if len(eventChannelsForConversation) == 0 {
		delete(
			conversationEventReceivers.eventChannelsByConversationID,
			conversationID,
		)
	}
}

// SendEventJSON 把同一份 JSON 发送给当前正在查看该会话的全部网页。
// 返回值是成功写入 channel 的网页数量。
func (
	conversationEventReceivers *ConversationEventReceivers,
) SendEventJSON(
	conversationID string,
	eventJSON []byte,
) int {
	conversationEventReceivers.eventChannelsMutex.RLock()
	eventChannelsForConversation :=
		conversationEventReceivers.
			eventChannelsByConversationID[conversationID]
	copiedEventChannels := make(
		[]chan []byte,
		0,
		len(eventChannelsForConversation),
	)
	for conversationEventChannel := range eventChannelsForConversation {
		copiedEventChannels = append(
			copiedEventChannels,
			conversationEventChannel,
		)
	}
	conversationEventReceivers.eventChannelsMutex.RUnlock()

	successfulReceiverCount := 0
	for _, conversationEventChannel := range copiedEventChannels {
		select {
		case conversationEventChannel <- eventJSON:
			successfulReceiverCount++
		default:
		}
	}

	return successfulReceiverCount
}

// ReceiverCount 返回一个会话当前打开的网页事件连接数量。
func (
	conversationEventReceivers *ConversationEventReceivers,
) ReceiverCount(conversationID string) int {
	conversationEventReceivers.eventChannelsMutex.RLock()
	defer conversationEventReceivers.eventChannelsMutex.RUnlock()

	return len(
		conversationEventReceivers.
			eventChannelsByConversationID[conversationID],
	)
}
