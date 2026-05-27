// Run-event pubsub.
//
// v1 was in-process: only the api replica whose StatusConsumer received
// the original NATS message could broadcast, which broke once we ran
// more than one api Pod. v1.5 backs the Hub with NATS core (non-
// JetStream) so the fan-out is cluster-wide:
//
//   replica A: StatusConsumer (JetStream queue group) — exactly one
//              replica wins per run.* message and writes DB + usage.
//              After the write, that replica also re-broadcasts to
//              "run.events.<runID>" on plain NATS.
//   every replica: subscribed to "run.events.>" on plain NATS. On
//              receive, looks up local SSE channels matching the runId
//              and delivers; otherwise drops silently.
//
// Plain NATS (no JetStream) is the right transport here: misses are
// acceptable (the SSE client either reconnects or hits the synthetic
// terminal fallback the handler emits at subscribe time), and we do
// NOT want at-least-once redelivery on best-effort fan-out.

package runs

import (
	"encoding/json"
	"log"
	"strings"
	"sync"

	"github.com/shantanubansal/AiLab/internal/eventbus"
	"github.com/shantanubansal/AiLab/pkg/events"
)

// RunEvent is one lifecycle notification carried over the hub.
type RunEvent struct {
	Kind      string                `json:"kind"` // "started" | "completed"
	Started   *events.RunStarted    `json:"started,omitempty"`
	Completed *events.RunCompleted  `json:"completed,omitempty"`
}

const runEventsPrefix = "run.events."

// Hub fans out RunEvents to per-subscriber channels across replicas via
// plain NATS. Closing the returned cancel func unregisters the local
// channel; the NATS-side wildcard subscription is set up once per Hub
// at construction and lives for the process lifetime.
type Hub struct {
	bus  *eventbus.Bus
	mu   sync.Mutex
	next int
	subs map[int]subscription
}

type subscription struct {
	runID string
	ch    chan RunEvent
}

// NewHub builds a Hub. Pass bus = nil for a single-process in-memory
// mode (handy in tests); pass a real bus for cross-process fan-out.
// The wildcard NATS subscribe is best-effort — failures are logged but
// don't error out so a transient NATS blip doesn't crash the api.
func NewHub(bus *eventbus.Bus) *Hub {
	h := &Hub{bus: bus, subs: make(map[int]subscription)}
	if bus != nil {
		if _, err := bus.SubscribeRaw(runEventsPrefix+">", h.deliverFromBus); err != nil {
			log.Printf("runs.Hub: subscribe %s>: %v", runEventsPrefix, err)
		}
	}
	return h
}

// Subscribe registers a local listener for events from a single run.
// The returned channel is buffered (32) so a brief receiver stall
// doesn't drop events; once the buffer fills, further events for this
// subscriber drop silently. Caller must invoke cancel when done.
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

// Broadcast publishes ev to NATS so every api replica's wildcard
// subscriber can deliver to its local SSE clients. Falls back to
// local-only delivery when bus is nil (single-process / test mode).
func (h *Hub) Broadcast(runID string, ev RunEvent) {
	if h.bus == nil {
		h.deliverLocal(runID, ev)
		return
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return
	}
	if err := h.bus.PublishRaw(runEventsPrefix+runID, payload); err != nil {
		log.Printf("runs.Hub: publish %s%s: %v", runEventsPrefix, runID, err)
	}
}

// deliverFromBus is the wildcard NATS handler. Subject is
// "run.events.<runID>"; we re-derive runID from it so the payload JSON
// stays free of redundant fields.
func (h *Hub) deliverFromBus(subject string, data []byte) {
	runID := strings.TrimPrefix(subject, runEventsPrefix)
	var ev RunEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return
	}
	h.deliverLocal(runID, ev)
}

func (h *Hub) deliverLocal(runID string, ev RunEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, s := range h.subs {
		if s.runID != runID {
			continue
		}
		select {
		case s.ch <- ev:
		default:
			// Subscriber buffer full — drop. SSE handler reads
			// continuously; this only fires if the client is dead.
		}
	}
}
