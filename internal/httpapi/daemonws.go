package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/coder/websocket"

	"covey/internal/daemon"
)

// wsLink implementiert orchestrator.DaemonLink über eine WebSocket-Verbindung.
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
		defer func() { recover() }() // doppelt schließen tolerieren
		close(l.done)
	}()
	return l.conn.Close(websocket.StatusNormalClosure, "sleep")
}

// handleDaemonWS authentifiziert den Sandbox-Daemon über sein kurzlebiges
// JWT (aud=daemon) und übergibt die Verbindung an die wartende Session.
func (s *Server) handleDaemonWS(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		writeErr(w, http.StatusUnauthorized, "daemon-token fehlt")
		return
	}
	agentID, err := s.Identity.VerifyAgentToken(r.Context(), token, "daemon")
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "daemon-token ungültig")
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Sandboxen verbinden sich prozess-/netzintern, nicht aus dem Browser.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	conn.SetReadLimit(16 << 20)
	link := &wsLink{conn: conn, done: make(chan struct{})}

	if err := s.Orch.AttachDaemon(agentID, link); err != nil {
		s.Log.Warn("daemon-verbindung abgelehnt", "agent", agentID, "err", err)
		conn.Close(websocket.StatusPolicyViolation, "keine wartende session")
		return
	}
	// Die Session besitzt die Verbindung jetzt; der Handler hält nur den
	// HTTP-Handler-Scope offen, bis die Session sie schließt.
	select {
	case <-r.Context().Done():
	case <-link.done:
	}
}
