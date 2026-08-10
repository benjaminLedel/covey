package runner

import (
	"context"
	"errors"
	"sync"
)

// Transport carries the runner protocol. It is the whole difference between a
// built-in runner and one on another host — and it is the same seam
// internal/daemon draws between the control plane and the sandbox.
type Transport interface {
	Send(ctx context.Context, msg Message) error
	Receive(ctx context.Context) (Message, error)
	Close() error
}

// ErrTransportClosed: the other side is gone.
var ErrTransportClosed = errors.New("runner transport closed")

// InProc is the transport of the built-in runner: two channels instead of a
// WebSocket. Deliberately buffered — an unbuffered channel would make Send
// wait for the reader, and the two sides would then take turns instead of
// working.
type InProc struct {
	toRunner  chan Message
	toControl chan Message

	closeOnce sync.Once
	closed    chan struct{}
}

// NewInProc returns the two ends of one connection: the control plane's and
// the runner's. Closing either one ends both — there is one connection here,
// not two.
func NewInProc() (control Transport, node Transport) {
	p := &InProc{
		toRunner:  make(chan Message, 32),
		toControl: make(chan Message, 32),
		closed:    make(chan struct{}),
	}
	return &inProcEnd{p: p, out: p.toRunner, in: p.toControl},
		&inProcEnd{p: p, out: p.toControl, in: p.toRunner}
}

type inProcEnd struct {
	p   *InProc
	out chan Message
	in  chan Message
}

func (e *inProcEnd) Send(ctx context.Context, msg Message) error {
	select {
	case <-e.p.closed:
		return ErrTransportClosed
	default:
	}
	select {
	case e.out <- msg:
		return nil
	case <-e.p.closed:
		return ErrTransportClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *inProcEnd) Receive(ctx context.Context) (Message, error) {
	select {
	case msg := <-e.in:
		return msg, nil
	case <-e.p.closed:
		// Drain first: a message that has already been sent must still arrive,
		// otherwise a close would swallow the last answer of a run that
		// completed perfectly well.
		select {
		case msg := <-e.in:
			return msg, nil
		default:
		}
		return Message{}, ErrTransportClosed
	case <-ctx.Done():
		return Message{}, ctx.Err()
	}
}

func (e *inProcEnd) Close() error {
	e.p.closeOnce.Do(func() { close(e.p.closed) })
	return nil
}
