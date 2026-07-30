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

// Manifest ist ein deklaratives Zielsystem-Plugin: eine JSON-Datei, die ein
// Admin hochlädt, statt Go-Code zu kompilieren. Eine generische REST-Engine
// (ManifestSystem) interpretiert sie — damit lassen sich API-Key-basierte
// REST-Systeme anbinden, ohne Covey neu auszuliefern. Für alles darüber
// hinaus (OAuth-Flows, Spezial-Protokolle) bleibt der Weg über ein
// kompiliertes Built-in-Plugin.
type Manifest struct {
	Name        string `json:"name"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	// Category ordnet das Plugin im Zielsystem-Store ein (siehe Category…
	// in target.go). Leer = "other".
	Category string `json:"category,omitempty"`

	// Auth beschreibt, wie das gebrokerte Token in Requests landet.
	Auth ManifestAuth `json:"auth"`

	// Webhook mappt eingehende Payloads auf das Wake-Event. Feld-Referenzen
	// sind Punkt-Pfade in das JSON ("ticket.id", "article.body").
	Webhook ManifestWebhook `json:"webhook"`

	// Actions sind die Aktionen, die der Agent über den Action-Proxy
	// aufrufen darf. Key = Aktionsname.
	Actions map[string]ManifestAction `json:"actions"`

	// PromptDoc beschreibt die Aktionen für den System-Prompt des Agenten.
	PromptDoc string `json:"prompt_doc,omitempty"`
}

type ManifestAuth struct {
	// Header, in den das Token geschrieben wird (Default "Authorization").
	Header string `json:"header,omitempty"`
	// Format des Header-Werts; {token} wird ersetzt (Default "Bearer {token}").
	Format string `json:"format,omitempty"`
}

type ManifestWebhook struct {
	// Signature: "hmac-sha1" | "hmac-sha256" | "" (keine Prüfung).
	Signature string `json:"signature,omitempty"`
	// SignatureHeader trägt die Signatur (Default "X-Hub-Signature").
	SignatureHeader string `json:"signature_header,omitempty"`

	// IDField identifiziert das fachliche Objekt (Korrelations-Key).
	IDField string `json:"id_field"`
	// EventIDField identifiziert das konkrete Ereignis (Dedup, z. B. Artikel-id).
	EventIDField string `json:"event_id_field,omitempty"`
	// TitleField/BodyField füllen die Backlog-Aufgabe.
	TitleField string `json:"title_field,omitempty"`
	BodyField  string `json:"body_field,omitempty"`
	// IgnoreWhen: weckt NICHT, wenn Feld == Wert (z. B. das eigene Agent-Echo).
	IgnoreWhen []ManifestCondition `json:"ignore_when,omitempty"`
}

type ManifestCondition struct {
	Field  string `json:"field"`
	Equals string `json:"equals"`
}

type ManifestAction struct {
	Method string `json:"method"`
	// Path relativ zur Base-URL; {param}-Platzhalter werden aus den
	// JSON-Params ersetzt, verbleibende Params gehen als JSON-Body raus.
	Path string `json:"path"`
	// Subject überschreibt das Guard-Rail-Subjekt (Default system:aktion).
	// SubjectWhen erlaubt param-abhängige Subjekte (z. B. internal=false).
	Subject     string                `json:"subject,omitempty"`
	SubjectWhen []ManifestSubjectRule `json:"subject_when,omitempty"`
}

type ManifestSubjectRule struct {
	Param   string          `json:"param"`
	Equals  json.RawMessage `json:"equals"`
	Subject string          `json:"subject"`
}

var manifestNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,31}$`)

// ParseManifest validiert eine hochgeladene Plugin-Datei fail-closed:
// lieber ein klarer Fehler beim Upload als ein stilles Fehlverhalten zur Laufzeit.
func ParseManifest(raw []byte) (Manifest, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return m, fmt.Errorf("manifest: %w", err)
	}
	if !manifestNameRe.MatchString(m.Name) {
		return m, fmt.Errorf("manifest: name muss %s entsprechen", manifestNameRe)
	}
	if len(m.Actions) == 0 {
		return m, fmt.Errorf("manifest: mindestens eine action nötig")
	}
	for name, a := range m.Actions {
		switch a.Method {
		case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			return m, fmt.Errorf("manifest: action %q: method %q nicht erlaubt", name, a.Method)
		}
		if !strings.HasPrefix(a.Path, "/") {
			return m, fmt.Errorf("manifest: action %q: path muss mit / beginnen", name)
		}
	}
	switch m.Webhook.Signature {
	case "", "hmac-sha1", "hmac-sha256":
	default:
		return m, fmt.Errorf("manifest: webhook.signature %q unbekannt (hmac-sha1|hmac-sha256)", m.Webhook.Signature)
	}
	if m.Webhook.IDField == "" {
		return m, fmt.Errorf("manifest: webhook.id_field fehlt")
	}
	return m, nil
}

// ManifestSystem interpretiert ein Manifest als target.System — dieselbe
// Schnittstelle wie ein kompiliertes Plugin, alle Enforcement-Punkte
// (Guard-Rails, Broker, Recording) greifen identisch.
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
		return WebhookEvent{}, fmt.Errorf("webhook payload: %s fehlt", s.M.Webhook.IDField)
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
		TaskBody: fmt.Sprintf("Neues Ereignis im Zielsystem %s (id=%s).\nTitel: %s\n\nInhalt:\n%s\n\nBearbeite den Vorgang über den Action-Proxy (system %s, id=%s).",
			s.M.Name, id, title, bodyText, s.M.Name, id),
		ResumeInput: fmt.Sprintf("Neues Ereignis zu %s:\n%s", correlation, bodyText),
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
		return nil, fmt.Errorf("unbekannte aktion %q", action)
	}
	var p map[string]json.RawMessage
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("params: %w", err)
	}

	// {param}-Platzhalter im Pfad ersetzen; verwendete Params wandern
	// aus dem Body-Kandidaten raus.
	path := a.Path
	for key, raw := range p {
		ph := "{" + key + "}"
		if strings.Contains(path, ph) {
			path = strings.ReplaceAll(path, ph, rawToString(raw))
			delete(p, key)
		}
	}
	if strings.Contains(path, "{") {
		return nil, fmt.Errorf("aktion %q: unaufgelöster platzhalter in %q", action, path)
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
	return fmt.Sprintf("Verfügbare %s-Aktionen: %s.", s.M.Name, strings.Join(names, ", "))
}

// jsonPath liest einen Punkt-Pfad ("ticket.id") aus einem entpackten
// JSON-Wert und liefert ihn als String (Zahlen ohne Exponent-Rauschen).
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
