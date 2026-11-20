// Package events is a small in-process pub/sub used to feed the dashboard's live stream.
package events

import (
	"sync"
	"time"
)

// Event is one item on the live stream.
type Event struct {
	Type string `json:"type"`
	At   string `json:"at"`
	Data any    `json:"data"`
}

// Hub fans events out to subscribers. Slow subscribers drop events rather than block the broker.
type Hub struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

// NewHub returns an empty hub.
func NewHub() *Hub { return &Hub{subs: map[chan Event]struct{}{}} }

// Publish sends an event to every subscriber without blocking.
func (h *Hub) Publish(typ string, data any) {
	ev := Event{Type: typ, At: time.Now().UTC().Format(time.RFC3339Nano), Data: data}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Subscribe returns a channel of events and a function that ends the subscription.
func (h *Hub) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 256)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}
