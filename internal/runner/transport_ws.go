package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
		if resp != nil {
			switch resp.StatusCode {
			case http.StatusUnauthorized:
				return nil, errors.New("the control plane refuses this runner token — " +
					"registered against another instance, or revoked there")
			case http.StatusUpgradeRequired:
				// 426 means the request arrived WITHOUT the upgrade headers.
				// The runner sent them — so something in between dropped them,
				// and that is a reverse proxy nine times out of ten. Saying
				// "expected status 101, got 426" is true and useless: it sends
				// somebody looking at the runner, and the runner is fine.
				return nil, fmt.Errorf("%s answered 426 to the WebSocket handshake — "+
					"a proxy in front of the instance is not forwarding the upgrade. "+
					"For nginx: proxy_http_version 1.1 plus `proxy_set_header Upgrade $http_upgrade` "+
					"and `proxy_set_header Connection $connection_upgrade` on this location "+
					"(see docs/en/operations/runner.md)", wsURL)
			}
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
	// This loop is the node's lifetime — it ends only when ctx does. Whatever
	// is still being watched then has no one left to report to, and its
	// `docker wait` would outlive the process that started it.
	defer node.Close()
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
