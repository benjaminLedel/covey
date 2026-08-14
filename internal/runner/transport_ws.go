package runner

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// wsTransport carries the runner protocol over a WebSocket — the second of the
// two transports, and the only difference between a runner in this process and
// one on another machine.
type wsTransport struct {
	conn *websocket.Conn
}

// NewWSTransport wraps an established connection.
func NewWSTransport(conn *websocket.Conn) Transport {
	// The messages are small (a start_sandbox with its environment); the
	// payload — gigabytes of blocks — deliberately goes past this channel.
	conn.SetReadLimit(1 << 20)
	return &wsTransport{conn: conn}
}

func (w *wsTransport) Send(ctx context.Context, msg Message) error {
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if err := w.conn.Write(ctx, websocket.MessageText, raw); err != nil {
		return wrapClosed(err)
	}
	return nil
}

func (w *wsTransport) Receive(ctx context.Context) (Message, error) {
	_, raw, err := w.conn.Read(ctx)
	if err != nil {
		return Message{}, wrapClosed(err)
	}
	var msg Message
	return msg, json.Unmarshal(raw, &msg)
}

func (w *wsTransport) Close() error {
	return w.conn.Close(websocket.StatusNormalClosure, "bye")
}

// wrapClosed turns "the other side is gone" into the one error the protocol
// loops recognise. Without it a normal disconnect would be logged as a fault,
// and a maintenance window would read like an incident.
func wrapClosed(err error) error {
	if err == nil {
		return nil
	}
	var closeErr websocket.CloseError
	if errors.As(err, &closeErr) || errors.Is(err, net.ErrClosed) {
		return ErrTransportClosed
	}
	return err
}

// Dial builds the runner's connection to the control plane. The direction is
// what makes remote execution practical in the first place: the runner dials
// out, and the control plane only waits — a runner needs no inbound
// reachability, only a way out (spec/16).
func Dial(ctx context.Context, controlURL, token string) (Transport, error) {
	wsURL := strings.TrimRight(controlURL, "/") + "/api/runner/ws"
	switch {
	case strings.HasPrefix(wsURL, "https://"):
		wsURL = "wss://" + strings.TrimPrefix(wsURL, "https://")
	case strings.HasPrefix(wsURL, "http://"):
		wsURL = "ws://" + strings.TrimPrefix(wsURL, "http://")
	}
	conn, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + token}},
	})
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return nil, errors.New("the control plane refuses this runner token — " +
				"registered against another instance, or revoked there")
		}
		return nil, err
	}
	return NewWSTransport(conn), nil
}

// RunNode connects a node and speaks the protocol until the connection ends —
// the runner's whole main loop. It reconnects, because a control plane that is
// briefly away must not cost the host its runner: whoever installed it as a
// service expects it to come back by itself.
func RunNode(ctx context.Context, node *Node, controlURL, token string, backoff time.Duration) error {
	if backoff <= 0 {
		backoff = 5 * time.Second
	}
	for {
		t, err := Dial(ctx, controlURL, token)
		if err == nil {
			err = node.Run(ctx, t)
			_ = t.Close()
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			node.Log.Warn("connection to the control plane ended", "err", err, "retry_in", backoff)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
}
