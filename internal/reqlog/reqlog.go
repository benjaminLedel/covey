// Package reqlog logs the HTTP requests at the platform's edges: outgoing calls
// of the target-system plugins (Teams connector, Zammad API, …) and incoming
// webhooks. It is the diagnostic layer below the recording — the recording says
// which action an agent performed, the request log says what went over the wire
// while doing so.
//
// The core is deliberately free of DB and control-plane dependencies: plugins
// run in the sandbox daemon, so their requests originate outside the control
// plane. A plugin builds its http.Client with Client()/Transport() and knows
// nothing about where things end up; where the entries flow is decided by the
// sink:
//
//   - in the daemon: a context sink that the action proxy sets per action and
//     that sends the entry to the control plane over the daemon protocol.
//   - in the control plane: the default sink (SetDefault) writing into the
//     store — for requests without agent context (work checks, JWKS fetch).
//
// Without a sink the transport is a plain pass-through: no buffer, no cost.
// That makes the log switchable off without any change to plugin code.
package reqlog

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"context"
)

// Directions of a logged request.
const (
	DirectionIn  = "in"  // Covey received the request (webhook, trigger)
	DirectionOut = "out" // Covey issued the request (target-system API)
)

// MaxBody is the upper bound per stored body excerpt. Requests such as a file
// download would blow up the log otherwise; for diagnosis the beginning is
// enough (error responses are short, JSON headers come first).
const MaxBody = 8 << 10

// Entry is a logged request. It also travels as JSON over the daemon protocol —
// the field names are therefore part of the seam.
type Entry struct {
	CreatedAt  time.Time `json:"created_at"`
	Direction  string    `json:"direction"`
	System     string    `json:"system,omitempty"`
	Method     string    `json:"method"`
	URL        string    `json:"url"`
	Status     int       `json:"status,omitempty"`
	DurationMS int64     `json:"duration_ms"`
	ReqBytes   int64     `json:"req_bytes,omitempty"`
	RespBytes  int64     `json:"resp_bytes,omitempty"`
	ReqBody    string    `json:"req_body,omitempty"`
	RespBody   string    `json:"resp_body,omitempty"`
	Error      string    `json:"error,omitempty"`
	// Remote is the sender (IP) on incoming requests, empty otherwise.
	Remote string `json:"remote,omitempty"`
	// TaskID is set by the daemon sink so that a request stays attributable to
	// the task in whose action it originated.
	TaskID string `json:"task_id,omitempty"`
}

// Sink accepts finished entries. Implementations must not be allowed to block —
// the call hangs off the request path.
type Sink func(Entry)

type ctxKey struct{}

// WithSink attaches a sink to the context. It wins over the default sink — that
// way a request belonging to a task ends up in the log with that task's context.
func WithSink(ctx context.Context, sink Sink) context.Context {
	return context.WithValue(ctx, ctxKey{}, sink)
}

var (
	defaultMu   sync.RWMutex
	defaultSink Sink
)

// SetDefault sets the process-wide sink (control plane). nil switches logging
// off unless a context sink applies.
func SetDefault(sink Sink) {
	defaultMu.Lock()
	defaultSink = sink
	defaultMu.Unlock()
}

// SinkFrom returns the responsible sink: context before default, nil = off.
func SinkFrom(ctx context.Context) Sink {
	if ctx != nil {
		if s, ok := ctx.Value(ctxKey{}).(Sink); ok && s != nil {
			return s
		}
	}
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultSink
}

// Emit writes a finished entry into the responsible sink (no-op without a
// sink). For callers that do not use an http.Client — the incoming webhook path,
// for instance.
func Emit(ctx context.Context, e Entry) {
	if sink := SinkFrom(ctx); sink != nil {
		if e.CreatedAt.IsZero() {
			e.CreatedAt = time.Now().UTC()
		}
		sink(e)
	}
}

// Client builds an http.Client whose requests go into the log. Target-system
// plugins build their clients with it — they inherit logging without knowing
// about it.
func Client(system string, timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: Transport(system, nil)}
}

// Transport wraps a RoundTripper (nil = http.DefaultTransport) and logs every
// request under the given target-system name.
func Transport(system string, base http.RoundTripper) http.RoundTripper {
	return &transport{system: system, base: base}
}

type transport struct {
	system string
	base   http.RoundTripper
}

