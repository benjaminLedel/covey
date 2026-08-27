package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

/* Zwei Fehler, ein Vorfall (#96). Ein Agent war um 14:32:21 fertig; im selben
   Moment beginnt der Runner, sein Home in den Store zu schreiben — und trägt
   dabei keine Sandbox mehr. Das eingeplante Update sah die Lücke, ersetzte das
   Binary und startete den Runner in den laufenden Sync hinein. Der
   Schnappschuss bewegte sich nie. Und die Kontrollebene wartete danach bis
   15:02 auf eine Antwort, die niemand mehr senden konnte. */

// registriereFalschenRunner baut eine Verbindung von Hand: die Steuerebene an
// dem einen Ende, ein stummes Gegenüber am anderen. Stumm ist der Punkt — es
// geht darum, was passiert, wenn keine Antwort kommt.
func registriereFalschenRunner(t *testing.T, p *Pool, orgID uuid.UUID) (Transport, uuid.UUID, chan error) {
	t.Helper()
	control, nodeEnd := NewInProc()
	runnerID := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	reg, err := encode(TypeRegistered, "", Registered{
		RunnerID: runnerID, OrgID: orgID, Protocol: Protocol, Version: "v0.0.0-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := nodeEnd.Send(ctx, reg); err != nil {
		t.Fatal(err)
	}
	fertig := make(chan error, 1)
	go func() { fertig <- p.Attach(ctx, control, false) }()

	warteBis(t, 3*time.Second, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.conns[runnerID] != nil
	})
	return nodeEnd, runnerID, fertig
}

// warteAufTyp holt Nachrichten ab, bis die gesuchte dabei ist.
func warteAufTyp(t *testing.T, end Transport, typ string) Message {
	t.Helper()
	frist, abbrechen := context.WithTimeout(context.Background(), 5*time.Second)
	defer abbrechen()
	for {
		msg, err := end.Receive(frist)
		if err != nil {
			t.Fatalf("keine Nachricht vom Typ %q: %v", typ, err)
		}
		if msg.Type == typ {
			return msg
		}
	}
}

func warteBis(t *testing.T, frist time.Duration, ok func() bool) {
	t.Helper()
	ende := time.Now().Add(frist)
	for time.Now().Before(ende) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("die Bedingung trat nicht ein")
}

