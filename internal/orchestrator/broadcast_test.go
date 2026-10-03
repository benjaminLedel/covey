package orchestrator

import (
	"testing"

	"github.com/google/uuid"
)

// A subscriber that names its types receives only those; one that names none
// receives everything of its organisation (#535).
func TestBroadcasterFiltersByType(t *testing.T) {
	org := uuid.New()
	b := NewBroadcaster()
	narrow, cancelNarrow := b.Subscribe(org, "chat", "agent_status")
	defer cancelNarrow()
	wide, cancelWide := b.Subscribe(org)
	defer cancelWide()

	b.Publish(Event{Type: "recording", OrgID: org})
	b.Publish(Event{Type: "chat", OrgID: org})
	b.Publish(Event{Type: "agent_status", OrgID: org})
	b.Publish(Event{Type: "chat", OrgID: uuid.New()}) // another organisation

	if got := drain(narrow); len(got) != 2 || got[0] != "chat" || got[1] != "agent_status" {
		t.Fatalf("narrow subscriber got %v, want [chat agent_status]", got)
	}
	if got := drain(wide); len(got) != 3 {
		t.Fatalf("wide subscriber got %v, want all three of its organisation", got)
	}
}

// Empty type names are not a filter: Subscribe(org, "") reads everything,
// so a request with a blank parameter does not silence the client.
func TestBroadcasterBlankTypesMeanAll(t *testing.T) {
	org := uuid.New()
	b := NewBroadcaster()
	ch, cancel := b.Subscribe(org, "")
	defer cancel()
	b.Publish(Event{Type: "recording", OrgID: org})
	if got := drain(ch); len(got) != 1 {
		t.Fatalf("got %v, want the recording event", got)
	}
}

// The recording flood of a run does not crowd out the one event a narrow
// subscriber waits for: filtered events never take a slot in its channel.
func TestBroadcasterFloodDoesNotDropFilteredSubscriber(t *testing.T) {
	org := uuid.New()
	b := NewBroadcaster()
	ch, cancel := b.Subscribe(org, "agent_status")
	defer cancel()
	for i := 0; i < subscriberBuffer*4; i++ {
		b.Publish(Event{Type: "recording", OrgID: org})
	}
	b.Publish(Event{Type: "agent_status", OrgID: org})
	if got := drain(ch); len(got) != 1 || got[0] != "agent_status" {
		t.Fatalf("got %v, want exactly the agent_status event", got)
	}
}

func drain(ch chan Event) []string {
	var out []string
	for {
		select {
		case e := <-ch:
			out = append(out, e.Type)
		default:
			return out
		}
	}
}
