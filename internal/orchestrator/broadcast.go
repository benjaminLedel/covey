package orchestrator

import "sync"

// Event is a live update for the admin UI (SSE).
type Event struct {
	Type    string `json:"type"` // agent_status | task | recording | approval | guardrail
	AgentID string `json:"agent_id,omitempty"`
	Data    any    `json:"data,omitempty"`
}

// Broadcaster is a simple fan-out for live events. Slow subscribers lose
// events (non-blocking send) — the UI reloads via query anyway.
type Broadcaster struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subs: map[chan Event]struct{}{}}
}

func (b *Broadcaster) Subscribe() (ch chan Event, cancel func()) {
	ch = make(chan Event, 64)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}
}

func (b *Broadcaster) Publish(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default:
		}
	}
}
