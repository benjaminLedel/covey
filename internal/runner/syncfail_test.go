package runner

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

/* Ein Home-Sync, der scheitert, hinterließ eine Zeile im Debug-Log des Runners
   und sonst nichts. Die Oberfläche zeigte weiter den letzten geglückten
   Schnappschuss — wahr und nutzlos: auf einer produktiven Instanz war das
   wochenlang so, und es kostete einen 39-Minuten-Lauf (#72). */

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

	// Der Host antwortet mit einem Fehlschlag — genau die Form, die der
	// Runner bei einem 413 des Reverse-Proxy schickt.
	// Gezielt auf den Sync warten: auf der Leitung liegen auch Herzschlag und
	// Kapazitätsfrage, und wer die erstbeste Nachricht nimmt, beantwortet die
	// falsche — der Host gilt dann als still und wird nach 90 Sekunden
	// abgehängt, was einen ganz anderen Fehlschlag ergibt.
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

// Auch der andere Weg in den Fehlschlag: der Host verschwindet mitten im Sync.
// Ohne Meldung sähe man hinterher nur einen Schnappschuss, der stehen blieb.
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

// warteAufTypOhneTest ist warteAufTyp für eine Nebenläufigkeit: t.Fatalf ist
// dort nicht erlaubt, und ausbleiben kann die Nachricht nur so, dass der
// wartende Test in seine eigene Frist läuft.
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
