package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"covey/internal/llm"
)

// konfigModell plays both turns of #491: the triage, which recognises a
// wish to change the agent's configuration, and the config assistant, which
// drafts it. Which one is asked it tells by the system prompt.
type konfigModell struct {
	mu      sync.Mutex
	takt    string // the HEARTBEAT.md the assistant proposes
	triagen []string
	drafts  int
}

func (m *konfigModell) Name() string { return "test" }

func (m *konfigModell) Complete(_ context.Context, req llm.Request) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if strings.Contains(req.System, "config assistant of the covey platform") {
		m.drafts++
		out, _ := json.Marshal(map[string]any{
			"reply":     "The inbox is checked every two hours instead of every 30 minutes, as asked in the chat.",
			"proposals": []map[string]string{{"file": "HEARTBEAT.md", "content": m.takt}},
		})
		return string(out), nil
	}
	m.triagen = append(m.triagen, req.System)
	return `{"action":"config","title":"Check the inbox every two hours","change":"HEARTBEAT.md: the inbox entry every 2h instead of every 30m.","text":"Drafted it — {approvers} can accept it."}`, nil
}

func (m *konfigModell) angeboten() (angeboten bool, n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.triagen {
		angeboten = angeboten || strings.Contains(s, `"action":"config"`)
	}
	return angeboten, len(m.triagen)
}

const taktAlt = "- alle: 30m titel: Inbox aufgabe: Look at the new tickets."
const taktNeu = "- alle: 2h titel: Inbox aufgabe: Look at the new tickets."

// threadRoh reads the per-agent thread's entries as they come.
func threadRoh(t *testing.T, c *apiClient, agentID uuid.UUID) []map[string]any {
	t.Helper()
	v := c.expect(http.MethodGet, "/api/v1/agents/"+agentID.String()+"/thread", nil, http.StatusOK)
	var out []map[string]any
	for _, e := range v["entries"].([]any) {
		out = append(out, e.(map[string]any))
	}
	return out
}

// karte waits for the proposal card in the reader's thread and returns it.
func karte(t *testing.T, c *apiClient, agentID uuid.UUID) map[string]any {
	t.Helper()
	var k map[string]any
	wartenAuf(t, "the proposal card stands in the conversation", func() bool {
		for _, e := range threadRoh(t, c, agentID) {
			if e["kind"] == "config_proposal" {
				k, _ = e["proposal"].(map[string]any)
			}
		}
		return k != nil
	})
	return k
}

