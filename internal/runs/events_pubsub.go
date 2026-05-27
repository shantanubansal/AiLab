// In-process pubsub for run lifecycle events.
//
// StatusConsumer broadcasts the run.started / run.completed messages it
// receives from NATS into this hub; the SSE handler in events.go
// subscribes to per-request channels filtered by runId.
//
// Buffered, non-blocking semantics: a slow subscriber's events drop
// after the channel fills up. The SSE handler is expected to read
// continuously, so this only protects the broadcaster from one
// disconnected client wedging the whole hub.

package runs

import (
	"sync"

	"github.com/shantanubansal/AiLab/pkg/events"
)

// RunEvent is one lifecycle notification carried over the hub.
type RunEvent struct {
	Kind      string                `json:"kind"` // "started" | "completed"
	Started   *events.RunStarted    `json:"started,omitempty"`
	Completed *events.RunCompleted  `json:"completed,omitempty"`
}

// Hub fans out RunEvents to per-subscriber channels.
type Hub struct {
	mu   sync.Mutex
	next int
	subs map[int]subscription
}

type subscription struct {
	runID string
	ch    chan RunEvent
}

// NewHub returns an empty Hub.
func NewHub() *Hub {
	return &Hub{subs: make(map[int]subscription)}
}

// Subscribe registers a listener for events from a single run. Returns
// the channel and a cancel function the caller must invoke when done.
// The channel is buffered (32) so a brief receiver stall doesn't drop
// events; once the buffer fills, additional events for this subscriber
// are dropped silently.
func (h *Hub) Subscribe(runID string) (<-chan RunEvent, func()) {
	h.mu.Lock()
	id := h.next
	h.next++
	ch := make(chan RunEvent, 32)
	h.subs[id] = subscription{runID: runID, ch: ch}
	h.mu.Unlock()
	cancel := func() {
		h.mu.Lock()
		if s, ok := h.subs[id]; ok {
			close(s.ch)
			delete(h.subs, id)
		}
		h.mu.Unlock()
	}
	return ch, cancel
}

// Broadcast fans an event out to every subscriber whose runID matches.
// Non-matching subscribers don't pay any cost beyond a string compare.
func (h *Hub) Broadcast(runID string, ev RunEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, s := range h.subs {
		if s.runID != runID {
			continue
		}
		select {
		case s.ch <- ev:
		default:
			// Subscriber is full — drop. Either the client is slow or
			// has died; either way we don't want a stuck send.
		}
	}
}
