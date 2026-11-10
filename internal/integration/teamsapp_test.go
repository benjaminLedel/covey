package integration

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/backlog"
	"covey/internal/orchestrator"
	"covey/internal/secrets/sealbox"
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
	box  *sealbox.Box
	conn *fakeConnector
	bots map[uuid.UUID]teamsapp.Bot
	// extra adds further control-plane systems before the orchestrator starts.
	extra func(*teamsAppStack, *stack, map[string]orchestrator.PlatformSystem)
}

func newTeamsAppStack(t *testing.T) *teamsAppStack {
	return newTeamsAppStackWith(t, nil)
}

func newTeamsAppStackWith(t *testing.T, extra func(*teamsAppStack, *stack, map[string]orchestrator.PlatformSystem)) *teamsAppStack {
	t.Helper()
	teamsys.Register()
	conn := newFakeConnector(t)
	ts := &teamsAppStack{conn: conn, extra: extra}
	ts.stack = newStackWith(t, stackOpts{platform: func(s *stack) map[string]orchestrator.PlatformSystem {
		key, _ := sealbox.GenerateMasterKey()
		box, _ := sealbox.New(key)
		ts.box = box
		ts.svc = teamsapp.New(s.pool, s.registry, box, nil)
		ts.svc.TokenURL = conn.srv.URL + "/token"
		ts.svc.Enabled = func(ctx context.Context, org uuid.UUID) bool {
			_, err := s.targets.System(ctx, org, teamsys.Name)
			return err == nil
		}
		// Microsoft's answer at saving: a secret "wrong" is refused.
		ts.svc.Probe = func(_ context.Context, tenant, appID, secret string) error {
			if secret == "wrong" {
				return errors.New("invalid_client")
			}
			return nil
		}
		// The token check is the plugin pack's (VerifyActivity, with its own
		// tests); here "Bearer valid-<app id>" stands for a valid one.
		ts.svc.Verify = func(appID, auth, _ string) error {
			if auth != "Bearer valid-"+appID {
				return errors.New("bad token")
			}
			return nil
		}
		m := map[string]orchestrator.PlatformSystem{teamsys.Name: ts.svc}
		if ts.extra != nil {
			ts.extra(ts, s, m)
		}
		return m
	}})
	ts.srv.TeamsApp = ts.svc
	return ts
}

// org creates an organisation (or reuses orgID) with the system switched on
// and a bot serving tenant.
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
	b, err := ts.svc.SaveBot(ctx, orgID, teamsapp.BotInput{Name: "covey", AppID: uuid.NewString(), TenantID: tenant, Secret: "s3cret-" + tenant})
	if err != nil {
		ts.t.Fatal(err)
	}
	if ts.bots == nil {
		ts.bots = map[uuid.UUID]teamsapp.Bot{}
	}
	ts.bots[orgID] = b
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