func (t *transport) roundTripper() http.RoundTripper {
	if t.base != nil {
		return t.base
	}
	return http.DefaultTransport
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	sink := SinkFrom(req.Context())
	if sink == nil {
		return t.roundTripper().RoundTrip(req) // off = no overhead
	}
	start := time.Now()
	e := Entry{
		CreatedAt: start.UTC(),
		Direction: DirectionOut,
		System:    t.system,
		Method:    req.Method,
		URL:       RedactURL(req.URL),
	}
	e.ReqBody, e.ReqBytes = snapshotRequestBody(req)

	resp, err := t.roundTripper().RoundTrip(req)
	if err != nil {
		e.DurationMS = millis(start)
		e.Error = err.Error()
		sink(e)
		return resp, err
	}
	e.Status = resp.StatusCode
	// The response is only complete once the caller has read or closed it —
	// until then duration and body are missing. The entry is therefore created
	// in the body wrapper, exactly once.
	resp.Body = &captureBody{rc: resp.Body, start: start, entry: e, sink: sink}
	return resp, nil
}

// snapshotRequestBody reads a copy of the request body via GetBody — which
// http.NewRequest sets for the usual body types (bytes/strings.Reader). Without
// GetBody (streaming body) nothing is read: consuming the body would destroy
// the request.
func snapshotRequestBody(req *http.Request) (string, int64) {
	if req.Body == nil || req.GetBody == nil {
		return "", req.ContentLength
	}
	rc, err := req.GetBody()
	if err != nil || rc == nil {
		return "", req.ContentLength
	}
	defer rc.Close()
	buf, _ := io.ReadAll(io.LimitReader(rc, MaxBody+1))
	n := req.ContentLength
	if n < 0 {
		n = int64(len(buf))
	}
	return Redact(truncate(buf)), n
}

// captureBody passes the response through, remembers its beginning and writes
// the entry as soon as the body has been read to the end or closed. sync.Once
// prevents duplicate entries on Close after EOF.
type captureBody struct {
	rc    io.ReadCloser
	start time.Time
	entry Entry
	sink  Sink
	buf   bytes.Buffer
	n     int64
	once  sync.Once
}

func (c *captureBody) Read(p []byte) (int, error) {
	n, err := c.rc.Read(p)
	if n > 0 {
		c.n += int64(n)
		if rest := MaxBody - c.buf.Len(); rest > 0 {
			c.buf.Write(p[:min(n, rest)])
		}
	}
	if err != nil {
		if err != io.EOF {
			c.entry.Error = err.Error()
		}
		c.emit()
	}
	return n, err
}

func (c *captureBody) Close() error {
	err := c.rc.Close()
	c.emit()
	return err
}

func (c *captureBody) emit() {
	c.once.Do(func() {
		c.entry.DurationMS = millis(c.start)
		c.entry.RespBytes = c.n
		c.entry.RespBody = Redact(truncate(c.buf.Bytes()))
		c.sink(c.entry)
	})
}

func millis(start time.Time) int64 { return time.Since(start).Milliseconds() }

func truncate(b []byte) string {
	if len(b) <= MaxBody {
		return string(b)
	}
	return string(b[:MaxBody]) + "\n… (truncated)"
}

// --- Redaction ---
//
// The log is for requests, not for credentials. Two places carry secrets: query
// parameters (?access_token=…) and JSON/form fields in the body (client_secret,
// password, access_token). Headers are not stored at all — that is where the
// bearer sits.

var secretParam = regexp.MustCompile(`(?i)(token|secret|password|passwd|key|signature|sig|code|auth)`)

// RedactURL turns a URL into its log version: the path stays, suspicious query
// values are replaced.
func RedactURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	if u.RawQuery == "" && u.User == nil {
		return u.String()
	}
	clone := *u
	clone.User = nil
	q := clone.Query()
	for k := range q {
		if secretParam.MatchString(k) {
			q.Set(k, "***")
		}
	}
	clone.RawQuery = q.Encode()
	return clone.String()
}

// secretField matches "field":"value" (JSON) and field=value (form) for field
// names that look like credentials.
var (
	secretJSON = regexp.MustCompile(`(?i)"([a-z0-9_\-]*(token|secret|password|passwd|credential|api[_\-]?key)[a-z0-9_\-]*)"\s*:\s*"[^"]*"`)
	secretForm = regexp.MustCompile(`(?i)\b([a-z0-9_\-]*(token|secret|password|passwd|credential|api[_\-]?key)[a-z0-9_\-]*)=[^&\s]*`)
)

// Redact replaces credentials in a body excerpt.
func Redact(body string) string {
	if body == "" {
		return ""
	}
	body = secretJSON.ReplaceAllString(body, `"$1":"***"`)
	body = secretForm.ReplaceAllString(body, `$1=***`)
	return body
}

// SystemFromPath reads the target-system name from a webhook path
// (/api/webhooks/<system>/<slug>); empty when the path looks different.
func SystemFromPath(path string) string {
	rest, ok := strings.CutPrefix(path, "/api/webhooks/")
	if !ok {
		return ""
	}
	system, _, _ := strings.Cut(rest, "/")
	return system
}
