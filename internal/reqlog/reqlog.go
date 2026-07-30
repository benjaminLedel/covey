// Package reqlog protokolliert die HTTP-Requests an den Rändern der Plattform:
// ausgehende Aufrufe der Zielsystem-Plugins (Teams-Connector, Zammad-API, …)
// und eingehende Webhooks. Es ist die Diagnose-Ebene unter dem Recording — das
// Recording sagt, welche Aktion ein Agent ausführte, das Request-Log sagt, was
// dabei über die Leitung ging.
//
// Der Kern ist bewusst frei von DB- und Control-Plane-Abhängigkeiten: Plugins
// laufen im Sandbox-Daemon, ihre Requests entstehen also außerhalb der Control
// Plane. Ein Plugin baut seinen http.Client mit Client()/Transport() und weiß
// nichts über den Verbleib; wohin die Einträge fließen, entscheidet die Senke:
//
//   - im Daemon: eine Context-Senke, die der Action-Proxy pro Aktion setzt und
//     die den Eintrag über das Daemon-Protokoll an die Control Plane schickt.
//   - in der Control Plane: die Default-Senke (SetDefault), die in den Store
//     schreibt — für Requests ohne Agenten-Kontext (Work-Checks, JWKS-Abruf).
//
// Ohne Senke ist der Transport ein reiner Pass-through: kein Puffer, keine
// Kosten. Damit ist das Log abschaltbar, ohne dass Plugin-Code sich ändert.
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

// Richtungen eines protokollierten Requests.
const (
	DirectionIn  = "in"  // Covey hat den Request empfangen (Webhook, Trigger)
	DirectionOut = "out" // Covey hat den Request gestellt (Zielsystem-API)
)

// MaxBody ist die Obergrenze je gespeichertem Body-Ausschnitt. Requests wie ein
// Datei-Download würden das Log sonst sprengen; für die Diagnose reicht der
// Anfang (Fehlerantworten sind kurz, JSON-Header stehen vorn).
const MaxBody = 8 << 10

// Entry ist ein protokollierter Request. Er reist als JSON auch über das
// Daemon-Protokoll — die Feldnamen sind deshalb Teil der Naht.
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
	// Remote ist bei eingehenden Requests der Absender (IP), sonst leer.
	Remote string `json:"remote,omitempty"`
	// TaskID setzt die Daemon-Senke, damit ein Request der Aufgabe zuordenbar
	// bleibt, in deren Aktion er entstand.
	TaskID string `json:"task_id,omitempty"`
}

// Sink nimmt fertige Einträge entgegen. Implementierungen müssen nicht
// blockieren dürfen — der Aufruf hängt am Request-Pfad.
type Sink func(Entry)

type ctxKey struct{}

// WithSink hängt eine Senke an den Context. Sie gewinnt gegen die Default-Senke
// — so landet ein Request, der zu einer Aufgabe gehört, mit deren Kontext im Log.
func WithSink(ctx context.Context, sink Sink) context.Context {
	return context.WithValue(ctx, ctxKey{}, sink)
}

var (
	defaultMu   sync.RWMutex
	defaultSink Sink
)

// SetDefault setzt die prozessweite Senke (Control Plane). nil schaltet das
// Protokollieren ab, sofern kein Context-Sink greift.
func SetDefault(sink Sink) {
	defaultMu.Lock()
	defaultSink = sink
	defaultMu.Unlock()
}

// SinkFrom liefert die zuständige Senke: Context vor Default, nil = aus.
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

// Emit schreibt einen fertigen Eintrag in die zuständige Senke (No-op ohne
// Senke). Für Aufrufer, die keinen http.Client benutzen — etwa der eingehende
// Webhook-Pfad.
func Emit(ctx context.Context, e Entry) {
	if sink := SinkFrom(ctx); sink != nil {
		if e.CreatedAt.IsZero() {
			e.CreatedAt = time.Now().UTC()
		}
		sink(e)
	}
}

// Client baut einen http.Client, dessen Requests ins Log gehen. Zielsystem-
// Plugins bauen ihre Clients damit — sie erben Protokollierung, ohne sie zu
// kennen.
func Client(system string, timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: Transport(system, nil)}
}

// Transport umhüllt einen RoundTripper (nil = http.DefaultTransport) und
// protokolliert jeden Request unter dem angegebenen Zielsystem-Namen.
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
		return t.roundTripper().RoundTrip(req) // aus = kein Overhead
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
	// Die Antwort ist erst vollständig, wenn der Aufrufer sie gelesen bzw.
	// geschlossen hat — bis dahin fehlen Dauer und Body. Der Eintrag entsteht
	// deshalb im Body-Wrapper, genau einmal.
	resp.Body = &captureBody{rc: resp.Body, start: start, entry: e, sink: sink}
	return resp, nil
}

// snapshotRequestBody liest eine Kopie des Request-Bodys über GetBody — den
// setzt http.NewRequest für die üblichen Body-Typen (bytes/strings.Reader).
// Ohne GetBody (Streaming-Body) wird nichts gelesen: den Body zu konsumieren
// würde den Request zerstören.
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

// captureBody reicht die Antwort durch, merkt sich den Anfang und schreibt den
// Eintrag, sobald der Body zu Ende gelesen oder geschlossen wird. sync.Once
// verhindert Doppel-Einträge bei Close nach EOF.
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
	return string(b[:MaxBody]) + "\n… (gekürzt)"
}

// --- Redaktion ---
//
// Ins Log gehören Requests, keine Zugangsdaten. Zwei Stellen tragen Secrets:
// Query-Parameter (?access_token=…) und JSON-/Form-Felder im Body
// (client_secret, password, access_token). Header werden gar nicht erst
// gespeichert — dort steckt der Bearer.

var secretParam = regexp.MustCompile(`(?i)(token|secret|password|passwd|key|signature|sig|code|auth)`)

// RedactURL macht aus einer URL die Log-Fassung: Pfad bleibt, verdächtige
// Query-Werte werden ersetzt.
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

// secretField trifft "feld":"wert" (JSON) und feld=wert (Form) für Feldnamen,
// die nach Zugangsdaten aussehen.
var (
	secretJSON = regexp.MustCompile(`(?i)"([a-z0-9_\-]*(token|secret|password|passwd|credential|api[_\-]?key)[a-z0-9_\-]*)"\s*:\s*"[^"]*"`)
	secretForm = regexp.MustCompile(`(?i)\b([a-z0-9_\-]*(token|secret|password|passwd|credential|api[_\-]?key)[a-z0-9_\-]*)=[^&\s]*`)
)

// Redact ersetzt Zugangsdaten in einem Body-Ausschnitt.
func Redact(body string) string {
	if body == "" {
		return ""
	}
	body = secretJSON.ReplaceAllString(body, `"$1":"***"`)
	body = secretForm.ReplaceAllString(body, `$1=***`)
	return body
}

// SystemFromPath liest den Zielsystem-Namen aus einem Webhook-Pfad
// (/api/webhooks/<system>/<slug>); leer, wenn der Pfad anders aussieht.
func SystemFromPath(path string) string {
	rest, ok := strings.CutPrefix(path, "/api/webhooks/")
	if !ok {
		return ""
	}
	system, _, _ := strings.Cut(rest, "/")
	return system
}
