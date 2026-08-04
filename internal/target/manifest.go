package target

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"covey/internal/reqlog"
)

// Manifest is a declarative target system plugin: a JSON file an admin uploads
// instead of compiling Go code. A generic REST engine (ManifestSystem)
// interprets it — that way API-key-based REST systems can be connected without
// shipping a new Covey build. For anything beyond that (OAuth flows, special
// protocols) the way remains a compiled built-in plugin.
type Manifest struct {
	Name        string `json:"name"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	// Category places the plugin in the target system store (see Category…
	// in target.go). Empty = "other".
	Category string `json:"category,omitempty"`

	// Auth describes how the brokered token ends up in requests.
	Auth ManifestAuth `json:"auth"`

	// Webhook maps incoming payloads onto the wake event. Field references
	// are dotted paths into the JSON ("ticket.id", "article.body").
	Webhook ManifestWebhook `json:"webhook"`

	// Actions are the actions the agent may call through the action proxy.
	// Key = action name.
	Actions map[string]ManifestAction `json:"actions"`

	// PromptDoc describes the actions for the agent's system prompt.
	PromptDoc string `json:"prompt_doc,omitempty"`
}

type ManifestAuth struct {
	// Header the token is written into (default "Authorization").
	Header string `json:"header,omitempty"`
	// Format of the header value; {token} is substituted (default "Bearer {token}").
	Format string `json:"format,omitempty"`
}

type ManifestWebhook struct {
	// Signature: "hmac-sha1" | "hmac-sha256" | "" (no verification).
	Signature string `json:"signature,omitempty"`
	// SignatureHeader carries the signature (default "X-Hub-Signature").
	SignatureHeader string `json:"signature_header,omitempty"`

	// IDField identifies the business object (correlation key).
	IDField string `json:"id_field"`
	// EventIDField identifies the concrete event (dedup, e.g. article id).
	EventIDField string `json:"event_id_field,omitempty"`
	// TitleField/BodyField fill the backlog task.
	TitleField string `json:"title_field,omitempty"`
	BodyField  string `json:"body_field,omitempty"`
	// IgnoreWhen: does NOT wake if field == value (e.g. the agent's own echo).
	IgnoreWhen []ManifestCondition `json:"ignore_when,omitempty"`
}

type ManifestCondition struct {
	Field  string `json:"field"`
	Equals string `json:"equals"`
}

type ManifestAction struct {
	Method string `json:"method"`
	// Path relative to the base URL; {param} placeholders are substituted from
	// the JSON params, the remaining params go out as the JSON body.
	Path string `json:"path"`
	// Subject overrides the guard-rail subject (default system:action).
	// SubjectWhen allows param-dependent subjects (e.g. internal=false).
	Subject     string                `json:"subject,omitempty"`
	SubjectWhen []ManifestSubjectRule `json:"subject_when,omitempty"`
}

type ManifestSubjectRule struct {
	Param   string          `json:"param"`
	Equals  json.RawMessage `json:"equals"`
	Subject string          `json:"subject"`
}

var manifestNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,31}$`)

// ParseManifest validates an uploaded plugin file fail-closed: better a clear
// error at upload time than silent misbehavior at runtime.
func ParseManifest(raw []byte) (Manifest, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return m, fmt.Errorf("manifest: %w", err)
	}
	if !manifestNameRe.MatchString(m.Name) {
		return m, fmt.Errorf("manifest: name must match %s", manifestNameRe)
	}
	if len(m.Actions) == 0 {
		return m, fmt.Errorf("manifest: at least one action is required")
	}
	for name, a := range m.Actions {
		switch a.Method {
		case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			return m, fmt.Errorf("manifest: action %q: method %q not allowed", name, a.Method)
		}
		if !strings.HasPrefix(a.Path, "/") {
			return m, fmt.Errorf("manifest: action %q: path must start with /", name)
		}
	}
	switch m.Webhook.Signature {
	case "", "hmac-sha1", "hmac-sha256":
	default:
		return m, fmt.Errorf("manifest: webhook.signature %q unknown (hmac-sha1|hmac-sha256)", m.Webhook.Signature)
	}
	if m.Webhook.IDField == "" {
		return m, fmt.Errorf("manifest: webhook.id_field missing")
	}
	return m, nil
}

// ManifestSystem interprets a manifest as a target.System — the same interface
// as a compiled plugin, all enforcement points (guard-rails, broker, recording)
// apply identically.
type ManifestSystem struct {
	M    Manifest
	HTTP *http.Client
}

func NewManifestSystem(m Manifest) *ManifestSystem {
	return &ManifestSystem{M: m, HTTP: reqlog.Client(m.Name, 15*time.Second)}
}

func (s *ManifestSystem) Name() string { return s.M.Name }

func (s *ManifestSystem) VerifyWebhook(secret string, body []byte, header http.Header) bool {
	if s.M.Webhook.Signature == "" || secret == "" {
		return true
	}
	hdr := s.M.Webhook.SignatureHeader
	if hdr == "" {
		hdr = "X-Hub-Signature"
	}
	sig := header.Get(hdr)
	var prefix string
	var mac []byte
	switch s.M.Webhook.Signature {
	case "hmac-sha1":
		prefix = "sha1="
		h := hmac.New(sha1.New, []byte(secret))
		h.Write(body)
		mac = h.Sum(nil)
	case "hmac-sha256":
		prefix = "sha256="
		h := hmac.New(sha256.New, []byte(secret))
		h.Write(body)
		mac = h.Sum(nil)
	}
	got, ok := strings.CutPrefix(sig, prefix)
	if !ok {
		return false
	}
	return hmac.Equal([]byte(hex.EncodeToString(mac)), []byte(got))
}

