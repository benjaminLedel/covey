package integration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"covey/internal/backlog"
)

// TestConfluencePlugin: the company's documentation through the whole stack.
// The agent reads a page, appends a section to it and comments — through the
// action proxy, the broker's two-secret convention and the ACCESS.md gate, onto
// a Confluence double that behaves like a Cloud site.
//
// What no unit test can see is checked here: that the plugin is reachable at
// all through activation + ACCESS + broker, that the credential's space wall
// holds inside a run, and that appending really leaves the existing page
// untouched — the property the whole action exists for.
func TestConfluencePlugin(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	wiki := newFakeConfluence(t)

	admin.expect(http.MethodPatch, "/api/v1/targets/confluence", map[string]any{"enabled": true}, http.StatusOK)

	agent, err := s.registry.Create(ctx, s.orgID, "writer", "Doc writer", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, agent.ID, map[string]string{
		"SOUL.md":   "# Doc writer",
		"ACCESS.md": "- system: confluence scope: read,write,comment",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	// The /wiki path is left off on purpose — the browser hides it, so nobody
	// has it in hand, and the plugin appends it for a Cloud credential.
	s.secrets.Put(ctx, s.orgID, "confluence_url", wiki.srv.URL+` space="ENG"`)
	s.secrets.Put(ctx, s.orgID, "confluence_token", "covey-bot@acme.example:tok3n")
	s.secrets.Assign(ctx, s.orgID, "confluence_url", agent.ID)
	s.secrets.Assign(ctx, s.orgID, "confluence_token", agent.ID)

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Document the release",
		`[mock:action confluence/get_page {"page_id":"131075"}]`+
			` [mock:action confluence/append_to_page {"page_id":"131075","version":7,"message":"release 1.2","body":"## Release 1.2\n\nThe importer guards the null case now — ACME-17."}]`+
			` [mock:action confluence/comment {"page_id":"131075","body":"Added the release section."}]`+
			` [mock:result documented]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "confluence task done", 20*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	for _, subject := range []string{"confluence:get_page", "confluence:append_to_page", "confluence:comment"} {
		var n int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM recording_events
			WHERE agent_id=$1 AND kind='action' AND payload->>'action'=$2 AND (payload->>'ok')::bool`,
			agent.ID, subject).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("action %s missing from the recording (count=%d)", subject, n)
		}
	}

	body, version := wiki.page()
	if version != 8 {
		t.Errorf("version = %d, want 8", version)
	}
	// The point of appending: what was there is still there, byte for byte.
	// Re-rendering it would reformat everything a human wrote, and a diff in
	// which the whole page moved is a diff nobody reviews.
	if !strings.HasPrefix(body, confluenceStoredPage) {
		t.Errorf("the existing page did not survive the append:\n%s", body)
	}
	if !strings.Contains(body, "<h2>Release 1.2</h2>") {
		t.Errorf("the new section is missing:\n%s", body)
	}
	// The agent wrote Markdown; the page holds storage format.
	if strings.Contains(body, "## Release 1.2") {
		t.Errorf("Markdown reached the page untranslated:\n%s", body)
	}
	if got := wiki.lastComment(); !strings.Contains(got, "<p>Added the release section.</p>") {
		t.Errorf("comment = %q", got)
	}

	// The wall holds inside a run: a page in another space fails the task
	// instead of quietly doing nothing.
	outside, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Foreign space",
		`[mock:action confluence/get_page {"page_id":"222222"}]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task fails outside the wall", 20*time.Second, func() bool {
		return s.taskState(outside.ID) == backlog.StateFailed
	})

	// Without an ACCESS.md line the broker refuses, activation or not.
	stranger, err := s.registry.Create(ctx, s.orgID, "no-wiki", "Without access", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, stranger.ID, map[string]string{"SOUL.md": "# No access"}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	denied, err := s.backlog.Create(ctx, s.orgID, stranger.ID, "Forbidden attempt",
		`[mock:action confluence/get_page {"page_id":"131075"}]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task fails without access", 20*time.Second, func() bool {
		return s.taskState(denied.ID) == backlog.StateFailed
	})
}