// Eine offene Frage, deren Verbindung abreißt, wird sofort beantwortet — mit
// der Tatsache, dass es nichts mehr zu warten gibt. Vorher lief sie in ihren
// eigenen Zeitablauf: bei einem Home-Sync dreißig Minuten, in denen die
// Oberfläche „sichert Arbeitsplatz" sagte und nichts geschah.
func TestEineOffeneFrageStirbtMitIhrerVerbindung(t *testing.T) {
	p := NewPool(quietLog())
	orgID := uuid.New()
	nodeEnd, runnerID, _ := registriereFalschenRunner(t, p, orgID)

	p.mu.Lock()
	c := p.conns[runnerID]
	p.mu.Unlock()

	antwort := make(chan error, 1)
	go func() {
		// Dreißig Minuten — die Frist eines Home-Syncs.
		_, err := c.ask(context.Background(), TypeSyncHome,
			SyncHome{AgentID: uuid.New(), OrgID: orgID}, 30*time.Minute)
		antwort <- err
	}()
	// Erst abholen, was gesendet wurde: dass ein Wartender eingetragen ist,
	// heißt noch nicht, dass die Frage auf der Leitung war — und eine Frage,
	// die nie hinausging, scheitert ohnehin sofort. Der Fall aus #96 ist der
	// andere: sie ging hinaus, und niemand kam mit einer Antwort zurück.
	// Gezielt auf DIESE Frage: auf der Leitung liegen auch Herzschlag und
	// Kapazitätsfrage, und eine davon abzuholen sagt nichts über den Sync.
	warteAufTyp(t, nodeEnd, TypeSyncHome)

	_ = nodeEnd.Close()

	select {
	case err := <-antwort:
		if !errors.Is(err, ErrRunnerGone) {
			t.Fatalf("die Frage endete mit %v, erwartet ErrRunnerGone", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("die Frage wartet weiter — genau das sind die dreißig Minuten aus #96")
	}
}

// „Leerlaufend" ist nicht „trägt keine Sandbox". Solange die Steuerebene auf
// eine Antwort wartet, tut der Host etwas — und ein Home zu schreiben ist das
// Wertvollste, was er tut.
func TestEinHostMitOffenerFrageGiltNichtAlsLeerlaufend(t *testing.T) {
	p := NewPool(quietLog())
	orgID := uuid.New()
	var gefragt int
	p.PlannedUpdate = func(ctx context.Context, runnerID uuid.UUID) (string, error) {
		gefragt++
		return "", nil
	}
	nodeEnd, runnerID, _ := registriereFalschenRunner(t, p, orgID)
	p.mu.Lock()
	c := p.conns[runnerID]
	p.mu.Unlock()

	// Ein Sync ist unterwegs …
	go func() {
		_, _ = c.ask(context.Background(), TypeSyncHome,
			SyncHome{AgentID: uuid.New(), OrgID: orgID}, time.Minute)
	}()
	warteBis(t, 3*time.Second, func() bool { return c.pending() > 0 })

	// … und der Host meldet dabei null Sandboxen. Genau die Lage aus #96.
	ctx := context.Background()
	warteAufTyp(t, nodeEnd, TypeSyncHome)
	// Ohne t.Fatalf: aus einer Nebenläufigkeit heraus ist das nicht erlaubt,
	// und ausbleiben kann die Antwort ohnehin nur so, dass refreshCapacity
	// unten in seine eigene Frist läuft.
	go func() {
		for {
			msg, err := nodeEnd.Receive(ctx)
			if err != nil {
				return
			}
			if msg.Type != TypeCapacity {
				continue
			}
			antwort, err := encode(TypeCapacity, msg.ID, CapacityReport{Sandboxes: 0, FreeBytes: 1 << 30})
			if err != nil {
				return
			}
			_ = nodeEnd.Send(ctx, antwort)
			return
		}
	}()
	c.refreshCapacity(ctx)

	if gefragt != 0 {
		t.Fatalf("das eingeplante Update fragte %d mal nach, obwohl eine Frage offen war", gefragt)
	}
}

// Und die Seite, die es wirklich weiß: der Host selbst lehnt ab, solange er an
// einer Arbeitskopie schreibt. Die Steuerebene sieht nur Sandboxen; der Runner
// sieht seine Warteschlange.
func TestDerHostLehntEinUpdateWaehrendEinesSyncsAb(t *testing.T) {
	dir := t.TempDir()
	runnerID, orgID, agentID := uuid.New(), uuid.New(), uuid.New()
	node := NewNode(runnerID, orgID, &Docker{RunnerID: runnerID, DataDir: dir}, quietLog())
	t.Cleanup(node.Close)

	// Eine Arbeitskopie-Aufgabe in der Warteschlange, die nicht fertig wird.
	laeuft := make(chan struct{})
	node.inOrder(agentID, func() { <-laeuft })
	defer close(laeuft)
	warteBis(t, 3*time.Second, func() bool {
		node.mu.Lock()
		defer node.mu.Unlock()
		return len(node.turn) > 0
	})

	res := node.updateSelf(context.Background(), Update{Version: "v9.9.9"})
	if !res.Busy {
		t.Fatalf("der Host ließ sich mitten im Schreiben ersetzen: %+v", res)
	}
	if res.Err == "" {
		t.Fatal("die Ablehnung nennt keinen Grund — sie steht in der Oberfläche")
	}
	// Und er hat nichts angefasst: kein Binary, kein Neustart.
	if res.Restarting {
		t.Fatal("der Host startet trotz Ablehnung neu")
	}
	if _, err := os.Stat(filepath.Join(dir, "covey-runner.new")); err == nil {
		t.Fatal("es wurde trotz Ablehnung heruntergeladen")
	}
}
