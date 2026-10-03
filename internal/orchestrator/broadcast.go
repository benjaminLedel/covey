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
// events (non-blocking send) — the UI reloads via query anyway, and a client
// that was cut off asks again when its stream reopens (#536).
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
	subs map[chan Event]subscription
}

// subscription is what one subscriber asked for: its organisation, and the
// event types it reads — nil for all of them.
type subscription struct {
	orgID uuid.UUID
	types map[string]bool
}

// subscriberBuffer is how many events a subscriber may fall behind before
// Publish drops for it. A run publishes one `recording` event per step, so a
// client that reads everything sees bursts of dozens in seconds; 64 was
// filled by them while the one `agent_status` that mattered was the event
// dropped (#535). The drop stays non-blocking — a client that never reads
// must not hold the orchestrator — but it has to be the outlier.
const subscriberBuffer = 256

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subs: map[chan Event]subscription{}}
}

// Subscribe opens a channel for one organisation. The zero UUID receives
// nothing — an account without a membership has no fleet to watch.
//
// types narrows the subscription to those event types; none means all. The
// filter sits here and not at the reader for the same reason the organisation
// does: an event that has already been copied into the channel has taken the
// slot, whether or not anyone wanted it. The app reads chat, task,
// agent_status and approval and never recording — without the filter the
// recording flood of one run filled its buffer and cost it the events it
// came for (#535).
func (b *Broadcaster) Subscribe(orgID uuid.UUID, types ...string) (ch chan Event, cancel func()) {
	ch = make(chan Event, subscriberBuffer)
	sub := subscription{orgID: orgID}
	if len(types) > 0 {
		sub.types = make(map[string]bool, len(types))
		for _, t := range types {
			if t != "" {
				sub.types[t] = true
			}
		}
		if len(sub.types) == 0 {
			sub.types = nil
		}
	}
	b.mu.Lock()
	b.subs[ch] = sub
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
	for ch, sub := range b.subs {
		// Fail closed: an event without an organisation reaches nobody. That
		// way a publish site that forgets to set it goes quiet instead of
		// broadcasting to everyone.
		if e.OrgID == uuid.Nil || sub.orgID != e.OrgID {
			continue
		}
		if sub.types != nil && !sub.types[e.Type] {
			continue
		}
		select {
		case ch <- e:
		default:
		}
	}
}