// confluenceStoredPage is the page as Confluence stores it — XHTML with
// Atlassian's own elements in it, which is exactly what must never reach the
// agent and must never be lost on the way back.
const confluenceStoredPage = `<h2>Deployment</h2>` +
	`<p>The importer runs at <code>03:00</code>.</p>` +
	`<ac:structured-macro ac:name="code"><ac:parameter ac:name="language">bash</ac:parameter>` +
	`<ac:plain-text-body><![CDATA[make sandbox-image]]></ac:plain-text-body></ac:structured-macro>`

// fakeConfluence is a Confluence Cloud double: one space, one page, the handful
// of endpoints the plugin calls.
type fakeConfluence struct {
	t *testing.T

	mu       sync.Mutex
	body     string
	version  int
	comments []string

	srv *httptest.Server
}

func newFakeConfluence(t *testing.T) *fakeConfluence {
	t.Helper()
	f := &fakeConfluence{t: t, body: confluenceStoredPage, version: 7}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeConfluence) page() (string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.body, f.version
}

func (f *fakeConfluence) lastComment() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.comments) == 0 {
		return ""
	}
	return f.comments[len(f.comments)-1]
}

func (f *fakeConfluence) handle(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	body := map[string]any{}
	if len(raw) > 0 {
		json.Unmarshal(raw, &body)
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	out := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
	// Every Cloud call carries the /wiki context path. A client that leaves it
	// off lands nowhere, and that is worth failing loudly for.
	path, ok := strings.CutPrefix(r.URL.Path, "/wiki")
	if !ok {
		f.t.Errorf("call without the /wiki context path: %s", r.URL.Path)
		http.Error(w, `{"message":"no /wiki"}`, http.StatusNotFound)
		return
	}

	switch {
	case path == "/rest/api/user/current":
		out(map[string]any{"accountId": "5b10bot", "displayName": "Covey Bot", "email": "covey-bot@acme.example"})

	case path == "/api/v2/spaces":
		out(map[string]any{"results": []any{
			map[string]any{"id": "98305", "key": "ENG", "name": "Engineering"},
			map[string]any{"id": "98306", "key": "OPS", "name": "Operations"},
		}})

	case path == "/api/v2/pages/131075" && r.Method == http.MethodGet:
		out(map[string]any{
			"id": "131075", "title": "Deployment runbook", "spaceId": "98305", "status": "current",
			"version": map[string]any{"number": f.version, "createdAt": "2026-08-24T09:00:00.000Z"},
			"body":    map[string]any{"storage": map[string]any{"value": f.body}},
			"_links":  map[string]any{"webui": "/spaces/ENG/pages/131075"},
		})

	case path == "/api/v2/pages/222222" && r.Method == http.MethodGet:
		out(map[string]any{
			"id": "222222", "title": "Ops runbook", "spaceId": "98306", "status": "current",
			"version": map[string]any{"number": 1},
			"body":    map[string]any{"storage": map[string]any{"value": "<p>not yours</p>"}},
			"_links":  map[string]any{"webui": "/spaces/OPS/pages/222222"},
		})

	case path == "/api/v2/pages/131075" && r.Method == http.MethodPut:
		if b, ok := body["body"].(map[string]any); ok {
			f.body, _ = b["value"].(string)
		}
		if v, ok := body["version"].(map[string]any); ok {
			if n, ok := v["number"].(float64); ok {
				f.version = int(n)
			}
		}
		w.WriteHeader(http.StatusOK)

	case path == "/api/v2/footer-comments" && r.Method == http.MethodPost:
		if b, ok := body["body"].(map[string]any); ok {
			value, _ := b["value"].(string)
			f.comments = append(f.comments, value)
		}
		out(map[string]any{"id": "9001"})

	default:
		f.t.Errorf("fake confluence: unexpected call %s %s", r.Method, r.URL.Path)
		http.Error(w, `{"message":"no route"}`, http.StatusNotFound)
	}
}
