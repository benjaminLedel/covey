package orchestrator

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/daemon"
)

// fakeLink is a minimal DaemonLink for the warmLink tests: Receive delivers
// from a channel, Close marks the link as closed.
type fakeLink struct {
	in     chan daemon.Message
	err    chan error
	closed atomic.Bool
	sent   atomic.Int32
}

func newFakeLink() *fakeLink {
	return &fakeLink{in: make(chan daemon.Message, 8), err: make(chan error, 1)}
}

func (f *fakeLink) Send(_ context.Context, _ daemon.Message) error {
	f.sent.Add(1)
	return nil
}

func (f *fakeLink) Receive(ctx context.Context) (daemon.Message, error) {
	select {
	case msg := <-f.in:
		return msg, nil
	case err := <-f.err:
		return daemon.Message{}, err
	case <-ctx.Done():
		return daemon.Message{}, ctx.Err()
	}
}

func (f *fakeLink) Close() error {
	f.closed.Store(true)
	return nil
}

func TestWarmLinkForwardsAndSurvivesCancel(t *testing.T) {
	inner := newFakeLink()
	wl := newWarmLink(inner)

	// A message flows through.
	inner.in <- daemon.Message{Type: daemon.TypeHeartbeat}
	msg, err := wl.Receive(context.Background())
	if err != nil || msg.Type != daemon.TypeHeartbeat {
		t.Fatalf("Receive = %v, %v", msg.Type, err)
	}

	// A Receive with an expired context returns the ctx error — and must NOT
	// close the inner link (that is the whole point of the pump wrapper).
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := wl.Receive(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, was %v", err)
	}
	if inner.closed.Load() {
		t.Fatal("the inner link must NOT be closed by a Receive cancel")
	}

	// After the cancel the link is still usable.
	inner.in <- daemon.Message{Type: daemon.TypeSleep}
	if msg, err := wl.Receive(context.Background()); err != nil || msg.Type != daemon.TypeSleep {
		t.Fatalf("link unusable after cancel: %v, %v", msg.Type, err)
	}

	// Send delegates to the inner link.
	_ = wl.Send(context.Background(), daemon.Message{Type: daemon.TypeKill})
	if inner.sent.Load() != 1 {
		t.Fatalf("Send was not passed through: %d", inner.sent.Load())
	}

	// Close closes the inner link.
	_ = wl.Close()
	if !inner.closed.Load() {
		t.Fatal("Close has to close the inner link")
	}
}

func TestWarmLinkPropagatesInnerError(t *testing.T) {
	inner := newFakeLink()
	wl := newWarmLink(inner)
	inner.err <- errors.New("connection lost")
	if _, err := wl.Receive(context.Background()); err == nil {
		t.Fatal("an error from the inner link has to propagate")
	}
}

// fakeSandbox remembers whether it was torn down.
type fakeSandbox struct{ stopped atomic.Bool }

func (f *fakeSandbox) Stop(context.Context) error {
	f.stopped.Store(true)
	return nil
}

// A warm sandbox is a RUNNING container belonging to a sleeping agent. When
// the control plane shuts down it has to tear it down — otherwise it is left
// behind: on the next start only the next wake of the same agent clears it
// away (the provider deletes the container of the same name), and for an agent
// that is never woken again it keeps running indefinitely.
func uuidNil() uuid.UUID { return uuid.New() }

func TestWarmSandboxesAreTornDownOnShutdown(t *testing.T) {
	o := New(Options{})
	link, sandbox := newFakeLink(), &fakeSandbox{}

	ctx, cancel := context.WithCancel(context.Background())
	go o.warmReaperLoop(ctx)
	o.parkWarm(uuidNil(), link, sandbox)

	o.mu.Lock()
	parked := len(o.warm)
	o.mu.Unlock()
	if parked != 1 {
		t.Fatalf("expected one parked sandbox, got %d", parked)
	}

	cancel() // the control plane shuts down

	deadline := time.After(3 * time.Second)
	for {
		if sandbox.stopped.Load() && link.closed.Load() {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("warm sandbox was not torn down (stopped=%v, link closed=%v)",
				sandbox.stopped.Load(), link.closed.Load())
		case <-time.After(10 * time.Millisecond):
		}
	}
	o.mu.Lock()
	rest := len(o.warm)
	o.mu.Unlock()
	if rest != 0 {
		t.Errorf("%d sandboxes are still parked after shutdown", rest)
	}
}

// syncingSandbox is a fakeSandbox that also keeps its home in a store.
type syncingSandbox struct {
	fakeSandbox
	syncs atomic.Int32
	err   error
}

func (s *syncingSandbox) SyncHome(context.Context) error {
	s.syncs.Add(1)
	return s.err
}

