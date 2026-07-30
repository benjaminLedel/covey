package httpapi

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/reqlog"
	reqlogstore "covey/internal/reqlog/store"
)

// --- Request-Log: die HTTP-Requests an den Rändern der Plattform (spec/06) ---
//
// Zwei Quellen speisen es: die ausgehenden Requests der Zielsystem-Plugins
// (über reqlog.Transport, aus der Sandbox per Daemon-Event) und die
// eingehenden Webhooks/Trigger, die logIncoming hier mitschreibt. Gerade der
// eingehende Pfad ist beim Anbinden eines Systems der interessante: ein an der
// Signaturprüfung gescheiterter Teams-Webhook hinterließ bisher keine Spur.

// maxLoggedBody kappt, was von einem eingehenden Request/einer Antwort ins Log
// wandert — dieselbe Grenze wie beim ausgehenden Transport.
const maxLoggedBody = reqlog.MaxBody

// maxIncomingBody deckelt, wie viel von einem eingehenden Request überhaupt
// gepuffert wird — dieselbe Grenze wie im Webhook-Handler.
const maxIncomingBody = 4 << 20

// logIncoming umhüllt einen Handler und protokolliert Request und Antwort.
// system ist der Zielsystem-Name, sofern er aus dem Pfad ablesbar ist (leer =
// generischer Trigger). Ohne Request-Log ist es ein reiner Pass-through.
func (s *Server) logIncoming(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.ReqLog == nil {
			next(w, r)
			return
		}
		start := time.Now()

		// Body mitlesen und für den Handler wiederherstellen. Der Rest wird
		// mitgepuffert, aber gedeckelt: der Endpunkt ist unauthentifiziert,
		// hier darf nichts unbegrenzt in den Speicher laufen. maxIncomingBody
		// entspricht der Grenze, die die Handler selbst ziehen — was darüber
		// liegt, hätten sie ohnehin verworfen.
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(io.LimitReader(r.Body, maxLoggedBody+1))
			rest, _ := io.ReadAll(io.LimitReader(r.Body, maxIncomingBody))
			r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(append(append([]byte{}, body...), rest...)))
		}

		// Der Handler löst den Agenten erst auf — über den Context reicht er
		// ihn zurück, damit der Eintrag Org/Agent trägt.
		meta := &incomingMeta{}
		rec := &recordingWriter{ResponseWriter: w, status: http.StatusOK}
		next(rec, r.WithContext(withIncomingMeta(r.Context(), meta)))

		e := reqlog.Entry{
			CreatedAt:  start.UTC(),
			Direction:  reqlog.DirectionIn,
			System:     reqlog.SystemFromPath(r.URL.Path),
			Method:     r.Method,
			URL:        reqlog.RedactURL(r.URL),
			Status:     rec.status,
			DurationMS: time.Since(start).Milliseconds(),
			ReqBytes:   int64(len(body)),
			ReqBody:    reqlog.Redact(truncateBody(body)),
			RespBytes:  rec.n,
			RespBody:   reqlog.Redact(truncateBody(rec.buf.Bytes())),
			Remote:     remoteHost(r),
		}
		if rec.status >= 400 {
			e.Error = strings.TrimSpace(rec.buf.String())
			if len(e.Error) > 500 {
				e.Error = e.Error[:500]
			}
		}
		s.ReqLog.Enqueue(reqlogstore.Record{Entry: e, OrgID: meta.orgID, AgentID: meta.agentID})
	}
}

func truncateBody(b []byte) string {
	if len(b) <= maxLoggedBody {
		return string(b)
	}
	return string(b[:maxLoggedBody]) + "\n… (gekürzt)"
}

// remoteHost ist die Absender-Adresse ohne Port; hinter einem Reverse-Proxy
// gewinnt X-Forwarded-For (erster Eintrag).
func remoteHost(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		first, _, _ := strings.Cut(fwd, ",")
		return strings.TrimSpace(first)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// recordingWriter merkt sich Status und den Anfang der Antwort.
type recordingWriter struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
	n      int64
}

func (w *recordingWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *recordingWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.n += int64(n)
	if rest := maxLoggedBody - w.buf.Len(); rest > 0 && n > 0 {
		w.buf.Write(p[:min(n, rest)])
	}
	return n, err
}

// incomingMeta trägt den erst im Handler aufgelösten Agenten zurück zur
// Middleware.
type incomingMeta struct {
	orgID   *uuid.UUID
	agentID *uuid.UUID
}

const incomingMetaKey ctxKey = "reqlog-incoming"

func withIncomingMeta(ctx context.Context, m *incomingMeta) context.Context {
	return context.WithValue(ctx, incomingMetaKey, m)
}

// noteWebhookAgent hinterlegt den aufgelösten Agenten für den Log-Eintrag.
// No-op, wenn der Request nicht durch logIncoming läuft (Tests).
func noteWebhookAgent(r *http.Request, a agents.Agent) {
	m, _ := r.Context().Value(incomingMetaKey).(*incomingMeta)
	if m == nil {
		return
	}
	orgID, agentID := a.OrgID, a.ID
	m.orgID, m.agentID = &orgID, &agentID
}

// --- API: /api/v1/platform/requests ---

// handleListRequests — GET /api/v1/platform/requests: die jüngsten Requests,
// gefiltert. Ohne Bodies (die kommen im Detail), damit die Liste schlank bleibt.
func (s *Server) handleListRequests(w http.ResponseWriter, r *http.Request) {
	if s.ReqLog == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "entries": []any{}})
		return
	}
	p := principalFrom(r)
	q := r.URL.Query()
	f := reqlogstore.Filter{
		Direction: q.Get("direction"),
		System:    q.Get("system"),
		OnlyBad:   q.Get("only_errors") == "true",
		Query:     q.Get("q"),
	}
	if v := q.Get("agent_id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			f.AgentID = &id
		}
	}
	f.BeforeID, _ = strconv.ParseInt(q.Get("before"), 10, 64)
	f.Limit, _ = strconv.Atoi(q.Get("limit"))

	entries, err := s.ReqLog.List(r.Context(), p.OrgID, f)
	if err != nil {
		mapErr(w, err)
		return
	}
	systems, err := s.ReqLog.Systems(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":         true,
		"bodies":          s.ReqLog.BodiesEnabled(),
		"retention_hours": int(s.ReqLog.Retention().Hours()),
		"dropped":         s.ReqLog.Dropped(),
		"systems":         systems,
		"entries":         entries,
	})
}

// handleGetRequest — GET /api/v1/platform/requests/{id}: ein Eintrag samt
// (gekappten, redigierten) Bodies.
func (s *Server) handleGetRequest(w http.ResponseWriter, r *http.Request) {
	if s.ReqLog == nil {
		writeErr(w, http.StatusNotFound, "request-log ist abgeschaltet")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	entry, err := s.ReqLog.Get(r.Context(), principalFrom(r).OrgID, id)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

// handleClearRequests — DELETE /api/v1/platform/requests: Log leeren.
func (s *Server) handleClearRequests(w http.ResponseWriter, r *http.Request) {
	if s.ReqLog == nil {
		writeErr(w, http.StatusNotFound, "request-log ist abgeschaltet")
		return
	}
	n, err := s.ReqLog.Clear(r.Context(), principalFrom(r).OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}
