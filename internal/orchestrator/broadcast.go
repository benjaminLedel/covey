package orchestrator

import "sync"

// Event ist ein Live-Update für die Admin-UI (SSE).
type Event struct {
	Type    string `json:"type"` // agent_status | task | recording | approval | guardrail
	AgentID string `json:"agent_id,omitempty"`
	Data    any    `json:"data,omitempty"`
}

// Broadcaster ist ein simpler Fan-out für Live-Events. Langsame Abonnenten
// verlieren Events (non-blocking send) — die UI lädt ohnehin per Query nach.
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
