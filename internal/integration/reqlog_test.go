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

// TestRequestLogTeams ist der Durchstich des Request-Logs (spec/06) am
// Teams-Zielsystem: der eingehende Webhook wird protokolliert (auch der
// abgelehnte), der ausgehende Bot-Connector-Call der Sandbox ebenso — samt
// Agenten- und Aufgabenbezug. Beides ist über die Plattform-API sichtbar.
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

	// 1) Abgelehnter Webhook: unbekannter Slug. Genau dieser Fall hinterließ
	//    bisher keine Spur — jetzt steht er mit Status 404 im Log.
	req, _ := http.NewRequest(http.MethodPost, s.http.URL+"/api/webhooks/teams/gibtsnicht",
		strings.NewReader(`{"type":"message","text":"hallo","client_secret":"streng-geheim"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unbekannter slug: HTTP %d (erwartet 404)", resp.StatusCode)
	}

	// 2) Echter Lauf: Webhook → Aufgabe → der Agent antwortet über den Connector.
	svc := teams.srv.URL
	text := fmt.Sprintf(
		"[mock:action teams/reply {\"service_url\":\"%s\",\"conversation_id\":\"19:c\",\"reply_to_activity_id\":\"a1\",\"text\":\"Alles klar\"}]\n"+
			"[mock:result Erledigt]", svc)
	activity := teamsActivity("a1", "19:c", "29:user", "28:bot", text, nil)
	if out := postTeamsWebhook(t, s, "teams-log", activity); out != `{"outcome":"created"}` {
		t.Fatalf("webhook muss eine Aufgabe anlegen, got %s", out)
	}
	waitFor(t, "aufgabe done", 15*time.Second, func() bool {
		tasks, _ := s.backlog.ListByAgent(ctx, agent.ID, false)
		for _, task := range tasks {
			if task.State == backlog.StateDone {
				return true
			}
		}
		return false
	})

	// Der Store schreibt asynchron — auf die erwarteten Einträge warten.
	var entries []reqlogstore.View
	waitFor(t, "request-log gefüllt", 10*time.Second, func() bool {
		entries, _ = s.reqlog.List(ctx, s.orgID, reqlogstore.Filter{Limit: 200})
		return countDirection(entries, "in") >= 2 && countDirection(entries, "out") >= 1
	})

	// Eingehend: der abgelehnte Webhook, mit redigiertem Secret im Body.
	rejected := findEntry(entries, func(v reqlogstore.View) bool {
		return v.Direction == "in" && v.Status == http.StatusNotFound
	})
	if rejected == nil {
		t.Fatalf("abgelehnter webhook fehlt im log: %+v", entries)
	}
	if rejected.System != "teams" {
		t.Fatalf("system falsch: %q", rejected.System)
	}
	detail, err := s.reqlog.Get(ctx, s.orgID, rejected.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(detail.ReqBody, "streng-geheim") {
		t.Fatalf("secret nicht redigiert: %q", detail.ReqBody)
	}
	if !strings.Contains(detail.RespBody, "kein agent mit slug") {
		t.Fatalf("antwort fehlt: %q", detail.RespBody)
	}

	// Eingehend: der angenommene Webhook trägt den aufgelösten Agenten.
	accepted := findEntry(entries, func(v reqlogstore.View) bool {
		return v.Direction == "in" && v.Status == http.StatusOK
	})
	if accepted == nil || accepted.AgentSlug != "teams-log" {
		t.Fatalf("angenommener webhook ohne agentenbezug: %+v", accepted)
	}

	// Ausgehend: der Connector-Call aus der Sandbox — mit Agent und Aufgabe.
	out := findEntry(entries, func(v reqlogstore.View) bool {
		return v.Direction == "out" && v.System == "teams" && strings.Contains(v.URL, "/activities")
	})
	if out == nil {
		t.Fatalf("connector-call fehlt im log: %+v", entries)
	}
	if out.AgentID == nil || out.TaskID == nil {
		t.Fatalf("ausgehender request ohne agent/aufgabe: %+v", out)
	}
	if out.Status != http.StatusOK || out.DurationMS < 0 {
		t.Fatalf("ausgehender request: status=%d dauer=%d", out.Status, out.DurationMS)
	}

	// 3) Sichtbar über die Plattform-API (Rolle platform_admin).
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
		t.Fatalf("API liefert keine einträge: %s", buf.String())
	}
	for _, e := range page.Entries {
		if e.System != "teams" {
			t.Fatalf("system-filter greift nicht: %+v", e)
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
