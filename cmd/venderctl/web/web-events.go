package web

import "sync"

type userKey struct {
	id       int64
	userType int32
}

type EventBus struct {
	mu       sync.RWMutex
	channels map[userKey][]chan interface{}
}

func NewEventBus() *EventBus {
	return &EventBus{
		channels: make(map[userKey][]chan interface{}),
	}
}

func (b *EventBus) Subscribe(userId int64, userType int32) chan interface{} {
	ch := make(chan interface{}, 10)
	key := userKey{userId, userType}
	b.mu.Lock()
	b.channels[key] = append(b.channels[key], ch)
	b.mu.Unlock()
	return ch
}

func (b *EventBus) Unsubscribe(userId int64, userType int32, ch chan interface{}) {
	key := userKey{userId, userType}
	b.mu.Lock()
	defer b.mu.Unlock()
	arr := b.channels[key]
	for i, c := range arr {
		if c == ch {
			b.channels[key] = append(arr[:i], arr[i+1:]...)
			close(c)
			break
		}
	}
}

func (b *EventBus) Publish(userId int64, userType int32, event interface{}) {
	key := userKey{userId, userType}
	b.mu.RLock()
	arr := b.channels[key]
	b.mu.RUnlock()
	for _, ch := range arr {
		select {
		case ch <- event:
		default:
		}
	}
}

// func (b *EventBus) channelCount(userId int64, userType int32) int {
// 	key := userKey{userId, userType}
// 	b.mu.RLock()
// 	defer b.mu.RUnlock()
// 	return len(b.channels[key])
// }
