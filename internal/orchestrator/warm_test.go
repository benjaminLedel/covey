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