// deliver posts an activity to org's bot endpoint; auth "valid" stands for a
// valid token of that bot.
func (ts *teamsAppStack) deliver(org uuid.UUID, activity map[string]any, auth string) (int, string) {
	ts.t.Helper()
	b := ts.bots[org]
	if auth == "valid" {
		auth = "Bearer valid-" + b.AppID
	}
	body, _ := json.Marshal(activity)
	req, _ := http.NewRequest(http.MethodPost, ts.http.URL+"/api/webhooks/teamsapp/"+b.ID.String(), bytes.NewReader(body))
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

	if code, _ := ts.deliver(orgA, ts.message("x0", "tenant-a", "19:a", "Vera, hi", nil), "Bearer forged"); code != http.StatusUnauthorized {
		t.Fatalf("a bad token must be refused, got %d", code)
	}

	if code, out := ts.deliver(orgA, ts.message("a1", "tenant-a", "19:a", "Vera, can you check the invoice?", nil), "valid"); code != 200 || !strings.Contains(out, "created") {
		t.Fatalf("named message: %d %s", code, out)
	}
	if n := len(tasksOf(t, ts.stack, vera.ID)); n != 1 {
		t.Fatalf("Vera in org A should have one task, has %d", n)
	}
	if n := len(tasksOf(t, ts.stack, bert.ID)); n != 0 {
		t.Fatalf("an agent of org B received a message from tenant A (%d tasks)", n)
	}
	// The conversation stays with Vera without her name.
	if _, out := ts.deliver(orgA, ts.message("a2", "tenant-a", "19:a", "and the second one?", nil), "valid"); !strings.Contains(out, "created") && !strings.Contains(out, "correlated") {
		t.Fatalf("follow-up: %s", out)
	}
	if n := len(tasksOf(t, ts.stack, paula.ID)); n != 0 {
		t.Fatalf("the follow-up went to another colleague")
	}

	// Two candidates and no name in a new conversation: the bot asks.
	if _, out := ts.deliver(orgA, ts.message("a3", "tenant-a", "19:a-new", "hello", nil), "valid"); !strings.Contains(out, "answered") {
		t.Fatalf("expected the chooser, got %s", out)
	}
	if got := ts.conn.callsTo("19:a-new"); len(got) != 1 || !strings.Contains(got[0], "Which colleague") {
		t.Fatalf("chooser not sent: %v", got)
	}

	// Org B has one candidate: it takes the message, named or not.
	if _, out := ts.deliver(orgB, ts.message("b1", "tenant-b", "19:b", "hello", nil), "valid"); !strings.Contains(out, "created") {
		t.Fatalf("org B: %s", out)
	}
	if n := len(tasksOf(t, ts.stack, bert.ID)); n != 1 {
		t.Fatalf("org B's agent should have its task, has %d", n)
	}

	// A message from another tenant than the bot's reaches nobody.
	before := len(tasksOf(t, ts.stack, vera.ID))
	if _, out := ts.deliver(orgA, ts.message("c1", "tenant-c", "19:c", "Vera, hi", nil), "valid"); !strings.Contains(out, "answered") {
		t.Fatalf("unlinked tenant: %s", out)
	}
	if got := ts.conn.callsTo("19:c/"); len(got) != 1 || !strings.Contains(got[0], "another Microsoft 365") {
		t.Fatalf("foreign tenant not told: %v", got)
	}
	// And bot B's valid token does not open bot A's endpoint.
	if code, _ := ts.deliver(orgA, ts.message("x1", "tenant-a", "19:a", "Vera, hi", nil), "Bearer valid-"+ts.bots[orgB].AppID); code != http.StatusUnauthorized {
		t.Fatalf("another bot's token opened the endpoint: %d", code)
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
	ts.deliver(orgA, ts.message("a1", "tenant-a", "19:chat-vera", "Vera, hi", nil), "valid")
	ts.deliver(orgA, ts.message("a2", "tenant-a", "19:team-a;messageid=5", "Paula, hi", team), "valid")

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
	conv, err := ts.svc.Store.Conversation(ctx, ts.bots[orgA].ID, "19:thread;messageid=7")
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
	if _, out := ts.deliver(orgA, ts.message("e1", "tenant-a", "19:e2e", text, nil), "valid"); !strings.Contains(out, "created") {
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
	_ = ts.svc.Store.Bind(context.Background(), teamsapp.Conversation{BotID: ts.bots[orgA].ID, ConversationID: "19:p",
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

// TestTeamsBotConfiguration: an organisation configures its bot in the
// interface; Microsoft's refusal of the credentials stops the save, an app ID
// belongs to one organisation, and the secret never comes back out.
func TestTeamsBotConfiguration(t *testing.T) {
	ts := newTeamsAppStack(t)
	ctx := context.Background()
	appID := uuid.NewString()

	if _, err := ts.svc.SaveBot(ctx, ts.orgID, teamsapp.BotInput{AppID: appID, TenantID: "tenant-a", Secret: "wrong"}); err == nil {
		t.Fatal("credentials Microsoft refused were saved")
	}
	b, err := ts.svc.SaveBot(ctx, ts.orgID, teamsapp.BotInput{Name: "Helpdesk", AppID: appID, TenantID: "tenant-a", Secret: "right"})
	if err != nil {
		t.Fatal(err)
	}
	// Updating without a secret keeps the stored one.
	if _, err := ts.svc.SaveBot(ctx, ts.orgID, teamsapp.BotInput{Name: "Helpdesk 2", AppID: appID, TenantID: "tenant-a"}); err != nil {
		t.Fatalf("update without secret: %v", err)
	}
	other := uuid.New()
	if _, err := ts.pool.Exec(ctx, `INSERT INTO organizations (id, name) VALUES ($1,'Dritte AG')`, other); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.svc.SaveBot(ctx, other, teamsapp.BotInput{AppID: appID, TenantID: "tenant-a", Secret: "right"}); !errors.Is(err, teamsapp.ErrAppTaken) {
		t.Fatalf("a second organisation took the app ID: %v", err)
	}
	// Sealed at rest, and absent from what the interface gets.
	var ct []byte
	_ = ts.pool.QueryRow(ctx, `SELECT secret_ct FROM teamsapp_bots WHERE id = $1`, b.ID).Scan(&ct)
	if len(ct) == 0 || strings.Contains(string(ct), "right") {
		t.Fatal("the secret is not sealed")
	}
	raw, _ := json.Marshal(b)
	if strings.Contains(string(raw), "right") {
		t.Fatalf("the secret leaks into the bot's JSON: %s", raw)
	}
	pkg, err := ts.svc.Package(b, "https://covey.example.org")
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(pkg), int64(len(pkg)))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		ID   string `json:"id"`
		Bots []struct {
			BotID string `json:"botId"`
		} `json:"bots"`
		Web struct {
			Resource string `json:"resource"`
		} `json:"webApplicationInfo"`
	}
	for _, f := range zr.File {
		if f.Name == "manifest.json" {
			rc, _ := f.Open()
			_ = json.NewDecoder(rc).Decode(&manifest)
			rc.Close()
		}
	}
	if manifest.ID != appID || len(manifest.Bots) != 1 || manifest.Bots[0].BotID != appID ||
		manifest.Web.Resource != "api://covey.example.org/botid-"+appID {
		t.Fatalf("package manifest: %+v", manifest)
	}
	if err := ts.svc.DeleteBot(ctx, ts.orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.svc.BotOfOrg(ctx, ts.orgID); !errors.Is(err, teamsapp.ErrNoBot) {
		t.Fatalf("bot survived delete: %v", err)
	}
}
