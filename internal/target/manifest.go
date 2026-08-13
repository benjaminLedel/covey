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
	"net/url"
	"regexp"
	"sort"
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

	// PromptDoc describes the actions for the agent's system prompt. With
	// per-action doc lines (ManifestAction.Doc) it becomes the preamble in front
	// of them; on its own it is the whole doc.
	PromptDoc string `json:"prompt_doc,omitempty"`

	// Probe is the read-only call that shows whether the stored credentials
	// actually work, and as whom (target.Prober). Without it the plugin offers
	// no connection test — "saved" and "works" then stay two different things
	// until an agent runs.
	Probe *ManifestProbe `json:"probe,omitempty"`

	// Poll declares how the control plane checks up front whether there is any
	// work at all (target.WorkChecker) — what makes `nur-wenn: <system>` in
	// HEARTBEAT.md gate anything for a manifest plugin. Key = the sub-scope
	// after the colon (`nur-wenn: <system>:<kind>`); the entry under "" applies
	// without one.
	//
	// Without this block every nur-wenn: on the system fires unconditionally
	// (fail-open) — the plugin cannot answer the question, and an unanswerable
	// condition must not leave work lying around.
	Poll map[string]ManifestPoll `json:"poll,omitempty"`

	// Scopes is the vocabulary of access levels this plugin understands in
	// ACCESS.md (`- system: redmine scope: read,comment`). Declaring it means
	// the setup assistant offers exactly these words instead of letting somebody
	// type one that is then silently ignored.
	Scopes []string `json:"scopes,omitempty"`
}

// ManifestProbe is one cheap, read-only GET. Read-only by contract — a probe
// that changed something in the foreign system would be a poor kind of test.
type ManifestProbe struct {
	// Path relative to the base URL, e.g. "/users/me".
	Path string `json:"path"`
	// IdentityField is the dotted path to the identity the target system
	// reports back (e.g. "login", "user.name"). Displayed as is, so it should
	// be short and recognisable. Empty = the probe only shows that the call
	// worked.
	IdentityField string `json:"identity_field,omitempty"`
}

