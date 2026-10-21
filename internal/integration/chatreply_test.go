package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/backlog"
)

// TestAConversationHearsTheReplyNotTheSummary is #650: a run writes its reply
// to the person above the status line and a short form into it; the
// conversation got the short form — "overview delivered" — and never the
// overview. For a task from a conversation the reply is what is said, on a
// parked task together with its question unless the reply already asks it.
func TestAConversationHearsTheReplyNotTheSummary(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("antwort-650")
	admin := teamLogin(t, s)
	base := "/api/v1/agents/" + agent.ID.String()

	run := func(text, state string) backlog.Task {
		t.Helper()
		created := admin.expect(http.MethodPost, base+"/messages", map[string]any{"text": text}, http.StatusCreated)
		id, _ := uuid.Parse(created["task"].(map[string]any)["id"].(string))
		waitFor(t, "the run ends", 40*time.Second, func() bool { return s.taskState(id) == state })
		got, err := s.backlog.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	// Without a reply — an older daemon — the status line's fields stand.
	// (First: the mock reads directives out of the history a chat task
	// carries, and a reply further up would be taken for this run's.)
	ohne := run("Kurz\n[mock:result Erledigt.]", backlog.StateDone)
	if ohne.Result == nil || *ohne.Result != "Erledigt." {
		t.Fatalf("without a reply the result field stands: %q", *ohne.Result)
	}

	// Done: the reply is the result the conversation gets.
	fertig := run("Übersicht bitte\n[mock:reply Hier die Übersicht: Login 4, Elternbriefe 7.]\n[mock:result Übersicht geliefert]", backlog.StateDone)
	if fertig.Result == nil || *fertig.Result != "Hier die Übersicht: Login 4, Elternbriefe 7." {
		t.Fatalf("the conversation task's result has to be its reply: %q", *fertig.Result)
	}

	// Parked, the reply already asking the question: said once.
	frage := "Welche der beiden soll ich entwerfen?"
	run("Was könnte mir helfen?\n[mock:reply Ich sehe zwei Kolleginnen: 1. Analystin, 2. Schreiberin. "+frage+"]\n[mock:block key=wahl question="+frage+"]", backlog.StateBlocked)
	s.srv.Nacherzaehlen(ctx)
	var gestellt string
	if err := s.pool.QueryRow(ctx, `SELECT text FROM conversation_messages WHERE kind = 'question'
		ORDER BY created_at DESC LIMIT 1`).Scan(&gestellt); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(gestellt, "Ich sehe zwei Kolleginnen") || strings.Count(gestellt, frage) != 1 {
		t.Fatalf("the question message is the reply, the question in it once: %q", gestellt)
	}

}
