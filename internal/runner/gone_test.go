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

/* Two faults, one incident (#96). An agent finished at 14:32:21; in the same
   moment the runner began writing its home into the store — and carried no
   sandbox any more. The planned update saw the gap, replaced the binary and
   restarted the runner into the running sync. The snapshot never moved. And
   the control plane waited afterwards until 15:02 for an answer that no
   one could send any more. */

// registriereFalschenRunner builds a connection by hand: the control plane at
// one end, a silent counterpart at the other. Silent is the point — it is
// about what happens when no answer comes.
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

// warteAufTyp pulls messages until the wanted one is among them.
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

// An open question whose connection breaks is answered at once — with the
// fact that there is nothing left to wait for. Before, it ran into its own
// timeout: thirty minutes on a home-sync, during which the UI said it was
// securing the workplace and nothing happened.
func TestEineOffeneFrageStirbtMitIhrerVerbindung(t *testing.T) {
	p := NewPool(quietLog())
	orgID := uuid.New()
	nodeEnd, runnerID, _ := registriereFalschenRunner(t, p, orgID)

	p.mu.Lock()
	c := p.conns[runnerID]
	p.mu.Unlock()

	antwort := make(chan error, 1)
	go func() {
		// Thirty minutes — the deadline of a home-sync.
		_, err := c.ask(context.Background(), TypeSyncHome,
			SyncHome{AgentID: uuid.New(), OrgID: orgID}, 30*time.Minute)
		antwort <- err
	}()
	// First fetch what was sent: that a waiter is registered does not yet mean
	// the question was on the wire — and a question that never went out fails
	// right away anyway. The case from #96 is the other one: it went out, and
	// no one came back with an answer. Aimed at THIS question: on the wire lie
	// also heartbeat and the capacity question, and fetching one of those says
	// nothing about the sync.
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

// "Idle" is not "carries no sandbox". As long as the control plane waits for
// an answer, the host is doing something — and writing a home is the most
// valuable thing it does.
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

	// A sync is under way …
	go func() {
		_, _ = c.ask(context.Background(), TypeSyncHome,
			SyncHome{AgentID: uuid.New(), OrgID: orgID}, time.Minute)
	}()
	warteBis(t, 3*time.Second, func() bool { return c.pending() > 0 })

	// … and the host reports zero sandboxes while it runs. Exactly the case from #96.
	ctx := context.Background()
	warteAufTyp(t, nodeEnd, TypeSyncHome)
	go antworteAufKapazitaet(nodeEnd, CapacityReport{Sandboxes: 0, FreeBytes: 1 << 30})
	c.refreshCapacity(ctx)

	if gefragt != 0 {
		t.Fatalf("das eingeplante Update fragte %d mal nach, obwohl eine Frage offen war", gefragt)
	}
}

// The counter-test, and it is the more important half: a guard that never
// lets anything through has not fixed the fault but switched off the function.
// A host that carries nothing and has nothing to answer for is idle — and
// then the planned update runs too.
func TestEinWirklichLeerlaufenderHostBekommtSeinUpdate(t *testing.T) {
	p := NewPool(quietLog())
	orgID := uuid.New()
	gefragt := make(chan struct{}, 1)
	p.PlannedUpdate = func(ctx context.Context, runnerID uuid.UUID) (string, error) {
		select {
		case gefragt <- struct{}{}:
		default:
		}
		return "", nil // no plan on file — it was asked anyway
	}
	nodeEnd, runnerID, _ := registriereFalschenRunner(t, p, orgID)
	p.mu.Lock()
	c := p.conns[runnerID]
	p.mu.Unlock()

	ctx := context.Background()
	// Answer throughout, not once: the pool asks for capacity on its own, and
	// one who answers only the first question may answer the wrong one — the
	// guard's, not the test's.
	go antworteAufKapazitaet(nodeEnd, CapacityReport{Sandboxes: 0, FreeBytes: 1 << 30})
	c.refreshCapacity(ctx)

	select {
	case <-gefragt:
	case <-time.After(2 * time.Second):
		t.Fatal("der leerlaufende Host wurde nie nach seinem Plan gefragt")
	}
}

// antworteAufKapazitaet plays the host that answers every capacity question
// until the connection ends. Without t.Fatalf: from a goroutine that is not
// allowed, and a missing answer can only show up as the waiter running into
// its own deadline.
func antworteAufKapazitaet(end Transport, bericht CapacityReport) {
	ctx := context.Background()
	for {
		msg, err := end.Receive(ctx)
		if err != nil {
			return
		}
		if msg.Type != TypeCapacity {
			continue
		}
		antwort, err := encode(TypeCapacity, msg.ID, bericht)
		if err != nil {
			return
		}
		if err := end.Send(ctx, antwort); err != nil {
			return
		}
	}
}

// And the side that really knows: the host itself refuses as long as it is
// writing to a working copy. The control plane only sees sandboxes; the
// runner sees its queue.
func TestDerHostLehntEinUpdateWaehrendEinesSyncsAb(t *testing.T) {
	dir := t.TempDir()
	runnerID, orgID, agentID := uuid.New(), uuid.New(), uuid.New()
	node := NewNode(runnerID, orgID, &Docker{RunnerID: runnerID, DataDir: dir}, quietLog())
	t.Cleanup(node.Close)

	// A working-copy task in the queue that never finishes.
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
	// And it has touched nothing: no binary, no restart.
	if res.Restarting {
		t.Fatal("der Host startet trotz Ablehnung neu")
	}
	if _, err := os.Stat(filepath.Join(dir, "covey-runner.new")); err == nil {
		t.Fatal("es wurde trotz Ablehnung heruntergeladen")
	}
}