func (s *ManifestSystem) ParseWebhook(body []byte) (WebhookEvent, error) {
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return WebhookEvent{}, fmt.Errorf("webhook payload: %w", err)
	}
	id := jsonPath(payload, s.M.Webhook.IDField)
	if id == "" {
		return WebhookEvent{}, fmt.Errorf("webhook payload: %s missing", s.M.Webhook.IDField)
	}
	eventID := jsonPath(payload, s.M.Webhook.EventIDField)
	title := jsonPath(payload, s.M.Webhook.TitleField)
	if title == "" {
		title = s.M.Name + " " + id
	}
	bodyText := jsonPath(payload, s.M.Webhook.BodyField)

	wake := true
	for _, c := range s.M.Webhook.IgnoreWhen {
		if jsonPath(payload, c.Field) == c.Equals {
			wake = false
			break
		}
	}
	label := s.M.Label
	if label == "" {
		label = s.M.Name
	}
	correlation := fmt.Sprintf("%s:%s", s.M.Name, id)
	return WebhookEvent{
		DedupKey:       fmt.Sprintf("%s:%s:%s", s.M.Name, id, eventID),
		CorrelationKey: correlation,
		Title:          fmt.Sprintf("%s: %s", label, title),
		TaskBody: fmt.Sprintf("New event in target system %s (id=%s).\nTitle: %s\n\nContent:\n%s\n\nHandle the case through the action proxy (system %s, id=%s).",
			s.M.Name, id, title, bodyText, s.M.Name, id),
		ResumeInput: fmt.Sprintf("New event for %s:\n%s", correlation, bodyText),
		Wake:        wake,
	}, nil
}

func (s *ManifestSystem) ActionSubject(action string, params json.RawMessage) string {
	a, ok := s.M.Actions[action]
	if !ok {
		return s.M.Name + ":" + action
	}
	var p map[string]json.RawMessage
	json.Unmarshal(params, &p)
	for _, rule := range a.SubjectWhen {
		if v, ok := p[rule.Param]; ok && jsonEqual(v, rule.Equals) {
			return s.M.Name + ":" + rule.Subject
		}
	}
	if a.Subject != "" {
		return s.M.Name + ":" + a.Subject
	}
	return s.M.Name + ":" + action
}

func (s *ManifestSystem) Execute(ctx context.Context, action string, params json.RawMessage, cred Credential) (any, error) {
	a, ok := s.M.Actions[action]
	if !ok {
		return nil, fmt.Errorf("unknown action %q", action)
	}
	var p map[string]json.RawMessage
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("params: %w", err)
	}

	// Substitute {param} placeholders in the path; the params used that way
	// drop out of the body candidate.
	path := a.Path
	for key, raw := range p {
		ph := "{" + key + "}"
		if strings.Contains(path, ph) {
			path = strings.ReplaceAll(path, ph, rawToString(raw))
			delete(p, key)
		}
	}
	if strings.Contains(path, "{") {
		return nil, fmt.Errorf("action %q: unresolved placeholder in %q", action, path)
	}

	var reqBody io.Reader
	if a.Method != http.MethodGet && len(p) > 0 {
		raw, err := json.Marshal(p)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, a.Method, strings.TrimRight(cred.BaseURL, "/")+path, reqBody)
	if err != nil {
		return nil, err
	}
	hdr := s.M.Auth.Header
	if hdr == "" {
		hdr = "Authorization"
	}
	format := s.M.Auth.Format
	if format == "" {
		format = "Bearer {token}"
	}
	req.Header.Set(hdr, strings.ReplaceAll(format, "{token}", cred.Token))
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	httpc := s.HTTP
	if httpc == nil {
		httpc = reqlog.Client(s.M.Name, 15*time.Second)
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s %s: HTTP %d: %.300s", s.M.Name, a.Method, path, resp.StatusCode, data)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return string(data), nil
	}
	return out, nil
}

func (s *ManifestSystem) PromptDoc() string {
	if s.M.PromptDoc != "" {
		return s.M.PromptDoc
	}
	names := make([]string, 0, len(s.M.Actions))
	for name := range s.M.Actions {
		names = append(names, name)
	}
	return fmt.Sprintf("Available %s actions: %s.", s.M.Name, strings.Join(names, ", "))
}

// jsonPath reads a dotted path ("ticket.id") out of an unmarshalled JSON value
// and returns it as a string (numbers without exponent noise).
func jsonPath(v any, path string) string {
	if path == "" {
		return ""
	}
	cur := v
	for _, part := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur, ok = obj[part]
		if !ok {
			return ""
		}
	}
	switch t := cur.(type) {
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%v", t)
	case bool:
		return fmt.Sprintf("%v", t)
	default:
		return ""
	}
}

func jsonEqual(a, b json.RawMessage) bool {
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	ar, _ := json.Marshal(av)
	br, _ := json.Marshal(bv)
	return bytes.Equal(ar, br)
}

func rawToString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return strings.Trim(string(raw), `"`)
}