func konfigAgent(t *testing.T, s *stack, slug string) (uuid.UUID, *konfigModell) {
	t.Helper()
	agent := s.newSupportAgent(slug)
	s.ohneLaeufe(agent.ID)
	if _, err := s.registry.SaveConfig(context.Background(), agent.ID, map[string]string{
		"SOUL.md":      "# Support-Agent\n\n## Role\nSupport.",
		"ACCESS.md":    "- system: zammad scope: read,write",
		"HEARTBEAT.md": taktAlt,
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	modell := &konfigModell{takt: taktNeu}
	s.srv.OrgLLM = func(context.Context, uuid.UUID) (llm.Provider, error) { return modell, nil }
	return agent.ID, modell
}

func heartbeat(t *testing.T, c *apiClient, agentID uuid.UUID) (string, float64) {
	t.Helper()
	cv := c.expect(http.MethodGet, "/api/v1/agents/"+agentID.String()+"/config", nil, http.StatusOK)
	return cv["files"].(map[string]any)["HEARTBEAT.md"].(string), cv["version"].(float64)
}

// TestConfigProposalsAreOffByDefault (#491): without the setting nothing
// changes — the triage is not offered the choice, and a wish to change the
// heartbeat becomes a task as before, with no proposal stored.
func TestConfigProposalsAreOffByDefault(t *testing.T) {
	s := newStack(t)
	agentID, modell := konfigAgent(t, s, "konfig-aus")
	admin := teamLogin(t, s)
	admin.expect(http.MethodPatch, "/api/v1/org/chat-triage", map[string]any{"mode": "on"}, http.StatusOK)
	if got := admin.expect(http.MethodGet, "/api/v1/org/chat-config-proposals", nil, http.StatusOK); got["enabled"] != false {
		t.Fatalf("the trial must be off by default: %v", got)
	}

	admin.expect(http.MethodPost, "/api/v1/agents/"+agentID.String()+"/messages",
		map[string]any{"text": "Please check the inbox only every two hours."}, http.StatusAccepted)
	wartenAuf(t, "the message became a task", func() bool {
		for _, e := range threadRoh(t, admin, agentID) {
			if e["kind"] == "message" && e["task_id"] != nil {
				return true
			}
		}
		return false
	})
	if angeboten, n := modell.angeboten(); angeboten || n == 0 {
		t.Fatalf("with the trial off the triage must not be offered config (offered %v, %d turns)", angeboten, n)
	}
	modell.mu.Lock()
	drafts := modell.drafts
	modell.mu.Unlock()
	if drafts != 0 {
		t.Fatal("nothing must be drafted with the trial off")
	}
	if items := admin.expectList(http.MethodGet, "/api/v1/improvements?kind=proposal", nil, http.StatusOK); len(items) != 0 {
		t.Fatalf("no proposal may be stored: %v", items)
	}
	if takt, _ := heartbeat(t, admin, agentID); takt != taktAlt {
		t.Fatalf("the heartbeat changed: %q", takt)
	}
}

// TestAConfigWishInTheChatBecomesAProposal walks #491 end to end: with the
// setting on, a wish is drafted and stored as a proposal that is not in
// effect, the conversation shows it as a card and says who may accept it,
// and accepting it on the card is accepting it on the agent page — a new
// version whose HEARTBEAT.md GET config shows. A second one declined changes
// nothing.
func TestAConfigWishInTheChatBecomesAProposal(t *testing.T) {
	s := newStack(t)
	agentID, modell := konfigAgent(t, s, "konfig-an")
	admin := teamLogin(t, s)
	admin.expect(http.MethodPatch, "/api/v1/org/chat-triage", map[string]any{"mode": "on"}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/org/chat-config-proposals", map[string]any{}, http.StatusBadRequest)
	admin.expect(http.MethodPatch, "/api/v1/org/chat-config-proposals", map[string]any{"enabled": true}, http.StatusOK)
	_, vorher := heartbeat(t, admin, agentID)

	admin.expect(http.MethodPost, "/api/v1/agents/"+agentID.String()+"/messages",
		map[string]any{"text": "Please check the inbox only every two hours."}, http.StatusAccepted)
	k := karte(t, admin, agentID)
	if k["status"] != "pending" || k["can_decide"] != true || k["can_accept"] != true {
		t.Fatalf("the owner may decide the card: %v", k)
	}
	diff := k["diff"].([]any)
	if len(diff) != 1 || diff[0].(map[string]any)["file"] != "HEARTBEAT.md" || diff[0].(map[string]any)["after"] != taktNeu {
		t.Fatalf("the card carries the per-file diff: %v", diff)
	}
	if w, _ := k["widens"].([]any); len(w) != 0 {
		t.Fatalf("a heartbeat change widens no access: %v", w)
	}
	// Not in effect before somebody accepts it.
	if takt, v := heartbeat(t, admin, agentID); takt != taktAlt || v != vorher {
		t.Fatalf("a proposal must not be in effect: %q v%v", takt, v)
	}
	// The agent's line names who may accept.
	var zeile string
	for _, e := range threadRoh(t, admin, agentID) {
		if e["kind"] == "answer" && strings.Contains(e["text"].(string), "can accept") {
			zeile = e["text"].(string)
		}
	}
	if !strings.Contains(zeile, "Admin") || strings.Contains(zeile, "{approvers}") {
		t.Fatalf("the reply names who may accept: %q", zeile)
	}
	// The same open point the agent page shows, with its origin.
	items := admin.expectList(http.MethodGet, "/api/v1/improvements?kind=proposal&status=pending", nil, http.StatusOK)
	if len(items) != 1 || items[0]["origin_message_id"] == nil || items[0]["requested_by_name"] != "Admin" {
		t.Fatalf("the proposal is an open point with its origin: %v", items)
	}

	admin.expect(http.MethodPost, "/api/v1/improvements/"+k["id"].(string)+"/decide", map[string]any{"accept": true}, http.StatusOK)
	takt, nachher := heartbeat(t, admin, agentID)
	if takt != taktNeu || nachher != vorher+1 {
		t.Fatalf("accepting puts the change in effect as a new version: %q v%v (was v%v)", takt, nachher, vorher)
	}
	k = karte(t, admin, agentID)
	if k["status"] != "accepted" || k["decided_by"] != "Admin" || k["decided_at"] == nil || k["can_decide"] != false {
		t.Fatalf("the card says who decided and when: %v", k)
	}

	// A draft that changes nothing is answered plainly, and nothing is stored.
	admin.expect(http.MethodPost, "/api/v1/agents/"+agentID.String()+"/messages",
		map[string]any{"text": "Every two hours, please."}, http.StatusAccepted)
	wartenAuf(t, "the failed draft is answered", func() bool {
		return enthaelt(eintraege(t, admin, agentID), "the draft changed nothing")
	})
	if items := admin.expectList(http.MethodGet, "/api/v1/improvements?kind=proposal&status=pending", nil, http.StatusOK); len(items) != 0 {
		t.Fatalf("a failed draft stores nothing: %v", items)
	}

	// A second wish, declined: nothing changes.
	modell.mu.Lock()
	modell.takt = taktAlt
	modell.mu.Unlock()
	admin.expect(http.MethodPost, "/api/v1/agents/"+agentID.String()+"/messages",
		map[string]any{"text": "Actually, go back to every 30 minutes."}, http.StatusAccepted)
	var zweite string
	wartenAuf(t, "a second card", func() bool {
		for _, e := range threadRoh(t, admin, agentID) {
			if p, _ := e["proposal"].(map[string]any); p != nil && p["status"] == "pending" {
				zweite = p["id"].(string)
			}
		}
		return zweite != ""
	})
	admin.expect(http.MethodPost, "/api/v1/improvements/"+zweite+"/decide", map[string]any{"accept": false}, http.StatusOK)
	if t2, v2 := heartbeat(t, admin, agentID); t2 != taktNeu || v2 != nachher {
		t.Fatalf("declining changes nothing: %q v%v", t2, v2)
	}
}

// TestAConfigWishWithoutRightsWaits (#491): somebody whose seat may not
// change an agent can still ask for it — the proposal is drafted, the card
// says it waits and for whom, and the decide route refuses them. The owner
// accepts it from the list the agent page reads.
func TestAConfigWishWithoutRightsWaits(t *testing.T) {
	s := newStack(t)
	agentID, _ := konfigAgent(t, s, "konfig-ohne")
	admin := teamLogin(t, s)
	admin.expect(http.MethodPatch, "/api/v1/org/chat-triage", map[string]any{"mode": "on"}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/org/chat-config-proposals", map[string]any{"enabled": true}, http.StatusOK)
	s.mitglied(t, "ada@test.local", "Ada", "auditor", "ada-passwort")
	ada := login(t, s, "ada@test.local", "ada-passwort")
	// A viewer may not switch the trial.
	ada.expect(http.MethodPatch, "/api/v1/org/chat-config-proposals", map[string]any{"enabled": false}, http.StatusForbidden)

	ada.expect(http.MethodPost, "/api/v1/agents/"+agentID.String()+"/messages",
		map[string]any{"text": "Could you check the inbox only every two hours?"}, http.StatusAccepted)
	k := karte(t, ada, agentID)
	if k["can_decide"] != false || k["can_accept"] != false || k["status"] != "pending" {
		t.Fatalf("a viewer's card waits: %v", k)
	}
	if a, _ := k["approvers"].([]any); len(a) == 0 || a[0] != "Admin" {
		t.Fatalf("the card names who may accept: %v", k["approvers"])
	}
	ada.expect(http.MethodPost, "/api/v1/improvements/"+k["id"].(string)+"/decide", map[string]any{"accept": true}, http.StatusForbidden)
	if takt, _ := heartbeat(t, admin, agentID); takt != taktAlt {
		t.Fatalf("a refused accept changes nothing: %q", takt)
	}

	admin.expect(http.MethodPost, "/api/v1/improvements/"+k["id"].(string)+"/decide", map[string]any{"accept": true}, http.StatusOK)
	if takt, _ := heartbeat(t, admin, agentID); takt != taktNeu {
		t.Fatalf("the owner's accept puts it in effect: %q", takt)
	}
	if k := karte(t, ada, agentID); k["status"] != "accepted" || k["decided_by"] != "Admin" {
		t.Fatalf("the requester's card says who decided: %v", k)
	}
}
