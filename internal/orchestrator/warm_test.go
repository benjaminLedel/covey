package orchestrator

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"covey/internal/daemon"
)

// fakeLink ist ein minimaler DaemonLink für die warmLink-Tests: Receive liefert
// aus einem Channel, Close markiert den Link als geschlossen.
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

	// Nachricht fließt durch.
	inner.in <- daemon.Message{Type: daemon.TypeHeartbeat}
	msg, err := wl.Receive(context.Background())
	if err != nil || msg.Type != daemon.TypeHeartbeat {
		t.Fatalf("Receive = %v, %v", msg.Type, err)
	}

	// Ein Receive mit abgelaufenem Context liefert den ctx-Fehler — und darf den
	// inneren Link NICHT schließen (das ist der ganze Zweck der Pump-Hülle).
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := wl.Receive(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("erwartet DeadlineExceeded, war %v", err)
	}
	if inner.closed.Load() {
		t.Fatal("innerer Link darf durch Receive-Cancel NICHT geschlossen werden")
	}

	// Nach dem Cancel ist der Link weiter benutzbar.
	inner.in <- daemon.Message{Type: daemon.TypeSleep}
	if msg, err := wl.Receive(context.Background()); err != nil || msg.Type != daemon.TypeSleep {
		t.Fatalf("Link nach Cancel unbrauchbar: %v, %v", msg.Type, err)
	}

	// Send delegiert an den inneren Link.
	_ = wl.Send(context.Background(), daemon.Message{Type: daemon.TypeKill})
	if inner.sent.Load() != 1 {
		t.Fatalf("Send nicht durchgereicht: %d", inner.sent.Load())
	}

	// Close schließt den inneren Link.
	_ = wl.Close()
	if !inner.closed.Load() {
		t.Fatal("Close muss den inneren Link schließen")
	}
}

func TestWarmLinkPropagatesInnerError(t *testing.T) {
	inner := newFakeLink()
	wl := newWarmLink(inner)
	inner.err <- errors.New("verbindung verloren")
	if _, err := wl.Receive(context.Background()); err == nil {
		t.Fatal("Fehler des inneren Links muss durchschlagen")
	}
}
