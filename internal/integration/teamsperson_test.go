package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"covey/internal/msauth"
	"covey/internal/orchestrator"
	"covey/internal/secrets/sealbox"
	"covey/internal/teamsuser"
	"covey/internal/teamsuser/usersys"

	"covey/internal/agents"
)

// The agent works as the person who wrote (#713): Ada's message runs with
// Ada's token, Bo's next one with Bo's; a writer who has not signed in gets
// the sign-in card and their message is handled afterwards; a token for
// somebody else than the writer binds nothing; and a run no Teams message
// started never gets a person's token.

// fakeEntra answers the on-behalf-of grant and Graph for two people.
type fakeEntra struct {
	srv   *httptest.Server
	mu    sync.Mutex
	posts []string // "auth body"
	// whoami maps an access token to the object id Graph reports.
	whoami map[string]string
}

func newFakeEntra(t *testing.T) *fakeEntra {
	f := &fakeEntra{whoami: map[string]string{
		"ada-access": "oid-ada", "bo-access": "oid-bo", "mallory-access": "oid-ada",
	}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth2/v2.0/token") {
			_ = r.ParseForm()
			person := strings.TrimPrefix(r.Form.Get("assertion"), "sso-")
			if r.Form.Get("requested_token_use") != "on_behalf_of" || person == "" || r.Form.Get("client_id") != "app" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error_description":"consent required"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": person + "-access", "refresh_token": person + "-refresh", "expires_in": 3600})
			return
		}
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if strings.HasPrefix(r.URL.Path, "/v1.0/me") {
			_ = json.NewEncoder(w).Encode(map[string]string{"id": f.whoami[auth], "userPrincipalName": auth + "@example.org"})
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.posts = append(f.posts, auth+" "+string(body))
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"id":"sent"}`))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeEntra) sent(token, text string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.posts {
		if strings.HasPrefix(p, token+" ") && strings.Contains(p, text) {
			return true
		}
	}
	return false
}

func (f *fakeEntra) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.posts)
}

func TestTeamsAgentWorksAsTheWriter(t *testing.T) {
	usersys.Register()
	key, _ := sealbox.GenerateMasterKey()
	box, _ := sealbox.New(key)
	entra := newFakeEntra(t)
	var people *teamsuser.Service
	ts := newTeamsAppStackWith(t, func(s *stack, m map[string]orchestrator.PlatformSystem) {
		people = teamsuser.New(s.pool, box, teamsuser.Config{
			GraphBase: entra.srv.URL + "/v1.0",
			SSO:       &msauth.App{ClientID: "app", Secret: "pw", LoginBase: entra.srv.URL, Purpose: "teamsapp-person"},
		}, nil)
		m[usersys.Name] = people
	})
	ts.svc.People = people
	ts.svc.ActsAsPerson = func(ctx context.Context, a agents.Agent) bool {
		ok, _ := ts.registry.HasAccess(ctx, a.ID, usersys.Name, "")
		return ok
	}
	ts.srv.TeamsUser = people
	ctx := context.Background()
	org := ts.org(ts.orgID, "tenant-a")
	if err := ts.targets.SetEnabled(ctx, org, usersys.Name, true); err != nil {
		t.Fatal(err)
	}
	vera, _ := ts.registry.Create(ctx, org, "vera", "Vera", "mock", nil)
	if _, err := ts.registry.SaveConfig(ctx, vera.ID, map[string]string{
		"SOUL.md":   "# Vera\n\n## Role\nAssist.",
		"ACCESS.md": "- system: teamsapp scope: read,write\n- system: teamsuser scope: read,write",
	}, nil); err != nil {
		t.Fatal(err)
	}

	msg := func(id, oid, text string) map[string]any {
		a := ts.message(id, "tenant-a", "19:group", text, nil)
		a["conversation"] = map[string]any{"id": "19:group", "conversationType": "groupChat", "tenantId": "tenant-a"}
		a["from"] = map[string]any{"id": "29:" + oid, "name": oid, "aadObjectId": oid}
		return a
	}
	exchange := func(oid, token string) int {
		a := map[string]any{"type": "invoke", "name": "signin/tokenExchange", "id": "inv-" + oid, "serviceUrl": ts.conn.srv.URL,
			"from":         map[string]any{"id": "29:" + oid, "aadObjectId": oid},
			"recipient":    map[string]any{"id": "28:app"},
			"conversation": map[string]any{"id": "19:group", "tenantId": "tenant-a"},
			"value":        map[string]any{"id": "x", "connectionName": "covey", "token": token}}
		body, _ := json.Marshal(a)
		req, _ := http.NewRequest(http.MethodPost, ts.http.URL+"/api/webhooks/teamsapp", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer valid")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	action := func(text string) string {
		return `Vera, [mock:action teamsuser/send_chat {"chat_id":"19:group","text":"` + text + `"}]` + "\n[mock:result Done]"
	}

	// Ada writes, has not signed in: no work yet, a sign-in card instead.
	if _, out := ts.deliver(msg("a1", "oid-ada", action("for Ada")), "Bearer valid"); !strings.Contains(out, "sign_in_requested") {
		t.Fatalf("expected a sign-in request, got %s", out)
	}
	if n := len(tasksOf(t, ts.stack, vera.ID)); n != 0 {
		t.Fatalf("work was created before the writer signed in (%d)", n)
	}
	if got := ts.conn.callsTo("/v3/conversations/19:group/activities/a1"); len(got) != 1 {
		t.Fatalf("no sign-in card: %v", ts.conn.calls)
	}

	// A token for somebody else than the writer binds nothing.
	if code := exchange("oid-bo", "sso-mallory"); code != http.StatusPreconditionFailed {
		t.Fatalf("a foreign token was accepted: %d", code)
	}

	// Ada's exchange: her kept message is handled, with her token.
	if code := exchange("oid-ada", "sso-ada"); code != http.StatusOK {
		t.Fatalf("exchange: %d", code)
	}
	waitFor(t, "Ada's message sent with Ada's token", 20*time.Second, func() bool { return entra.sent("ada-access", "for Ada") })

	// Bo writes next, signs in, and his message runs as Bo.
	if _, out := ts.deliver(msg("b1", "oid-bo", action("for Bo")), "Bearer valid"); !strings.Contains(out, "sign_in_requested") {
		t.Fatalf("Bo: %s", out)
	}
	if code := exchange("oid-bo", "sso-bo"); code != http.StatusOK {
		t.Fatalf("Bo's exchange: %d", code)
	}
	waitFor(t, "Bo's message sent with Bo's token", 20*time.Second, func() bool { return entra.sent("bo-access", "for Bo") })
	if entra.sent("ada-access", "for Bo") {
		t.Fatal("Bo's message went out with Ada's token")
	}

	// Ada again, already signed in: straight to work, as Ada.
	if _, out := ts.deliver(msg("a2", "oid-ada", action("again Ada")), "Bearer valid"); !strings.Contains(out, "created") && !strings.Contains(out, "correlated") {
		t.Fatalf("Ada again: %s", out)
	}
	waitFor(t, "Ada's second message", 20*time.Second, func() bool { return entra.sent("ada-access", "again Ada") })

	// A run no Teams message started gets no person's token.
	before := entra.count()
	task, err := ts.backlog.Create(ctx, org, vera.ID, "heartbeat", `[mock:action teamsuser/send_chat {"chat_id":"19:group","text":"from nobody"}]`+"\n[mock:result Done]", "heartbeat", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the heartbeat run ends", 20*time.Second, func() bool {
		st := ts.taskState(task.ID)
		return st == "done" || st == "failed"
	})
	if entra.count() != before {
		t.Fatal("a run without a Teams writer sent with a person's token")
	}
}
