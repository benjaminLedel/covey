package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"covey/internal/backlog"
	reqlogstore "covey/internal/reqlog/store"
)

// TestRequestLogTeams is the vertical slice of the request log (spec/06) on the
// Teams target system: the incoming webhook is logged (the rejected one as
// well), and so is the sandbox's outgoing bot-connector call — including the
// agent and task reference. Both are visible through the platform API.
func TestRequestLogTeams(t *testing.T) {
	s := newStack(t)
	teams := newFakeTeams(t)
	ctx := context.Background()

	if err := s.secrets.Put(ctx, s.orgID, "teams_token", "bot-app-id:bot-app-secret"); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.Put(ctx, s.orgID, "teams_url", teams.srv.URL+"/token"); err != nil {
		t.Fatal(err)
	}
	agent := s.newTeamsAgent("teams-log")
	s.secrets.Assign(ctx, s.orgID, "teams_token", agent.ID)
	s.secrets.Assign(ctx, s.orgID, "teams_url", agent.ID)

	// 1) Rejected webhook: unknown slug. Exactly this case used to leave no
	//    trace — now it is in the log with status 404.
	req, _ := http.NewRequest(http.MethodPost, s.http.URL+"/api/webhooks/teams/gibtsnicht",
		strings.NewReader(`{"type":"message","text":"hallo","client_secret":"streng-geheim"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown slug: HTTP %d (expected 404)", resp.StatusCode)
	}

	// 2) Real run: webhook → task → the agent answers through the connector.
	svc := teams.srv.URL
	text := fmt.Sprintf(
		"[mock:action teams/reply {\"service_url\":\"%s\",\"conversation_id\":\"19:c\",\"reply_to_activity_id\":\"a1\",\"text\":\"All right\"}]\n"+
			"[mock:result Done]", svc)
	activity := teamsActivity("a1", "19:c", "29:user", "28:bot", text, nil)
	if out := postTeamsWebhook(t, s, "teams-log", activity); out != `{"outcome":"created"}` {
		t.Fatalf("the webhook has to create a task, got %s", out)
	}
	waitFor(t, "task done", 15*time.Second, func() bool {
		tasks, _ := s.backlog.ListByAgent(ctx, agent.ID, false)
		for _, task := range tasks {
			if task.State == backlog.StateDone {
				return true
			}
		}
		return false
	})

	// The store writes asynchronously — wait for the expected entries.
	var entries []reqlogstore.View
	waitFor(t, "request log filled", 10*time.Second, func() bool {
		entries, _ = s.reqlog.List(ctx, s.orgID, reqlogstore.Filter{Limit: 200})
		return countDirection(entries, "in") >= 2 && countDirection(entries, "out") >= 1
	})

	// Incoming: the rejected webhook, with the secret redacted in the body.
	rejected := findEntry(entries, func(v reqlogstore.View) bool {
		return v.Direction == "in" && v.Status == http.StatusNotFound
	})
	if rejected == nil {
		t.Fatalf("the rejected webhook is missing from the log: %+v", entries)
	}
	if rejected.System != "teams" {
		t.Fatalf("wrong system: %q", rejected.System)
	}
	detail, err := s.reqlog.Get(ctx, s.orgID, rejected.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(detail.ReqBody, "streng-geheim") {
		t.Fatalf("the secret was not redacted: %q", detail.ReqBody)
	}
	if !strings.Contains(detail.RespBody, "no agent with slug") {
		t.Fatalf("the response is missing: %q", detail.RespBody)
	}

	// Incoming: the accepted webhook carries the resolved agent.
	accepted := findEntry(entries, func(v reqlogstore.View) bool {
		return v.Direction == "in" && v.Status == http.StatusOK
	})
	if accepted == nil || accepted.AgentSlug != "teams-log" {
		t.Fatalf("the accepted webhook has no agent reference: %+v", accepted)
	}

	// Outgoing: the connector call from the sandbox — with agent and task.
	out := findEntry(entries, func(v reqlogstore.View) bool {
		return v.Direction == "out" && v.System == "teams" && strings.Contains(v.URL, "/activities")
	})
	if out == nil {
		t.Fatalf("the connector call is missing from the log: %+v", entries)
	}
	if out.AgentID == nil || out.TaskID == nil {
		t.Fatalf("outgoing request without agent/task: %+v", out)
	}
	if out.Status != http.StatusOK || out.DurationMS < 0 {
		t.Fatalf("outgoing request: status=%d duration=%d", out.Status, out.DurationMS)
	}

	// 3) Visible through the platform API (role org_admin).
	c := login(t, s, "admin@test.local", "admin-passwort")
	resp = c.do(http.MethodGet, "/api/v1/platform/requests?system=teams&limit=50", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("API HTTP %d", resp.StatusCode)
	}
	var page struct {
		Enabled bool               `json:"enabled"`
		Systems []string           `json:"systems"`
		Entries []reqlogstore.View `json:"entries"`
		Bodies  bool               `json:"bodies"`
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	if err := json.Unmarshal(buf.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if !page.Enabled || len(page.Entries) == 0 {
		t.Fatalf("the API returns no entries: %s", buf.String())
	}
	for _, e := range page.Entries {
		if e.System != "teams" {
			t.Fatalf("the system filter does not take effect: %+v", e)
		}
	}
}

func countDirection(entries []reqlogstore.View, dir string) int {
	n := 0
	for _, e := range entries {
		if e.Direction == dir {
			n++
		}
	}
	return n
}

func findEntry(entries []reqlogstore.View, match func(reqlogstore.View) bool) *reqlogstore.View {
	for i := range entries {
		if match(entries[i]) {
			return &entries[i]
		}
	}
	return nil
}
