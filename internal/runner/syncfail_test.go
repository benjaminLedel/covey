package runner

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

/* A home sync that failed left a line in the runner's debug log and
   nothing else. The UI kept showing the last successful snapshot — true
   and useless: on a production instance it stayed that way for weeks, and it
   cost a 39-minute run (#72). */

func TestEinGescheiterterSyncWirdGemeldet(t *testing.T) {
	p := NewPool(quietLog())
	orgID, agentID := uuid.New(), uuid.New()
	p.SnapshotTaken = func(context.Context, uuid.UUID, uuid.UUID, HomeSynced) error { return nil }

	gemeldet := make(chan string, 2)
	p.SnapshotFailed = func(_ context.Context, _, _ uuid.UUID, reason, msg string) {
		gemeldet <- reason + ": " + msg
	}

	nodeEnd, runnerID, _ := registriereFalschenRunner(t, p, orgID)
	p.mu.Lock()
	c := p.conns[runnerID]
	p.mu.Unlock()

	// The host answers with a failure — exactly the form that the
	// runner sends on a 413 from the reverse proxy.
	// Wait for the sync specifically: heartbeat and the capacity question lie
	// on the line too, and whoever takes the first message that arrives answers
	// the wrong one — the host then counts as silent and is dropped after 90
	// seconds, which yields a quite different failure.
	go func() {
		ctx, abbrechen := context.WithTimeout(context.Background(), 20*time.Second)
		defer abbrechen()
		for {
			msg, err := nodeEnd.Receive(ctx)
			if err != nil {
				return
			}
			if msg.Type != TypeSyncHome {
				continue
			}
			antwort, err := encode(TypeHomeSynced, msg.ID, HomeSynced{
				AgentID: agentID, Err: "block 78a279df: 413 Request Entity Too Large",
			})
			if err == nil {
				_ = nodeEnd.Send(ctx, antwort)
			}
			return
		}
	}()

	if err := p.syncHomeReason(context.Background(), c, agentID, orgID, "job"); err == nil {
		t.Fatal("der Fehlschlag kam nicht beim Aufrufer an")
	}
	select {
	case got := <-gemeldet:
		if got != "job: block 78a279df: 413 Request Entity Too Large" {
			t.Fatalf("gemeldet wurde %q — Grund und Wortlaut des Hosts gehören beide hinein", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nichts gemeldet — genau die Stille aus #72")
	}
}

// The other way into the failure as well: the host vanishes mid-sync.
// Without a report all one sees afterwards is a snapshot that stayed put.
func TestAuchEinVerschwundenerHostWirdGemeldet(t *testing.T) {
	p := NewPool(quietLog())
	orgID, agentID := uuid.New(), uuid.New()
	p.SnapshotTaken = func(context.Context, uuid.UUID, uuid.UUID, HomeSynced) error { return nil }
	gemeldet := make(chan string, 2)
	p.SnapshotFailed = func(_ context.Context, _, _ uuid.UUID, reason, msg string) {
		gemeldet <- msg
	}

	nodeEnd, runnerID, _ := registriereFalschenRunner(t, p, orgID)
	p.mu.Lock()
	c := p.conns[runnerID]
	p.mu.Unlock()

	go func() {
		warteAufTypOhneTest(nodeEnd, TypeSyncHome)
		_ = nodeEnd.Close()
	}()

	if err := p.syncHomeReason(context.Background(), c, agentID, orgID, "job"); err == nil {
		t.Fatal("der Abbruch kam nicht beim Aufrufer an")
	}
	select {
	case got := <-gemeldet:
		if got == "" {
			t.Fatal("gemeldet wurde ein leerer Grund")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nichts gemeldet")
	}
}

// warteAufTypOhneTest is warteAufTyp for a goroutine: t.Fatalf is
// not allowed there, and the message can only stay out in a way that lets the
// waiting test run into its own deadline.
func warteAufTypOhneTest(end Transport, typ string) {
	ctx, abbrechen := context.WithTimeout(context.Background(), 10*time.Second)
	defer abbrechen()
	for {
		msg, err := end.Receive(ctx)
		if err != nil {
			return
		}
		if msg.Type == typ {
			return
		}
	}
}
