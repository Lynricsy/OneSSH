package events

import (
	"encoding/json"
	"sync"
	"time"
)

type Event struct {
	Type string `json:"type"`
	Ts   int64  `json:"ts"`
	Data any    `json:"data"`
}
type Bus struct {
	mu   sync.RWMutex
	next int
	subs map[int]chan Event
}

func New() *Bus { return &Bus{subs: make(map[int]chan Event)} }
func (b *Bus) Publish(kind string, data any) {
	e := Event{Type: kind, Ts: time.Now().UnixMilli(), Data: data}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subs {
		select {
		case ch <- e:
		default:
		}
	}
}
func (b *Bus) Subscribe() (<-chan Event, func()) {
	b.mu.Lock()
	id := b.next
	b.next++
	ch := make(chan Event, 64)
	b.subs[id] = ch
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		if old, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(old)
		}
		b.mu.Unlock()
	}
}
func (e Event) JSON() []byte { b, _ := json.Marshal(e); return b }
