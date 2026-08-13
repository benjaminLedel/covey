package wasmplug

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"covey/internal/reqlog"
	"covey/internal/target"
)

// System makes a compiled module a target system like any other. That is the
// whole point of this file: broker, guard rails, recording, prompt docs and
// ACCESS.md sit downstream of target.System, and a wasm plugin must not get a
// path of its own around any of them.
type System struct {
	mod  *Module
	desc Description
	HTTP *http.Client

	// docs caches rendered prompt documentation. Asking the module costs an
	// instantiation, and the doc is read on EVERY turn of every agent that has
	// the system — that is the one call frequent enough to matter.
	docMu sync.Mutex
	docs  map[string]string
}

func NewSystem(mod *Module) *System {
	d := mod.Describe()
	return &System{
		mod:  mod,
		desc: d,
		HTTP: reqlog.Client(d.Name, 30*time.Second),
		docs: map[string]string{},
	}
}

func (s *System) Name() string          { return s.desc.Name }
func (s *System) Describe() Description { return s.desc }
func (s *System) Close(ctx context.Context) error {
	return s.mod.Close(ctx)
}

// Supports (target.CapabilityReporter): a compiled plugin carries every method
// in its Go type just as the manifest engine does, so what it can actually do
// has to come from what it said about itself.
func (s *System) Supports(capability string) bool {
	switch capability {
	case target.CapProbe:
		return s.desc.Probe
	case target.CapPoll:
		return s.desc.Poll
	default:
		return true
	}
}

// ActionSubject maps an action onto its guard-rail subject. The module may name
// one (e.g. comment → comment_external), because only it knows which of its
// actions are the ones a person would want to gate.
func (s *System) ActionSubject(action string, _ json.RawMessage) string {
	for _, a := range s.desc.Actions {
		if a.Name == action && a.Subject != "" {
			return s.desc.Name + ":" + a.Subject
		}
	}
	return s.desc.Name + ":" + action
}

func (s *System) Execute(ctx context.Context, action string, params json.RawMessage, cred target.Credential) (any, error) {
	out, err := s.mod.Invoke(ctx, Invocation{Op: "execute", Action: action, Params: params}, s.fetcher(cred))
	if err != nil {
		return nil, err
	}
	if out.Error != "" {
		return nil, fmt.Errorf("%s: %s", s.desc.Name, out.Error)
	}
	var v any
	if len(out.Result) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(out.Result, &v); err != nil {
		return string(out.Result), nil
	}
	return v, nil
}

func (s *System) PromptDoc() string { return s.PromptDocForScopes(nil) }

// PromptDocForScopes (target.ScopedDocSystem) asks the module for the doc an
// agent with these scopes should see.
func (s *System) PromptDocForScopes(scopes []string) string {
	key := strings.Join(scopes, ",")
	s.docMu.Lock()
	if doc, ok := s.docs[key]; ok {
		s.docMu.Unlock()
		return doc
	}
	s.docMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	doc := s.fallbackDoc(scopes)
	// No fetcher: documentation is not something a plugin needs a target system
	// for, and a doc that depended on a live system would be missing whenever
	// that system is down.
	if out, err := s.mod.Invoke(ctx, Invocation{Op: "prompt_doc", Scopes: scopes}, nil); err == nil && out.Error == "" {
		var text string
		if json.Unmarshal(out.Result, &text) == nil && text != "" {
			doc = text
		}
	}
	s.docMu.Lock()
	s.docs[key] = doc
	s.docMu.Unlock()
	return doc
}

