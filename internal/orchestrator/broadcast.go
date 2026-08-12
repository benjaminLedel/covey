package orchestrator

import (
	"sync"

	"github.com/google/uuid"
)

// Event is a live update for the admin UI (SSE).
//
// OrgID does not go to the browser (`json:"-"`) — it is not information for
// the recipient but the criterion for WHETHER they are one. Whoever puts it in
// the payload has already sent it.
type Event struct {
	Type    string    `json:"type"` // agent_status | task | recording | approval | guardrail
	AgentID string    `json:"agent_id,omitempty"`
	OrgID   uuid.UUID `json:"-"`
	Data    any       `json:"data,omitempty"`
}

// Broadcaster is a simple fan-out for live events. Slow subscribers lose
// events (non-blocking send) — the UI reloads via query anyway.
//
// Every subscription belongs to ONE organisation and receives only its events.
// Before that, the bus knew no tenants: every signed-in human of every
// organisation watched every other one's fleet work — agent and task ids,
// statuses, action names, guard-rail decisions, live and without an id to
// guess (FR-003, finding A).
//
// The filter sits HERE and not in the SSE handler: a fan-out that has already
// copied the event into a foreign channel has lost the argument, even if
// nobody reads it afterwards.
type Broadcaster struct {
	mu   sync.Mutex
	subs map[chan Event]uuid.UUID
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subs: map[chan Event]uuid.UUID{}}
}

// Subscribe opens a channel for one organisation. The zero UUID receives
// nothing — an account without a membership has no fleet to watch.
func (b *Broadcaster) Subscribe(orgID uuid.UUID) (ch chan Event, cancel func()) {
	ch = make(chan Event, 64)
	b.mu.Lock()
	b.subs[ch] = orgID
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
	for ch, orgID := range b.subs {
		// Fail closed: an event without an organisation reaches nobody. That
		// way a publish site that forgets to set it goes quiet instead of
		// broadcasting to everyone.
		if e.OrgID == uuid.Nil || orgID != e.OrgID {
			continue
		}
		select {
		case ch <- e:
		default:
		}
	}
}