// ManifestPoll is one GET whose result answers "is there work?".
type ManifestPoll struct {
	// Path relative to the base URL, e.g. "/issues?assignee=me&state=open".
	Path string `json:"path"`
	// ItemsField is the dotted path to the array of open items. Empty = the
	// response itself is the array. There is work when the array is not empty.
	ItemsField string `json:"items_field,omitempty"`
	// SignatureField is a per-item field that changes when the item does (an
	// id, an updated_at, the id of the last comment). From it the control plane
	// forms the work signature (target.SignedWorkChecker): the heartbeat then
	// fires once per piece of news instead of on every tick for as long as the
	// state persists.
	//
	// Empty = no signature; the condition then fires on every level, as a plain
	// WorkChecker does.
	SignatureField string `json:"signature_field,omitempty"`
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

	// Doc is the action's line in the agent's system prompt ("reply to a
	// ticket; params: id, body"). Once any action carries one, the prompt doc is
	// composed from these lines instead of from the bare action names — and only
	// then can it be narrowed to an agent's scopes, because free text cannot be
	// cut down without knowing what belongs to what.
	Doc string `json:"doc,omitempty"`

	// Scope is the access level from Manifest.Scopes this action belongs to.
	// An agent without it does not get the action's line in its prompt doc.
	// Empty = the action belongs to every scope.
	Scope string `json:"scope,omitempty"`
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
	scopes := map[string]bool{}
	for _, s := range m.Scopes {
		if s == "" {
			return m, fmt.Errorf("manifest: scopes must not contain an empty entry")
		}
		if scopes[s] {
			return m, fmt.Errorf("manifest: scope %q listed twice", s)
		}
		scopes[s] = true
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
		// A scope nobody declared would silently take the action out of every
		// agent's doc — the exact failure the declared vocabulary exists to
		// prevent.
		if a.Scope != "" && !scopes[a.Scope] {
			return m, fmt.Errorf("manifest: action %q: scope %q is not declared in scopes", name, a.Scope)
		}
	}
	if m.Probe != nil && !strings.HasPrefix(m.Probe.Path, "/") {
		return m, fmt.Errorf("manifest: probe.path must start with /")
	}
	for kind, p := range m.Poll {
		where := "poll"
		if kind != "" {
			where = fmt.Sprintf("poll %q", kind)
		}
		if !strings.HasPrefix(p.Path, "/") {
			return m, fmt.Errorf("manifest: %s: path must start with /", where)
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
	//
	// The value is ESCAPED, and that is not cosmetic. The path of a manifest
	// action is the boundary the action is scoped to — the guard rails govern
	// `system:action`, and the manifest author decided which endpoint that
	// reaches. An unescaped value breaks out of it: a parameter `../../admin`
	// turns an action on /tickets/{id} into a call to a completely different
	// endpoint of the same system. The value comes from the agent, and by our
	// own threat model (spec/04) the agent is not a trustworthy source — a
	// prompt-injected agent is exactly the case this is written for.
	path := a.Path
	for key, raw := range p {
		ph := "{" + key + "}"
		if !strings.Contains(path, ph) {
			continue
		}
		value := rawToString(raw)
		if err := checkPathParam(key, value); err != nil {
			return nil, err
		}
		path = strings.ReplaceAll(path, ph, url.PathEscape(value))
		delete(p, key)
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
	s.setAuth(req, cred)
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

func (s *ManifestSystem) PromptDoc() string { return s.doc(nil, false) }

// PromptDocForScopes (target.ScopedDocSystem) narrows the doc to the scopes an
// agent was granted in ACCESS.md.
//
// It can only narrow what is structured. A manifest whose prompt_doc is a block
// of free text stays whole no matter what the scopes say — there is no way to
// tell which sentence belongs to which action. Per-action doc lines are what
// make the narrowing possible; without them this is a full doc with extra steps.
// Fail-open in both directions: no scopes recorded, or no action declaring one,
// and the agent gets everything.
func (s *ManifestSystem) PromptDocForScopes(scopes []string) string {
	if len(scopes) == 0 || !s.scoped() {
		return s.doc(nil, false)
	}
	granted := make(map[string]bool, len(scopes))
	for _, sc := range scopes {
		granted[sc] = true
	}
	return s.doc(granted, true)
}

// scoped: does any action belong to a scope at all?
func (s *ManifestSystem) scoped() bool {
	for _, a := range s.M.Actions {
		if a.Scope != "" {
			return true
		}
	}
	return false
}

// doc renders the prompt documentation. With granted != nil only the actions
// whose scope the agent holds are rendered (an action without a scope belongs to
// everybody).
func (s *ManifestSystem) doc(granted map[string]bool, narrow bool) string {
	names := make([]string, 0, len(s.M.Actions))
	described := false
	for name, a := range s.M.Actions {
		if narrow && a.Scope != "" && !granted[a.Scope] {
			continue
		}
		names = append(names, name)
		if a.Doc != "" {
			described = true
		}
	}
	sort.Strings(names)

	// Free text without per-action lines: hand it over as it stands.
	if !described {
		if s.M.PromptDoc != "" {
			return s.M.PromptDoc
		}
		if len(names) == 0 {
			return fmt.Sprintf("No %s actions are available to you.", s.M.Name)
		}
		return fmt.Sprintf("Available %s actions: %s.", s.M.Name, strings.Join(names, ", "))
	}

	var b strings.Builder
	if s.M.PromptDoc != "" {
		b.WriteString(s.M.PromptDoc)
		b.WriteString("\n\n")
	}
	if len(names) == 0 {
		b.WriteString(fmt.Sprintf("No %s actions are available to you.", s.M.Name))
		return b.String()
	}
	b.WriteString(fmt.Sprintf("Available %s actions:\n", s.M.Name))
	for _, name := range names {
		if d := s.M.Actions[name].Doc; d != "" {
			fmt.Fprintf(&b, "- %s: %s\n", name, d)
		} else {
			fmt.Fprintf(&b, "- %s\n", name)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// Supports (target.CapabilityReporter) reports the optional capabilities this
// manifest actually declares. A manifest system's Go type carries the methods
// either way — only the file says whether they mean anything.
func (s *ManifestSystem) Supports(capability string) bool {
	switch capability {
	case CapProbe:
		return s.M.Probe != nil
	case CapPoll:
		return len(s.M.Poll) > 0
	default:
		return true
	}
}

// Probe (target.Prober) runs the declared read-only call and reports the
// identity the target system answers with.
func (s *ManifestSystem) Probe(ctx context.Context, cred Credential) (string, error) {
	if s.M.Probe == nil {
		return "", fmt.Errorf("%s declares no probe", s.M.Name)
	}
	body, err := s.get(ctx, s.M.Probe.Path, cred)
	if err != nil {
		return "", err
	}
	if s.M.Probe.IdentityField == "" {
		return "ok", nil
	}
	if id := jsonPath(body, s.M.Probe.IdentityField); id != "" {
		return id, nil
	}
	// The call worked — the field is the plugin's problem, not the
	// credential's, and reporting a failure here would blame the wrong thing.
	return "ok", nil
}

// HasWork (target.WorkChecker) checks the declared poll without a sub-scope.
func (s *ManifestSystem) HasWork(ctx context.Context, cred Credential) (bool, error) {
	has, _, err := s.HasWorkSigned(ctx, cred, "")
	return has, err
}

// HasWorkKind (target.KindWorkChecker) checks the poll of one sub-scope.
func (s *ManifestSystem) HasWorkKind(ctx context.Context, cred Credential, kind string) (bool, error) {
	has, _, err := s.HasWorkSigned(ctx, cred, kind)
	return has, err
}

// HasWorkSigned (target.SignedWorkChecker) additionally returns the signature of
// the work found, so the same piece of news wakes the agent once rather than on
// every tick until it acts.
func (s *ManifestSystem) HasWorkSigned(ctx context.Context, cred Credential, kind string) (bool, string, error) {
	p, ok := s.M.Poll[kind]
	if !ok {
		// An unknown sub-scope must not report LESS than the plain check
		// (fail-open) — so fall back to the entry without one.
		if p, ok = s.M.Poll[""]; !ok {
			return true, "", nil
		}
	}
	body, err := s.get(ctx, p.Path, cred)
	if err != nil {
		return false, "", err
	}
	items := pollItems(body, p.ItemsField)
	if len(items) == 0 {
		return false, "", nil
	}
	return true, pollSignature(items, p.SignatureField), nil
}

// pollItems reads the array of open items out of a poll response.
func pollItems(body any, field string) []any {
	cur := body
	if field != "" {
		for _, part := range strings.Split(field, ".") {
			obj, ok := cur.(map[string]any)
			if !ok {
				return nil
			}
			if cur, ok = obj[part]; !ok {
				return nil
			}
		}
	}
	items, _ := cur.([]any)
	return items
}

// pollSignature describes WHAT the check responded to — a short, stable string
// that changes when the work does. Sorted, so the order the target system
// happens to answer in does not by itself look like news; hashed once it grows
// long, because the signature is stored per heartbeat and only ever compared,
// never read.
func pollSignature(items []any, field string) string {
	if field == "" {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, it := range items {
		if v := jsonPath(it, field); v != "" {
			parts = append(parts, v)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	sort.Strings(parts)
	joined := strings.Join(parts, ",")
	if len(joined) <= 200 {
		return joined
	}
	sum := sha256.Sum256([]byte(joined))
	return fmt.Sprintf("%d@%s", len(parts), hex.EncodeToString(sum[:8]))
}

// get performs one authenticated GET and returns the parsed JSON body — the
// shared plumbing behind probe and poll. Both are read-only calls the CONTROL
// PLANE makes on its own: no agent is involved, so no path parameters are
// substituted and nothing from a run can steer them.
func (s *ManifestSystem) get(ctx context.Context, path string, cred Credential) (any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cred.BaseURL, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	s.setAuth(req, cred)
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
		return nil, fmt.Errorf("%s GET %s: HTTP %d: %.300s", s.M.Name, path, resp.StatusCode, data)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("%s GET %s: response is not JSON", s.M.Name, path)
	}
	return out, nil
}

// setAuth writes the brokered token into the request the way the manifest
// declares.
func (s *ManifestSystem) setAuth(req *http.Request, cred Credential) {
	hdr := s.M.Auth.Header
	if hdr == "" {
		hdr = "Authorization"
	}
	format := s.M.Auth.Format
	if format == "" {
		format = "Bearer {token}"
	}
	req.Header.Set(hdr, strings.ReplaceAll(format, "{token}", cred.Token))
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

// checkPathParam rejects values that would change the MEANING of a path
// instead of filling a slot in it. url.PathEscape alone is not enough: it
// encodes the separator, but leaves the dot segments "." and ".." untouched —
// and a segment consisting solely of ".." shifts the request up one level even
// without a slash of its own.
//
// Control characters are refused on top of that. They have no business in a
// path segment, and a value with CR/LF in it is the classic building block of
// request smuggling as soon as a proxy sits in between.
func checkPathParam(key, value string) error {
	if value == "." || value == ".." {
		return fmt.Errorf("parameter %q: %q is a path segment, not a value", key, value)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("parameter %q contains a control character", key)
		}
	}
	return nil
}

func rawToString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return strings.Trim(string(raw), `"`)
}