// fallbackDoc is what an agent gets when the module cannot produce a doc — its
// declared actions, narrowed to the granted scopes. Better a plain list than
// nothing: without a doc the agent does not know the actions exist at all.
func (s *System) fallbackDoc(scopes []string) string {
	granted := map[string]bool{}
	for _, sc := range scopes {
		granted[sc] = true
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Available %s actions:\n", s.desc.Name)
	var n int
	for _, a := range s.desc.Actions {
		if a.Scope != "" && len(scopes) > 0 && !granted[a.Scope] {
			continue
		}
		n++
		if a.Doc != "" {
			fmt.Fprintf(&b, "- %s: %s\n", a.Name, a.Doc)
		} else {
			fmt.Fprintf(&b, "- %s\n", a.Name)
		}
	}
	if n == 0 {
		return fmt.Sprintf("No %s actions are available to you.", s.desc.Name)
	}
	return strings.TrimRight(b.String(), "\n")
}

// Probe (target.Prober) asks the module to make one read-only call and report
// whose credential this is.
func (s *System) Probe(ctx context.Context, cred target.Credential) (string, error) {
	out, err := s.mod.Invoke(ctx, Invocation{Op: "probe"}, s.fetcher(cred))
	if err != nil {
		return "", err
	}
	if out.Error != "" {
		return "", fmt.Errorf("%s", out.Error)
	}
	var who string
	if json.Unmarshal(out.Result, &who) == nil && who != "" {
		return who, nil
	}
	return "ok", nil
}

func (s *System) HasWork(ctx context.Context, cred target.Credential) (bool, error) {
	has, _, err := s.HasWorkSigned(ctx, cred, "")
	return has, err
}

func (s *System) HasWorkKind(ctx context.Context, cred target.Credential, kind string) (bool, error) {
	has, _, err := s.HasWorkSigned(ctx, cred, kind)
	return has, err
}

// HasWorkSigned (target.SignedWorkChecker) lets the module answer both whether
// there is work and what it responded to.
func (s *System) HasWorkSigned(ctx context.Context, cred target.Credential, kind string) (bool, string, error) {
	if !s.desc.Poll {
		return true, "", nil // fail-open, as everywhere else on this path
	}
	out, err := s.mod.Invoke(ctx, Invocation{Op: "poll", Kind: kind}, s.fetcher(cred))
	if err != nil {
		return false, "", err
	}
	if out.Error != "" {
		return false, "", fmt.Errorf("%s", out.Error)
	}
	var pr PollResult
	if err := json.Unmarshal(out.Result, &pr); err != nil {
		return true, "", nil // an unreadable answer must not silence a heartbeat
	}
	return pr.HasWork, pr.Signature, nil
}

// fetcher performs the module's requests against the brokered target system.
//
// Everything that makes this safe is here: the module names a PATH, never a
// host; the host adds the base URL and the token; and headers the module sets
// cannot overwrite the authentication. A plugin can therefore reach exactly the
// system the organization pointed it at, and nothing else.
func (s *System) fetcher(cred target.Credential) Fetcher { return s.fetch(cred, false) }

// fetcherAllowingHTTP exists for tests, whose declared host is a local server
// on plain http. Nothing outside a test may call it — a declared host on the
// open network has to be https.
func (s *System) fetcherAllowingHTTP(cred target.Credential) Fetcher { return s.fetch(cred, true) }

func (s *System) fetch(cred target.Credential, allowHTTP bool) Fetcher {
	return func(ctx context.Context, req FetchRequest) FetchResponse {
		// A path is relative to the brokered system; an absolute URL is only
		// allowed to a host the module DECLARED, and never carries the
		// credential.
		path, foreign := req.Path, ""
		switch {
		// "//host/x" first: it also starts with "/", and taking it for a
		// relative path is exactly the confusion this check exists to stop.
		case strings.HasPrefix(path, "//"):
			return FetchResponse{Error: "a plugin cannot name a host"}
		case strings.HasPrefix(path, "/"):
		case strings.Contains(path, "://"):
			u, err := url.Parse(path)
			if err != nil || u.Host == "" {
				return FetchResponse{Error: "a plugin cannot name a host"}
			}
			if u.Scheme != "https" && !allowHTTP {
				return FetchResponse{Error: "a declared host has to be reached over https"}
			}
			if !s.declared(u.Host) {
				return FetchResponse{Error: fmt.Sprintf(
					"host %q is not declared by this plugin — declare it in Describe so an operator sees it before installing", u.Host)}
			}
			foreign = u.Host
		default:
			return FetchResponse{Error: fmt.Sprintf("path %q must start with /", path)}
		}
		method := strings.ToUpper(req.Method)
		switch method {
		case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		case "":
			method = http.MethodGet
		default:
			return FetchResponse{Error: "method " + method + " is not allowed"}
		}

		full := path
		if foreign == "" {
			full = strings.TrimRight(cred.BaseURL, "/") + path
		}
		if len(req.Query) > 0 {
			q := url.Values{}
			for k, v := range req.Query {
				q.Set(k, v)
			}
			sep := "?"
			if strings.Contains(full, "?") {
				sep = "&"
			}
			full += sep + q.Encode()
		}

		var body io.Reader
		if len(req.Body) > 0 {
			body = bytes.NewReader(req.Body)
		}
		hreq, err := http.NewRequestWithContext(ctx, method, full, body)
		if err != nil {
			return FetchResponse{Error: err.Error()}
		}
		for k, v := range req.Header {
			// The plugin may set headers, but not the one that carries the
			// credential — otherwise "it never sees the token" would be true
			// and beside the point.
			if strings.EqualFold(k, authHeader(s.desc.Auth)) || strings.EqualFold(k, "authorization") {
				continue
			}
			hreq.Header.Set(k, v)
		}
		if body != nil && hreq.Header.Get("Content-Type") == "" {
			hreq.Header.Set("Content-Type", "application/json")
		}
		// The credential belongs to the brokered system. A declared host is
		// somebody else, and a token that travels to somebody else is a token
		// that leaked.
		if cred.Token != "" && foreign == "" {
			format := s.desc.Auth.Format
			if format == "" {
				format = "Bearer {token}"
			}
			hreq.Header.Set(authHeader(s.desc.Auth), strings.ReplaceAll(format, "{token}", cred.Token))
		}

		httpc := s.HTTP
		if httpc == nil {
			httpc = reqlog.Client(s.desc.Name, 30*time.Second)
		}
		resp, err := httpc.Do(hreq)
		if err != nil {
			return FetchResponse{Error: err.Error()}
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if err != nil {
			return FetchResponse{Error: err.Error()}
		}
		out := FetchResponse{Status: resp.StatusCode}
		if json.Valid(data) {
			out.Body = data
		} else {
			out.Text = string(data)
		}
		return out
	}
}

// declared: did the module say it needs this host? Compared case-insensitively
// and exactly — no wildcards, because "*.example.com" is a good way to end up
// somewhere nobody reviewed.
func (s *System) declared(host string) bool {
	for _, h := range s.desc.Hosts {
		if strings.EqualFold(strings.TrimSpace(h), host) {
			return true
		}
	}
	return false
}

func authHeader(a AuthDesc) string {
	if a.Header != "" {
		return a.Header
	}
	return "Authorization"
}
