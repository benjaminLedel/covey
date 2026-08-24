package integration

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"covey/internal/backlog"
)

// TestJiraPlugin: the developer loop through the whole stack. The agent takes a
// Jira ticket on, moves it through its workflow and writes back — through the
// action proxy, the broker's two-secret convention and the ACCESS.md gate, onto
// a Jira double that behaves like a Cloud site.
//
// Three things are checked here that no unit test can see: that the plugin is
// reachable at all through activation + ACCESS + broker, that the guard-rail
// subject a comment produces is the external one, and that the credential's
// project wall holds inside a run rather than only in the client.
func TestJiraPlugin(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	jira := newFakeJira(t)

	// Activation is opt-in — as with every target system.
	admin.expect(http.MethodPatch, "/api/v1/targets/jira", map[string]any{"enabled": true}, http.StatusOK)

	agent, err := s.registry.Create(ctx, s.orgID, "dev-1", "Developer", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, agent.ID, map[string]string{
		"SOUL.md":   "# Developer",
		"ACCESS.md": "- system: jira scope: read,write,comment",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	// The token's shape is what tells the plugin this is a Cloud site: a pair
	// with a colon. project= is the wall around this agent.
	s.secrets.Put(ctx, s.orgID, "jira_url", jira.srv.URL+` project="ACME"`)
	s.secrets.Put(ctx, s.orgID, "jira_token", "covey-bot@acme.example:tok3n")
	s.secrets.Assign(ctx, s.orgID, "jira_url", agent.ID)
	s.secrets.Assign(ctx, s.orgID, "jira_token", agent.ID)

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Work ACME-17",
		`[mock:action jira/get_issue {"issue_key":"ACME-17"}]`+
			` [mock:action jira/assign {"issue_key":"ACME-17","assignee":"me"}]`+
			` [mock:action jira/transition {"issue_key":"ACME-17","to":"In Progress"}]`+
			` [mock:action jira/update_issue {"issue_key":"ACME-17","fields":{"Story Points":3},"add_labels":["backend"]}]`+
			` [mock:action jira/comment {"issue_key":"ACME-17","body":"Fixed in [MR !42](https://gitlab.example/mr/42) — ACME-17 guards the null case."}]`+
			` [mock:action jira/transition {"issue_key":"ACME-17","to":"In Review"}]`+
			` [mock:result ACME-17 handed over]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "jira task done", 20*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	// The recording carries every action with its guard-rail subject. The
	// comment's subject is the external one — that is the distinction a rule
	// would be written against.
	for _, subject := range []string{"jira:get_issue", "jira:assign", "jira:update_issue", "jira:comment_external"} {
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

	// The workflow was walked, not set: two transitions, and the ticket stands
	// where the second one leads.
	if got := jira.status(); got != "In Review" {
		t.Fatalf("status = %q, want In Review", got)
	}
	if !jira.assigned() {
		t.Error("the agent did not take the ticket on")
	}

	// The comment left as a document, not as the literal text of one — the
	// whole reason this plugin is compiled.
	body := jira.lastComment()
	if body == nil {
		t.Fatal("no comment arrived")
	}
	if body["type"] != "doc" {
		t.Fatalf("comment body is not ADF: %#v", body)
	}
	if !strings.Contains(fmt.Sprint(body), "https://gitlab.example/mr/42") {
		t.Errorf("the merge request link did not survive the translation: %#v", body)
	}

	// A custom field was addressed by its NAME and arrived under its id.
	if got := jira.storyPoints(); got != 3.0 {
		t.Errorf("story points = %#v — the field name was not resolved", got)
	}

	// The wall holds inside a run: an issue from another project is refused
	// before a request goes out, and the task fails rather than quietly doing
	// nothing.
	outside, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Foreign project",
		`[mock:action jira/get_issue {"issue_key":"OPS-3"}]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task fails outside the wall", 20*time.Second, func() bool {
		return s.taskState(outside.ID) == backlog.StateFailed
	})

	// The webhook is the other half of the intake: a comment on the ticket
	// becomes work without anybody waiting for the next heartbeat.
	event := []byte(`{"webhookEvent":"comment_created","issue":{"key":"ACME-17",` +
		`"fields":{"summary":"Importer drops rows","project":{"key":"ACME"},"status":{"name":"In Review"}}},` +
		`"comment":{"id":"10500","author":{"displayName":"Dana Reporter"},"body":"The fix works — one question left."}}`)
	postJiraWebhook(t, s, "dev-1", event)
	waitFor(t, "webhook creates a task", 10*time.Second, func() bool {
		var n int
		s.pool.QueryRow(ctx, `SELECT count(*) FROM backlog_tasks
			WHERE agent_id=$1 AND title LIKE 'Jira ACME-17%'`, agent.ID).Scan(&n)
		return n == 1
	})
	// Twice the same event is once the same work: Jira retries a webhook that
	// timed out.
	postJiraWebhook(t, s, "dev-1", event)
	var tasks int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM backlog_tasks
		WHERE agent_id=$1 AND title LIKE 'Jira ACME-17%'`, agent.ID).Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if tasks != 1 {
		t.Fatalf("the retry created a second task (%d)", tasks)
	}

	// Without an ACCESS.md line the broker refuses, activation or not.
	stranger, err := s.registry.Create(ctx, s.orgID, "no-jira", "Without access", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, stranger.ID, map[string]string{
		"SOUL.md": "# No jira access",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	denied, err := s.backlog.Create(ctx, s.orgID, stranger.ID, "Forbidden attempt",
		`[mock:action jira/get_issue {"issue_key":"ACME-17"}]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task fails without access", 20*time.Second, func() bool {
		return s.taskState(denied.ID) == backlog.StateFailed
	})
}

