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

// --- Request log: the HTTP requests at the platform's edges (spec/06) ---
//
// Two sources feed it: the outgoing requests of the target-system plugins (via
// reqlog.Transport, from the sandbox as a daemon event) and the incoming
// webhooks/triggers that logIncoming records here. The incoming path in
// particular is the interesting one when wiring up a system: a Teams webhook
// that failed signature verification used to leave no trace at all.

// maxLoggedBody caps how much of an incoming request/response goes into the log
// — the same limit as for the outgoing transport.
const maxLoggedBody = reqlog.MaxBody

// maxIncomingBody caps how much of an incoming request is buffered at all —
// the same limit as in the webhook handler.
const maxIncomingBody = 4 << 20

// logIncoming wraps a handler and logs request and response. system is the
// target-system name, as far as it can be read from the path (empty = generic
// trigger). Without a request log it is a plain pass-through.
func (s *Server) logIncoming(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.ReqLog == nil {
			next(w, r)
			return
		}
		start := time.Now()

		// Read the body along the way and restore it for the handler. The rest
		// is buffered too, but capped: the endpoint is unauthenticated, nothing
		// may run into memory without a bound here. maxIncomingBody matches the
		// limit the handlers draw themselves — anything beyond it they would
		// have discarded anyway.
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(io.LimitReader(r.Body, maxLoggedBody+1))
			rest, _ := io.ReadAll(io.LimitReader(r.Body, maxIncomingBody))
			r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(append(append([]byte{}, body...), rest...)))
		}

		// The handler is the one that resolves the agent — it hands it back via
		// the context so that the entry carries org/agent.
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
	return string(b[:maxLoggedBody]) + "\n… (truncated)"
}

// remoteHost is the sender address without the port; behind a reverse proxy
// X-Forwarded-For wins (first entry).
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

// recordingWriter remembers the status and the beginning of the response.
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

// incomingMeta carries the agent — resolved only inside the handler — back to
// the middleware.
type incomingMeta struct {
	orgID   *uuid.UUID
	agentID *uuid.UUID
}

const incomingMetaKey ctxKey = "reqlog-incoming"

func withIncomingMeta(ctx context.Context, m *incomingMeta) context.Context {
	return context.WithValue(ctx, incomingMetaKey, m)
}

// noteWebhookAgent records the resolved agent for the log entry. No-op if the
// request does not pass through logIncoming (tests).
func noteWebhookAgent(r *http.Request, a agents.Agent) {
	m, _ := r.Context().Value(incomingMetaKey).(*incomingMeta)
	if m == nil {
		return
	}
	orgID, agentID := a.OrgID, a.ID
	m.orgID, m.agentID = &orgID, &agentID
}

// --- API: /api/v1/platform/requests ---

// handleListRequests — GET /api/v1/platform/requests: the most recent requests,
// filtered. Without bodies (those come with the detail view) so the list stays
// slim.
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

// handleGetRequest — GET /api/v1/platform/requests/{id}: one entry including
// its (truncated, redacted) bodies.
func (s *Server) handleGetRequest(w http.ResponseWriter, r *http.Request) {
	if s.ReqLog == nil {
		writeErr(w, http.StatusNotFound, "request log is switched off")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	entry, err := s.ReqLog.Get(r.Context(), principalFrom(r).OrgID, id)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

// handleClearRequests — DELETE /api/v1/platform/requests: empty the log.
func (s *Server) handleClearRequests(w http.ResponseWriter, r *http.Request) {
	if s.ReqLog == nil {
		writeErr(w, http.StatusNotFound, "request log is switched off")
		return
	}
	n, err := s.ReqLog.Clear(r.Context(), principalFrom(r).OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}
