package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/coder/websocket"

	"covey/internal/daemon"
)

// wsLink implements orchestrator.DaemonLink over a WebSocket connection.
type wsLink struct {
	conn *websocket.Conn
	done chan struct{}
}

func (l *wsLink) Send(ctx context.Context, msg daemon.Message) error {
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return l.conn.Write(ctx, websocket.MessageText, raw)
}

func (l *wsLink) Receive(ctx context.Context) (daemon.Message, error) {
	_, raw, err := l.conn.Read(ctx)
	if err != nil {
		return daemon.Message{}, err
	}
	var msg daemon.Message
	return msg, json.Unmarshal(raw, &msg)
}

func (l *wsLink) Close() error {
	defer func() {
		defer func() { recover() }() // tolerate a double close
		close(l.done)
	}()
	return l.conn.Close(websocket.StatusNormalClosure, "sleep")
}

// handleDaemonWS authenticates the sandbox daemon via its short-lived JWT
// (aud=daemon) and hands the connection over to the waiting session.
func (s *Server) handleDaemonWS(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		writeErr(w, http.StatusUnauthorized, "daemon token missing")
		return
	}
	agentID, err := s.Identity.VerifyAgentToken(r.Context(), token, "daemon")
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "daemon token invalid")
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Sandboxes connect process/network-internally, not from the browser.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	conn.SetReadLimit(16 << 20)
	link := &wsLink{conn: conn, done: make(chan struct{})}

	if err := s.Orch.AttachDaemon(agentID, link); err != nil {
		s.Log.Warn("daemon connection rejected", "agent", agentID, "err", err)
		conn.Close(websocket.StatusPolicyViolation, "no waiting session")
		return
	}
	// The session owns the connection now; the handler only keeps the HTTP
	// handler scope open until the session closes it.
	select {
	case <-r.Context().Done():
	case <-link.done:
	}
}