// postJiraWebhook fires a signed Jira event at the intake, the way the site
// itself would: HMAC-SHA256 over the raw body in Atlassian's own header.
func postJiraWebhook(t *testing.T, s *stack, slug string, body []byte) {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(webhookSecret))
	mac.Write(body)
	req, _ := http.NewRequest(http.MethodPost, s.http.URL+"/api/webhooks/jira/"+slug, bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		out, _ := io.ReadAll(resp.Body)
		t.Fatalf("webhook HTTP %d: %s", resp.StatusCode, out)
	}
}

// fakeJira is a Jira Cloud double: one project, one issue, a workflow with
// three moves. It answers only what the plugin actually calls — everything else
// fails the test loudly rather than quietly returning an empty object.
type fakeJira struct {
	t *testing.T

	mu       sync.Mutex
	state    string
	assignee any
	comments []map[string]any
	points   any
	labels   []any

	srv *httptest.Server
}

func newFakeJira(t *testing.T) *fakeJira {
	t.Helper()
	f := &fakeJira{t: t, state: "To Do", labels: []any{"importer"}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeJira) status() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

func (f *fakeJira) assigned() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.assignee != nil
}

func (f *fakeJira) storyPoints() any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.points
}

func (f *fakeJira) lastComment() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.comments) == 0 {
		return nil
	}
	body, _ := f.comments[len(f.comments)-1]["body"].(map[string]any)
	return body
}

func (f *fakeJira) handle(w http.ResponseWriter, r *http.Request) {
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
	path := strings.TrimPrefix(r.URL.Path, "/rest/api/3")

	switch {
	case path == "/myself":
		out(map[string]any{"accountId": "5b10bot", "displayName": "Covey Bot", "emailAddress": "covey-bot@acme.example"})

	case path == "/issue/ACME-17" && r.Method == http.MethodGet:
		out(map[string]any{"key": "ACME-17", "fields": f.fields()})

	case path == "/issue/ACME-17" && r.Method == http.MethodPut:
		if fields, ok := body["fields"].(map[string]any); ok {
			if v, ok := fields["customfield_10016"]; ok {
				f.points = v
			}
		}
		if update, ok := body["update"].(map[string]any); ok {
			if ops, ok := update["labels"].([]any); ok {
				for _, op := range ops {
					if add, ok := op.(map[string]any)["add"]; ok {
						f.labels = append(f.labels, add)
					}
				}
			}
		}
		w.WriteHeader(http.StatusNoContent)

	case path == "/issue/ACME-17/assignee":
		f.assignee = map[string]any{"accountId": "5b10bot", "displayName": "Covey Bot"}
		w.WriteHeader(http.StatusNoContent)

	case path == "/issue/ACME-17/comment" && r.Method == http.MethodPost:
		body["id"] = fmt.Sprint(10100 + len(f.comments))
		f.comments = append(f.comments, body)
		out(map[string]any{"id": body["id"], "body": body["body"], "created": "2026-08-24T10:00:00.000+0000"})

	case path == "/issue/ACME-17/transitions" && r.Method == http.MethodGet:
		out(map[string]any{"transitions": f.offered()})

	case path == "/issue/ACME-17/transitions":
		switch body["transition"].(map[string]any)["id"] {
		case "21":
			f.state = "In Progress"
		case "31":
			f.state = "In Review"
		case "41":
			f.state = "Done"
		}
		w.WriteHeader(http.StatusNoContent)

	case path == "/field":
		out([]map[string]any{
			{"id": "summary", "name": "Summary"},
			{"id": "customfield_10016", "name": "Story Points"},
		})

	default:
		f.t.Errorf("fake jira: unexpected call %s %s", r.Method, r.URL.Path)
		http.Error(w, `{"errorMessages":["no route"]}`, http.StatusNotFound)
	}
}

// fields is the issue as Cloud stores it — the description as a document tree,
// which is what the agent must never get to see raw.
func (f *fakeJira) fields() map[string]any {
	return map[string]any{
		"summary": "Importer drops rows with an empty customer_id",
		"description": map[string]any{"type": "doc", "version": 1, "content": []any{
			map[string]any{"type": "paragraph", "content": []any{
				map[string]any{"type": "text", "text": "The guard for "},
				map[string]any{"type": "text", "text": "customer_id", "marks": []any{map[string]any{"type": "code"}}},
				map[string]any{"type": "text", "text": " is missing."},
			}},
		}},
		"status":            map[string]any{"name": f.state, "statusCategory": map[string]any{"key": "new", "name": f.state}},
		"issuetype":         map[string]any{"name": "Bug"},
		"priority":          map[string]any{"name": "High"},
		"assignee":          f.assignee,
		"reporter":          map[string]any{"displayName": "Dana Reporter"},
		"labels":            f.labels,
		"updated":           "2026-08-24T09:00:00.000+0000",
		"comment":           map[string]any{"total": len(f.comments)},
		"customfield_10016": f.points,
	}
}

// offered is what the workflow allows from where the issue stands — the list is
// a property of the issue, not of the project, which is the whole reason a
// status cannot simply be set.
func (f *fakeJira) offered() []map[string]any {
	all := []map[string]any{
		{"id": "21", "name": "Start Progress", "to": map[string]any{"name": "In Progress"}},
		{"id": "31", "name": "Ready for Review", "to": map[string]any{"name": "In Review"}},
		{"id": "41", "name": "Resolve", "to": map[string]any{"name": "Done"}},
	}
	var offered []map[string]any
	for _, t := range all {
		if t["to"].(map[string]any)["name"] != f.state {
			offered = append(offered, t)
		}
	}
	return offered
}
