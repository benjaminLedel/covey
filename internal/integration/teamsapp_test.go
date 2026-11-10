package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/backlog"
	"covey/internal/orchestrator"
	"covey/internal/teamsapp"
	"covey/internal/teamsapp/teamsys"
)

// The installation's Teams app (#710): one bot for every organisation. What
// has to hold before it may exist is that the organisations stay apart — in
// both directions, whatever IDs anybody sends.

// fakeConnector is the Bot Connector for the shared bot: it records every
// call with the body and answers what the client expects.
type fakeConnector struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls []string // "METHOD path summary-or-text"
}

func newFakeConnector(t *testing.T) *fakeConnector {
	f := &fakeConnector{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 3600})
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		what := fmt.Sprint(body["summary"])
		if what == "<nil>" {
			what = fmt.Sprint(body["text"])
		}
		f.mu.Lock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path+" "+what)
		f.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v3/teams/"):
			team := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v3/teams/"), "/conversations")
			_ = json.NewEncoder(w).Encode(map[string]any{"conversations": []map[string]any{
				{"id": team}, {"id": "19:leads-" + team, "name": "Leads"},
			}})
		case r.URL.Path == "/v3/conversations":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "19:thread;messageid=7", "activityId": "7"})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "m1"})
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeConnector) callsTo(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if strings.Contains(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

type teamsAppStack struct {
	*stack
	svc  *teamsapp.Service
	conn *fakeConnector
	// extra adds further control-plane systems before the orchestrator starts.
	extra func(*stack, map[string]orchestrator.PlatformSystem)
}

func newTeamsAppStack(t *testing.T) *teamsAppStack {
	return newTeamsAppStackWith(t, nil)
}

func newTeamsAppStackWith(t *testing.T, extra func(*stack, map[string]orchestrator.PlatformSystem)) *teamsAppStack {
	t.Helper()
	teamsys.Register()
	conn := newFakeConnector(t)
	ts := &teamsAppStack{conn: conn, extra: extra}
	ts.stack = newStackWith(t, stackOpts{platform: func(s *stack) map[string]orchestrator.PlatformSystem {
		ts.svc = teamsapp.New(s.pool, s.registry, teamsapp.Config{
			AppID: "app", AppPassword: "pw", TokenURL: conn.srv.URL + "/token",
		}, nil)
		ts.svc.Enabled = func(ctx context.Context, org uuid.UUID) bool {
			_, err := s.targets.System(ctx, org, teamsys.Name)
			return err == nil
		}
		// The token check is the plugin pack's (VerifyActivity, with its own
		// tests); here a fixed bearer stands for a valid one.
		ts.svc.Verify = func(auth, _ string) error {
			if auth != "Bearer valid" {
				return errors.New("bad token")
			}
			return nil
		}
		m := map[string]orchestrator.PlatformSystem{teamsys.Name: ts.svc}
		if ts.extra != nil {
			ts.extra(s, m)
		}
		return m
	}})
	ts.srv.TeamsApp = ts.svc
	return ts
}

// org creates an organisation with the system switched on and a linked
// tenant; the stack's own organisation is passed as orgID to reuse it.
func (ts *teamsAppStack) org(orgID uuid.UUID, tenant string) uuid.UUID {
	ctx := context.Background()
	if orgID == uuid.Nil {
		orgID = uuid.New()
		if _, err := ts.pool.Exec(ctx, `INSERT INTO organizations (id, name) VALUES ($1,'Andere GmbH')`, orgID); err != nil {
			ts.t.Fatal(err)
		}
	}
	if err := ts.targets.SetEnabled(ctx, orgID, teamsys.Name, true); err != nil {
		ts.t.Fatal(err)
	}
	if err := ts.svc.Store.Link(ctx, orgID, tenant, "admin@example.org"); err != nil {
		ts.t.Fatal(err)
	}
	return orgID
}

func (ts *teamsAppStack) agent(orgID uuid.UUID, slug, name string) agents.Agent {
	ts.t.Helper()
	ctx := context.Background()
	a, err := ts.registry.Create(ctx, orgID, slug, name, "mock", nil)
	if err != nil {
		ts.t.Fatal(err)
	}
	if _, err := ts.registry.SaveConfig(ctx, a.ID, map[string]string{
		"SOUL.md":   "# " + name + "\n\n## Role\nTeams.",
		"ACCESS.md": "- system: teamsapp scope: read,write",
	}, nil); err != nil {
		ts.t.Fatal(err)
	}
	return a
}

func (ts *teamsAppStack) deliver(activity map[string]any, auth string) (int, string) {
	ts.t.Helper()
	body, _ := json.Marshal(activity)
	req, _ := http.NewRequest(http.MethodPost, ts.http.URL+"/api/webhooks/teamsapp", bytes.NewReader(body))
	req.Header.Set("Authorization", auth)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		ts.t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, strings.TrimSpace(buf.String())
}

func (ts *teamsAppStack) message(id, tenant, conv, text string, channel map[string]any) map[string]any {
	a := map[string]any{
		"type": "message", "id": id, "text": text, "serviceUrl": ts.conn.srv.URL,
		"from":         map[string]any{"id": "29:user-" + tenant, "name": "Ada"},
		"recipient":    map[string]any{"id": "28:app", "name": "covey"},
		"conversation": map[string]any{"id": conv, "conversationType": "personal", "tenantId": tenant},
	}
	if channel != nil {
		a["conversation"] = map[string]any{"id": conv, "conversationType": "channel", "tenantId": tenant}
		a["channelData"] = channel
	}
	return a
}

func tasksOf(t *testing.T, s *stack, agentID uuid.UUID) []backlog.Task {
	t.Helper()
	list, err := s.backlog.ListByAgent(context.Background(), agentID, true)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

// TestTeamsAppRoutesByTenant: a message reaches only an agent of the
// organisation that linked the sender's tenant; an unlinked tenant reaches
// nobody and is told so; a bad token is refused before anything is read.
func TestTeamsAppRoutesByTenant(t *testing.T) {
	ts := newTeamsAppStack(t)
	orgA := ts.org(ts.orgID, "tenant-a")
	orgB := ts.org(uuid.Nil, "tenant-b")
	vera := ts.agent(orgA, "vera", "Vera Brandt")
	paula := ts.agent(orgA, "paula", "Paula Paulsen")
	ts.ohneLaeufe(vera.ID)
	ts.ohneLaeufe(paula.ID)
	bert := ts.agent(orgB, "vera-b", "Vera Bertram") // same first name, other org
	ts.ohneLaeufe(bert.ID)

	if code, _ := ts.deliver(ts.message("x0", "tenant-a", "19:a", "Vera, hi", nil), "Bearer forged"); code != http.StatusUnauthorized {
		t.Fatalf("a bad token must be refused, got %d", code)
	}

	if code, out := ts.deliver(ts.message("a1", "tenant-a", "19:a", "Vera, can you check the invoice?", nil), "Bearer valid"); code != 200 || !strings.Contains(out, "created") {
		t.Fatalf("named message: %d %s", code, out)
	}
	if n := len(tasksOf(t, ts.stack, vera.ID)); n != 1 {
		t.Fatalf("Vera in org A should have one task, has %d", n)
	}
	if n := len(tasksOf(t, ts.stack, bert.ID)); n != 0 {
		t.Fatalf("an agent of org B received a message from tenant A (%d tasks)", n)
	}
	// The conversation stays with Vera without her name.
	if _, out := ts.deliver(ts.message("a2", "tenant-a", "19:a", "and the second one?", nil), "Bearer valid"); !strings.Contains(out, "created") && !strings.Contains(out, "correlated") {
		t.Fatalf("follow-up: %s", out)
	}
	if n := len(tasksOf(t, ts.stack, paula.ID)); n != 0 {
		t.Fatalf("the follow-up went to another colleague")
	}

	// Two candidates and no name in a new conversation: the bot asks.
	if _, out := ts.deliver(ts.message("a3", "tenant-a", "19:a-new", "hello", nil), "Bearer valid"); !strings.Contains(out, "answered") {
		t.Fatalf("expected the chooser, got %s", out)
	}
	if got := ts.conn.callsTo("19:a-new"); len(got) != 1 || !strings.Contains(got[0], "Which colleague") {
		t.Fatalf("chooser not sent: %v", got)
	}

	// Org B has one candidate: it takes the message, named or not.
	if _, out := ts.deliver(ts.message("b1", "tenant-b", "19:b", "hello", nil), "Bearer valid"); !strings.Contains(out, "created") {
		t.Fatalf("org B: %s", out)
	}
	if n := len(tasksOf(t, ts.stack, bert.ID)); n != 1 {
		t.Fatalf("org B's agent should have its task, has %d", n)
	}

	// An unlinked tenant reaches nobody.
	before := len(tasksOf(t, ts.stack, vera.ID))
	if _, out := ts.deliver(ts.message("c1", "tenant-c", "19:c", "Vera, hi", nil), "Bearer valid"); !strings.Contains(out, "answered") {
		t.Fatalf("unlinked tenant: %s", out)
	}
	if got := ts.conn.callsTo("19:c/"); len(got) != 1 || !strings.Contains(got[0], "not connected") {
		t.Fatalf("unlinked tenant not told: %v", got)
	}
	if n := len(tasksOf(t, ts.stack, vera.ID)); n != before {
		t.Fatalf("an unlinked tenant created work (%d tasks, %d before)", n, before)
	}
}

// TestTeamsAppActionsStayInOrg: an agent can only reach its own
// organisation's conversations and channels, the host comes from the
// records whatever the agent passes, and a personal chat belongs to the
// colleague it is bound to.
func TestTeamsAppActionsStayInOrg(t *testing.T) {
	ts := newTeamsAppStack(t)
	ctx := context.Background()
	orgA := ts.org(ts.orgID, "tenant-a")
	orgB := ts.org(uuid.Nil, "tenant-b")
	vera := ts.agent(orgA, "vera", "Vera")
	paula := ts.agent(orgA, "paula", "Paula")
	ts.ohneLaeufe(vera.ID)
	ts.ohneLaeufe(paula.ID)
	bert := ts.agent(orgB, "bert", "Bert")
	ts.ohneLaeufe(bert.ID)

	team := map[string]any{"tenant": map[string]any{"id": "tenant-a"},
		"team": map[string]any{"id": "19:team-a", "name": "Sales"}, "channel": map[string]any{"id": "19:team-a"}}
	ts.deliver(ts.message("a1", "tenant-a", "19:chat-vera", "Vera, hi", nil), "Bearer valid")
	ts.deliver(ts.message("a2", "tenant-a", "19:team-a;messageid=5", "Paula, hi", team), "Bearer valid")

	// Org B's agent cannot reply into org A's chat, nor post in its channel.
	if _, err := ts.svc.Execute(ctx, bert, uuid.Nil, "reply", json.RawMessage(`{"conversation_id":"19:chat-vera","text":"x"}`)); !errors.Is(err, teamsapp.ErrUnknownConversation) {
		t.Fatalf("org B replied into org A's chat: %v", err)
	}
	if _, err := ts.svc.Execute(ctx, bert, uuid.Nil, "post", json.RawMessage(`{"channel":"Sales/Leads","text":"x"}`)); err == nil {
		t.Fatal("org B posted into org A's channel")
	}
	if _, err := ts.svc.Execute(ctx, bert, uuid.Nil, "post", json.RawMessage(`{"channel":"19:leads-19:team-a","text":"x"}`)); err == nil {
		t.Fatal("org B posted into org A's channel by ID")
	}
	// A colleague in the same org cannot take over Vera's personal chat.
	if _, err := ts.svc.Execute(ctx, paula, uuid.Nil, "reply", json.RawMessage(`{"conversation_id":"19:chat-vera","text":"x"}`)); err == nil {
		t.Fatal("another colleague wrote into Vera's personal chat")
	}

	// Vera's reply goes to the recorded connector, not to the host she names.
	collector := newFakeConnector(t)
	params := fmt.Sprintf(`{"conversation_id":"19:chat-vera","reply_to_activity_id":"a1","service_url":%q,"text":"Done."}`, collector.srv.URL)
	if _, err := ts.svc.Execute(ctx, vera, uuid.Nil, "reply", json.RawMessage(params)); err != nil {
		t.Fatal(err)
	}
	if len(collector.callsTo("/v3/")) != 0 {
		t.Fatal("the reply went to the host the agent passed")
	}
	if got := ts.conn.callsTo("/v3/conversations/19:chat-vera/activities/a1"); len(got) != 1 || !strings.Contains(got[0], "Vera: Done.") {
		t.Fatalf("reply not sent as Vera: %v", got)
	}

	// Posting: listed channels are org A's, a new thread is bound to the poster.
	out, err := ts.svc.Execute(ctx, vera, uuid.Nil, "post", json.RawMessage(`{"channel":"Leads","text":"Daily digest"}`))
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := out.(map[string]string); m["conversation_id"] != "19:thread;messageid=7" {
		t.Fatalf("post result: %#v", out)
	}
	conv, err := ts.svc.Store.Conversation(ctx, orgA, "19:thread;messageid=7")
	if err != nil || conv.AgentID == nil || *conv.AgentID != vera.ID {
		t.Fatalf("posted thread not bound to Vera: %+v %v", conv, err)
	}
	if list, _ := ts.svc.Channels(ctx, orgB); len(list) != 0 {
		t.Fatalf("org B sees org A's channels: %+v", list)
	}
}

// TestTeamsAppAgentRepliesThroughControlPlane is the whole path: a message
// in Teams wakes the agent, its reply action leaves the sandbox as a request
// to the control plane, and the control plane sends it — with ACCESS
// checked there, not only in the daemon.
func TestTeamsAppAgentRepliesThroughControlPlane(t *testing.T) {
	ts := newTeamsAppStack(t)
	orgA := ts.org(ts.orgID, "tenant-a")
	ts.agent(orgA, "vera", "Vera")

	text := `Vera, [mock:action teamsapp/reply {"conversation_id":"19:e2e","reply_to_activity_id":"e1","text":"On it."}]` + "\n[mock:result Done]"
	if _, out := ts.deliver(ts.message("e1", "tenant-a", "19:e2e", text, nil), "Bearer valid"); !strings.Contains(out, "created") {
		t.Fatalf("delivery: %s", out)
	}
	waitFor(t, "the reply reaches the connector", 20*time.Second, func() bool {
		return len(ts.conn.callsTo("/v3/conversations/19:e2e/activities/e1")) == 1
	})
	if got := ts.conn.callsTo("19:e2e/activities/e1"); !strings.Contains(got[0], "Vera: On it.") {
		t.Fatalf("reply: %v", got)
	}

	// Without teamsapp in ACCESS.md the control plane refuses, whatever the
	// daemon decided.
	paula, _ := ts.registry.Create(context.Background(), orgA, "paula", "Paula", "mock", nil)
	_, _ = ts.registry.SaveConfig(context.Background(), paula.ID, map[string]string{
		"SOUL.md": "# Paula\n\n## Role\nx.", "ACCESS.md": "- system: zammad scope: read",
	}, nil)
	id := paula.ID
	_ = ts.svc.Store.Bind(context.Background(), teamsapp.Conversation{TenantID: "tenant-a", ConversationID: "19:p",
		Kind: "personal", AgentID: &id, ServiceURL: ts.conn.srv.URL})
	if _, err := ts.backlog.Create(context.Background(), orgA, paula.ID, "x",
		`[mock:action teamsapp/reply {"conversation_id":"19:p","text":"leak"}]`+"\n[mock:result Done]", "test", 3); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "paula's task ends", 20*time.Second, func() bool {
		for _, tk := range tasksOf(t, ts.stack, paula.ID) {
			if tk.State == backlog.StateDone || tk.State == backlog.StateFailed {
				return true
			}
		}
		return false
	})
	if got := ts.conn.callsTo("19:p"); len(got) != 0 {
		t.Fatalf("an agent without teamsapp access wrote into Teams: %v", got)
	}
}

// TestTeamsAppLinkNeedsBrowserAndAdmin: the tenant comes from the token
// endpoint's ID token, the callback needs the cookie of the browser that
// started the link, only an administrator links, and a tenant held by
// another organisation stays there.
func TestTeamsAppLinkNeedsBrowserAndAdmin(t *testing.T) {
	ts := newTeamsAppStack(t)
	ctx := context.Background()
	var claims map[string]any
	login := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("client_secret") != "pw" || r.Form.Get("code") != "the-code" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error_description":"bad code"}`))
			return
		}
		payload, _ := json.Marshal(claims)
		idt := "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": idt})
	}))
	t.Cleanup(login.Close)
	svc := teamsapp.New(ts.pool, ts.registry, teamsapp.Config{AppID: "app", AppPassword: "pw", LoginBase: login.URL}, nil)

	start := func(org uuid.UUID) (state, nonce string) {
		u, n, err := svc.StartLink(org, "https://covey.example.org/api/teamsapp/callback")
		if err != nil {
			t.Fatal(err)
		}
		pu, _ := url.Parse(u)
		return pu.Query().Get("state"), n
	}
	const admin = "69091246-20e8-4a56-aa4d-066075b2a7a8"

	state, nonce := start(ts.orgID)
	claims = map[string]any{"aud": "app", "tid": "tenant-x", "nonce": nonce, "wids": []string{admin}, "preferred_username": "ada@example.org"}
	if _, _, err := svc.FinishLink(ctx, state, "another-browser", "the-code", "cb"); !errors.Is(err, teamsapp.ErrLinkState) {
		t.Fatalf("a callback without the starting browser's cookie must fail: %v", err)
	}
	if _, _, err := svc.FinishLink(ctx, state+"x", nonce, "the-code", "cb"); !errors.Is(err, teamsapp.ErrLinkState) {
		t.Fatalf("a tampered state must fail: %v", err)
	}

	claims["wids"] = []string{"b79fbf4d-3ef9-4689-8143-76b194e85509"} // a plain member
	if _, _, err := svc.FinishLink(ctx, state, nonce, "the-code", "cb"); !errors.Is(err, teamsapp.ErrNotAdmin) {
		t.Fatalf("a non-admin linked the tenant: %v", err)
	}

	claims["wids"] = []string{admin}
	org, tenant, err := svc.FinishLink(ctx, state, nonce, "the-code", "cb")
	if err != nil || org != ts.orgID || tenant != "tenant-x" {
		t.Fatalf("link: %v %v %v", org, tenant, err)
	}

	other := uuid.New()
	if _, err := ts.pool.Exec(ctx, `INSERT INTO organizations (id, name) VALUES ($1,'Dritte AG')`, other); err != nil {
		t.Fatal(err)
	}
	state2, nonce2 := start(other)
	claims["nonce"] = nonce2
	if _, _, err := svc.FinishLink(ctx, state2, nonce2, "the-code", "cb"); !errors.Is(err, teamsapp.ErrTenantTaken) {
		t.Fatalf("a tenant moved to another organisation: %v", err)
	}
	if got, _ := svc.Store.OrgOfTenant(ctx, "tenant-x"); got != ts.orgID {
		t.Fatalf("tenant now belongs to %v", got)
	}
}