// waitFor polls a condition — the sync runs in its own goroutine, so the test
// may not read the counter straight away.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for !cond() {
		select {
		case <-deadline:
			t.Fatalf("timeout: %s", what)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// The point of the whole exercise: a warm agent's job goes into the store
// without its sandbox being stopped. Before this, the work of a run lived in
// one container volume until something tore the sandbox down — and every way
// of losing the container in between lost the run with it.
func TestParkedWarmHomeIsSynced(t *testing.T) {
	o := New(Options{})
	link, sandbox := newFakeLink(), &syncingSandbox{}
	id := uuid.New()

	ws := o.parkWarm(id, link, sandbox)
	o.syncParkedHome(id, sandbox, ws)

	waitFor(t, "the parked home was not synced", func() bool { return sandbox.syncs.Load() == 1 })
	if sandbox.stopped.Load() {
		t.Error("the sandbox was stopped — a warm one has to keep running")
	}
}

// An agent on a short heartbeat must not scan its home every time it nods off.
func TestParkedWarmHomeSyncIsThrottled(t *testing.T) {
	o := New(Options{})
	id := uuid.New()
	sandbox := &syncingSandbox{}

	ws := o.parkWarm(id, newFakeLink(), sandbox)
	o.syncParkedHome(id, sandbox, ws)
	waitFor(t, "first sync missing", func() bool { return sandbox.syncs.Load() == 1 })

	o.syncParkedHome(id, sandbox, ws) // immediately afterwards: too soon
	time.Sleep(20 * time.Millisecond)
	if got := sandbox.syncs.Load(); got != 1 {
		t.Errorf("expected one sync inside the interval, got %d", got)
	}

	// Beyond the interval it goes again.
	o.mu.Lock()
	o.lastWarmSync[id] = time.Now().Add(-warmSyncEvery - time.Second)
	o.mu.Unlock()
	o.syncParkedHome(id, sandbox, ws)
	waitFor(t, "second sync missing after the interval", func() bool { return sandbox.syncs.Load() == 2 })
}

// A failed sync must not block the next attempt for the whole interval: the
// home is precisely NOT in the store then.
func TestFailedParkedSyncReleasesTheInterval(t *testing.T) {
	o := New(Options{})
	id := uuid.New()
	sandbox := &syncingSandbox{err: errors.New("store unreachable")}

	ws := o.parkWarm(id, newFakeLink(), sandbox)
	o.syncParkedHome(id, sandbox, ws)
	waitFor(t, "sync was not attempted", func() bool { return sandbox.syncs.Load() == 1 })
	waitFor(t, "the interval was not released after the failure", func() bool {
		o.mu.Lock()
		defer o.mu.Unlock()
		_, noted := o.lastWarmSync[id]
		return !noted
	})
}

// A session that is no longer the parked one belongs to the next run —
// scanning its home from here would describe a state nobody asked about.
func TestTakenOverSessionIsNotSynced(t *testing.T) {
	o := New(Options{})
	id := uuid.New()
	sandbox := &syncingSandbox{}

	ws := o.parkWarm(id, newFakeLink(), sandbox)
	if o.takeWarm(id) == nil { // the next wake takes the session over
		t.Fatal("takeWarm returned nothing")
	}
	o.syncParkedHome(id, sandbox, ws)

	time.Sleep(50 * time.Millisecond)
	if got := sandbox.syncs.Load(); got != 0 {
		t.Errorf("a session that is no longer parked was synced anyway (%d)", got)
	}
}

// A provider without a store (the mock) must not make the sleep path fail.
func TestSandboxWithoutStoreIsNoError(t *testing.T) {
	o := New(Options{})
	id := uuid.New()
	sandbox := &fakeSandbox{}
	o.syncParkedHome(id, sandbox, o.parkWarm(id, newFakeLink(), sandbox))
}

// discardingSandbox kann beides und schreibt mit, was verlangt wurde.
type discardingSandbox struct {
	syncingSandbox
	discards atomic.Int32
}

func (d *discardingSandbox) Discard(context.Context) error {
	d.discards.Add(1)
	d.stopped.Store(true)
	return nil
}

// Ein Start, der nie ein Lauf wurde — Container sofort tot, oder der Daemon
// meldet sich nie —, darf sein Home nicht zurückschreiben. Es ist Byte für Byte
// das, was Minuten zuvor hineinmaterialisiert wurde; der Sync ist ein voller
// Scan für ein identisches Ergebnis. Auf einer Produktivinstanz gemessen: elf
// Minuten Home hinein, Container unter einer Sekunde tot, dann eine halbe
// Stunde Scannen, bevor das Scheitern überhaupt aufgezeichnet war.
func TestEinToterStartSchreibtDasHomeNichtZurueck(t *testing.T) {
	sandbox := &discardingSandbox{}
	discard(context.Background(), sandbox)

	if got := sandbox.discards.Load(); got != 1 {
		t.Errorf("erwartet ein Discard, bekommen %d", got)
	}
	if got := sandbox.syncs.Load(); got != 0 {
		t.Errorf("das Home wurde trotzdem synchronisiert (%d)", got)
	}
	if !sandbox.stopped.Load() {
		t.Error("die Rechenleistung muss trotzdem runter")
	}
}

// Ein Provider, der den Unterschied nicht kennt, bekommt den gewöhnlichen
// Stopp — verloren ist dann nur die Zeit, nicht die Arbeit.
func TestOhneDiscardBleibtEsBeimStopp(t *testing.T) {
	sandbox := &fakeSandbox{}
	discard(context.Background(), sandbox)
	if !sandbox.stopped.Load() {
		t.Error("ohne Discard muss Stop greifen")
	}
}
