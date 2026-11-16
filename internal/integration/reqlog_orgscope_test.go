package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/accounts"
	identbuiltin "covey/internal/identity/builtin"
	reqlogstore "covey/internal/reqlog/store"
)

const loggedTriggerWorkflow = `
workflow: Orders
owner: admin
mode: steer
steps:
  t:
    type: trigger
    token: true
    next: e
  e:
    type: end
`

// TestRequestLogStaysInsideItsOrganisation: the request log is one table for
// the whole installation, so every reader is held to its own organisation. A
// workflow's token trigger is stored with the workflow's organisation and
// without its token; another organisation's admin can neither list, read nor
// clear it; an entry without an organisation is the system_admin's alone.
func TestRequestLogStaysInsideItsOrganisation(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	adminA := login(t, s, "admin@test.local", "admin-passwort")
	saveWorkflowAPI(t, adminA, "orders", loggedTriggerWorkflow)
	tok := adminA.expect(http.MethodPost, "/api/v1/workflows/orders/token", nil, http.StatusOK)
	triggerURL := tok["url"].(string)
	token := triggerURL[strings.LastIndex(triggerURL, "/")+1:]
	if code, out := postJSON(t, triggerURL, []byte(`{"case":"A-1","title":"a case","body":"a body"}`), nil); code != http.StatusOK {
		t.Fatalf("trigger: %d %v", code, out)
	}
	// A token nobody owns: rejected, and attributable to no organisation.
	if code, _ := postJSON(t, s.http.URL+"/api/trigger/workflows/unknown-token", []byte(`{}`), nil); code != http.StatusNotFound {
		t.Fatalf("unknown token: %d", code)
	}

	all := reqlogstore.Scope{OrgID: s.orgID, Unattributed: true}
	var entries []reqlogstore.View
	waitFor(t, "request log filled", 10*time.Second, func() bool {
		entries, _ = s.reqlog.List(ctx, all, reqlogstore.Filter{Limit: 100})
		return len(entries) >= 2
	})
	for _, e := range entries {
		if strings.Contains(e.URL, token) || strings.Contains(e.URL, "unknown-token") {
			t.Fatalf("a trigger token was stored in the URL: %q", e.URL)
		}
	}
	fired := findEntry(entries, func(v reqlogstore.View) bool { return v.Status == http.StatusOK })
	unattributed := findEntry(entries, func(v reqlogstore.View) bool { return v.Status == http.StatusNotFound })
	if fired == nil || unattributed == nil {
		t.Fatalf("expected the fired and the rejected trigger: %+v", entries)
	}
	if fired.OrgID == nil || *fired.OrgID != s.orgID {
		t.Fatalf("the workflow trigger is not attributed to its organisation: %+v", fired)
	}
	if fired.URL != "/api/trigger/workflows/***" {
		t.Fatalf("stored URL: %q", fired.URL)
	}
	if unattributed.OrgID != nil {
		t.Fatalf("a rejected token names no organisation: %+v", unattributed)
	}

	// Organisation B, with an org_admin seat of its own.
	orgB := uuid.New()
	if _, err := s.pool.Exec(ctx, `INSERT INTO organizations (id, name) VALUES ($1,'Other Ltd')`, orgB); err != nil {
		t.Fatal(err)
	}
	hash, _ := identbuiltin.HashPassword("b-passwort")
	accB := uuid.New()
	if _, err := s.pool.Exec(ctx, `INSERT INTO accounts (id, email, password_hash, display_name, email_verified_at)
		VALUES ($1,'b@other.example',$2,'B',now())`, accB, hash); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO humans (id, org_id, account_id, email, display_name, password_hash, role)
		VALUES ($1,$2,$3,'b@other.example','B',$4,'org_admin')`, uuid.New(), orgB, accB, hash); err != nil {
		t.Fatal(err)
	}
	adminB := login(t, s, "b@other.example", "b-passwort")

	page := adminB.expect(http.MethodGet, "/api/v1/platform/requests?limit=500", nil, http.StatusOK)
	if got, _ := page["entries"].([]any); len(got) != 0 {
		t.Fatalf("organisation B sees entries that are not its own: %v", got)
	}
	adminB.expect(http.MethodGet, "/api/v1/platform/requests/"+itoa(fired.ID), nil, http.StatusNotFound)
	adminB.expect(http.MethodGet, "/api/v1/platform/requests/"+itoa(unattributed.ID), nil, http.StatusNotFound)
	if del := adminB.expect(http.MethodDelete, "/api/v1/platform/requests", nil, http.StatusOK); del["deleted"] != float64(0) {
		t.Fatalf("organisation B cleared entries that are not its own: %v", del)
	}

	// Organisation A's admin sees its own entry, not the unattributed one.
	adminA.expect(http.MethodGet, "/api/v1/platform/requests/"+itoa(fired.ID), nil, http.StatusOK)
	adminA.expect(http.MethodGet, "/api/v1/platform/requests/"+itoa(unattributed.ID), nil, http.StatusNotFound)

	// As the installation's system_admin the same seat sees it too.
	if err := accounts.New(s.pool).SetPlatformRole(ctx, "admin@test.local", accounts.RoleSystemAdmin); err != nil {
		t.Fatal(err)
	}
	adminA.expect(http.MethodGet, "/api/v1/platform/requests/"+itoa(unattributed.ID), nil, http.StatusOK)
}

// TestRequestLogAttributesTeamsBotTraffic: a Teams bot belongs to one
// organisation, and so does everything that passes through it — the
// activity that came in and the bot's own reply out, also on the branches
// that end before an agent is reached.
func TestRequestLogAttributesTeamsBotTraffic(t *testing.T) {
	ts := newTeamsAppStack(t)
	orgA := ts.org(ts.orgID, "tenant-a")
	ts.ohneLaeufe(ts.agent(orgA, "vera", "Vera Brandt").ID)
	ts.ohneLaeufe(ts.agent(orgA, "paula", "Paula Paulsen").ID)

	// Two candidates, no name: the bot answers itself and no agent is
	// involved.
	if _, out := ts.deliver(orgA, ts.message("a1", "tenant-a", "19:a", "hello", nil), "valid"); !strings.Contains(out, "answered") {
		t.Fatalf("expected the chooser, got %s", out)
	}

	ctx := context.Background()
	var entries []reqlogstore.View
	waitFor(t, "bot traffic logged", 10*time.Second, func() bool {
		entries, _ = ts.reqlog.List(ctx, reqlogstore.Scope{OrgID: orgA}, reqlogstore.Filter{Limit: 100})
		return countDirection(entries, "in") >= 1 && countDirection(entries, "out") >= 1
	})
	unattributed, _ := ts.reqlog.List(ctx, reqlogstore.Scope{OrgID: uuid.New(), Unattributed: true}, reqlogstore.Filter{Limit: 100})
	if len(unattributed) != 0 {
		t.Fatalf("bot traffic stored without an organisation: %+v", unattributed)
	}
}
