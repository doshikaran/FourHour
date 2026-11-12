// Package stream fans replay events out to Server-Sent Events subscribers.
package stream

import (
	"encoding/json"
	"fmt"
	"sync"
)

// Message is one SSE message: an event name and its JSON body.
type Message struct {
	Event string
	Data  []byte
}

// Format writes the message in SSE wire format.
func (m Message) Format() []byte {
	return []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", m.Event, m.Data))
}

// Sub is one subscriber. C is closed when the hub drops it.
type Sub struct {
	C chan Message
}

// Hub is safe for concurrent use. A subscriber that falls more than its
// buffer behind is dropped (its channel closed); the client reconnects and
// gets a fresh snapshot.
type Hub struct {
	mu     sync.Mutex
	subs   map[*Sub]struct{}
	buffer int
}

// NewHub returns a hub whose subscribers buffer up to buffer messages.
func NewHub(buffer int) *Hub {
	if buffer < 1 {
		buffer = 1
	}
	return &Hub{subs: map[*Sub]struct{}{}, buffer: buffer}
}

// Subscribe adds a subscriber.
func (h *Hub) Subscribe() *Sub {
	s := &Sub{C: make(chan Message, h.buffer)}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s
}

// Unsubscribe removes a subscriber and closes its channel. Safe to call twice.
func (h *Hub) Unsubscribe(s *Sub) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[s]; ok {
		delete(h.subs, s)
		close(s.C)
	}
}

// Publish marshals body and sends it to every subscriber without blocking.
func (h *Hub) Publish(event string, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("stream: marshal %s: %w", event, err)
	}
	m := Message{Event: event, Data: b}
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		select {
		case s.C <- m:
		default:
			delete(h.subs, s)
			close(s.C)
		}
	}
	return nil
}

// Len returns the number of subscribers.
func (h *Hub) Len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}
