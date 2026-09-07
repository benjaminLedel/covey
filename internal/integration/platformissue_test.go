package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/benjaminLedel/covey-plugin-sdk/target"

	"covey/internal/agents"
)

// trackerTestSystem is a target system that takes issues. It records what it
// was given, so the test can check that the platform — and not the agent —
// picked the destination and brought the credential.
type trackerTestSystem struct{}

var trackerState struct {
	sync.Mutex
	filed []trackerIssue
}

type trackerIssue struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Repo        string `json:"repo"`
	Token       string
}

func init() {
	target.Register(target.Descriptor{
		Name: "trackertest", Label: "Trackertest", Kind: "builtin", BaseURLOptional: true,
		System: trackerTestSystem{},
	})
}

func (trackerTestSystem) Name() string { return "trackertest" }
func (trackerTestSystem) ActionSubject(action string, _ json.RawMessage) string {
	return "trackertest:" + action
}
func (trackerTestSystem) PromptDoc() string { return "" }

func (trackerTestSystem) Execute(_ context.Context, action string, params json.RawMessage, cred target.Credential) (any, error) {
	if action != "create_issue" {
		return nil, errors.New("unknown action " + action)
	}
	if strings.TrimSpace(cred.Token) == "" {
		return nil, errors.New("no credential")
	}
	var in trackerIssue
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, err
	}
	in.Token = cred.Token
	trackerState.Lock()
	defer trackerState.Unlock()
	trackerState.filed = append(trackerState.filed, in)
	return map[string]any{"iid": len(trackerState.filed),
		"web_url": "https://tracker.test/gruppe/covey/issues/" + string(rune('0'+len(trackerState.filed)))}, nil
}

func filedIssues() []trackerIssue {
	trackerState.Lock()
	defer trackerState.Unlock()
	return append([]trackerIssue(nil), trackerState.filed...)
}

// TestPlatformIssueFiledWithoutASeat pins covey#200. The shipped covey Doctor
// was told to file platform findings and could not: `covey/create_issue` did
// not exist, and `gitlab:create_issue` was refused because its ACCESS.md grants
// the platform's own system and nothing else. Eight findings waited eleven days
// in an inbox.
//
// It now files through the control plane: the agent brings a title and a body,
// the platform brings the destination and the credential — and the agent has no
// access to that target system at all, which is the entire point.
func TestPlatformIssueFiledWithoutASeat(t *testing.T) {
	trackerState.Lock()
	trackerState.filed = nil
	trackerState.Unlock()

	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	admin.expect(http.MethodPatch, "/api/v1/targets/trackertest", map[string]any{"enabled": true}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/org/platform-repo",
		map[string]any{"system": "trackertest", "project": "gruppe/covey"}, http.StatusOK)
	if err := s.secrets.Put(ctx, s.orgID, "trackertest_token", "bot-token"); err != nil {
		t.Fatal(err)
	}

	// Only `- system: covey scope: agents:review` — no line for trackertest.
	doctor := reviewAgent(t, s, "covey-doctor", "agents:review")

	_, msg := laufLassen(t, s, doctor, "Befund melden",
		`[mock:action covey/create_issue {"title":"Turn limit kills partial results","body":"Eleven runs, three agents, $340."}]
[mock:result gemeldet]`)
	if strings.Contains(msg, "no access") || strings.Contains(msg, "unknown covey action") {
		t.Fatalf("the review scope has to carry the filing: %s", msg)
	}

	filed := filedIssues()
	if len(filed) != 1 {
		t.Fatalf("exactly one issue was filed, got %d", len(filed))
	}
	if filed[0].Title != "Turn limit kills partial results" {
		t.Fatalf("the title is the agent's: %+v", filed[0])
	}
	if filed[0].Repo != "gruppe/covey" {
		t.Fatalf("the destination is master data, not a parameter: %+v", filed[0])
	}
	if filed[0].Token != "bot-token" {
		t.Fatalf("the platform brings the credential: %+v", filed[0])
	}
	if !strings.Contains(filed[0].Description, "$340") ||
		!strings.Contains(filed[0].Description, "covey-doctor") {
		t.Fatalf("body plus provenance belong in the issue: %q", filed[0].Description)
	}

	// The counterpart in the inbox: a human sees what went out in their name.
	items, err := s.registry.ListImprovements(ctx, s.orgID, agents.ImprovementFilter{Kind: agents.KindIssue})
	if err != nil || len(items) != 1 {
		t.Fatalf("the issue belongs in the inbox as well: %d, %v", len(items), err)
	}
	if !strings.HasPrefix(items[0].Link, "https://tracker.test/") {
		t.Fatalf("without the link every reader has to search: %+v", items[0])
	}

	// The same title a second time is refused — one report per fault.
	_, msg = laufLassen(t, s, doctor, "Nochmal melden",
		`[mock:action covey/create_issue {"title":"Turn limit kills partial results","body":"Noch einmal."}]
[mock:result gemeldet]`)
	if !strings.Contains(msg, "already filed") {
		t.Fatalf("a duplicate has to be refused with the reason: %s", msg)
	}
	if len(filedIssues()) != 1 {
		t.Fatalf("and nothing may reach the tracker twice: %d", len(filedIssues()))
	}
}

// Without the review scope nothing is filed: the action is as gated as the rest
// of the platform's own system.
func TestPlatformIssueNeedsTheReviewScope(t *testing.T) {
	trackerState.Lock()
	trackerState.filed = nil
	trackerState.Unlock()

	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	admin.expect(http.MethodPatch, "/api/v1/targets/trackertest", map[string]any{"enabled": true}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/org/platform-repo",
		map[string]any{"system": "trackertest", "project": "gruppe/covey"}, http.StatusOK)

	plain := reviewAgent(t, s, "ohne-scope", "")
	_, msg := laufLassen(t, s, plain, "Befund melden",
		`[mock:action covey/create_issue {"title":"Etwas","body":"Belege."}]
[mock:result versucht]`)
	if !strings.Contains(msg, "agents:review") {
		t.Fatalf("without the scope the action does not exist for this agent: %s", msg)
	}
	if len(filedIssues()) != 0 {
		t.Fatal("and nothing may have been filed")
	}
}

// A missing credential does not swallow the finding: the answer says what is
// missing and where the finding belongs meanwhile.
func TestPlatformIssueSaysWhatIsMissing(t *testing.T) {
	trackerState.Lock()
	trackerState.filed = nil
	trackerState.Unlock()

	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	admin.expect(http.MethodPatch, "/api/v1/targets/trackertest", map[string]any{"enabled": true}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/org/platform-repo",
		map[string]any{"system": "trackertest", "project": "gruppe/covey"}, http.StatusOK)

	doctor := reviewAgent(t, s, "doctor-ohne-token", "agents:review")
	_, msg := laufLassen(t, s, doctor, "Befund melden",
		`[mock:action covey/create_issue {"title":"Etwas","body":"Belege."}]
[mock:result versucht]`)
	if !strings.Contains(msg, "trackertest_token") || !strings.Contains(msg, "review") {
		t.Fatalf("the error has to name the missing secret and the way out: %s", msg)
	}
	if len(filedIssues()) != 0 {
		t.Fatal("and nothing may have been filed")
	}
}

// kanalDouble stands in for the channel to the project: it records what an
// installation without an account of its own would have sent upstream.
type kanalDouble struct {
	agent, titel, text string
	link               string
	err                error
}

func (k *kanalDouble) Bericht(_ context.Context, agent, titel, text string) (string, error) {
	k.agent, k.titel, k.text = agent, titel, text
	return k.link, k.err
}

// TestPlatformIssueGoesUpstreamWithoutAnAccount is the other half of covey#200:
// an installation that has no seat on the forge at all. Until now that meant
// no way to report a platform fault — and "no way" meant the finding stayed in
// an inbox nobody read.
//
// Now it goes through the channel to the project, which files it under its own
// name. Nothing about the destination is the agent's choice, and no credential
// exists on this side to leak.
func TestPlatformIssueGoesUpstreamWithoutAnAccount(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	kanal := &kanalDouble{}
	s.orch.Upstream = kanal

	// No platform-repo setting at all: then it is the project this build comes
	// from, which is exactly the case the channel is for. And no token.
	doctor := reviewAgent(t, s, "doctor-ohne-konto", "agents:review")
	_, msg := laufLassen(t, s, doctor, "Befund melden",
		`[mock:action covey/create_issue {"title":"Wake attempts never give up","body":"900 attempts in six hours, three agents."}]
[mock:result gemeldet]`)
	if strings.Contains(msg, "no ") && strings.Contains(msg, "_token") {
		t.Fatalf("ohne eigenes Konto muss der Projektkanal einspringen: %s", msg)
	}
	if kanal.titel != "Wake attempts never give up" || kanal.agent != "doctor-ohne-konto" {
		t.Fatalf("der Bericht muss mit Titel und Absender hinausgehen: %+v", kanal)
	}
	if !strings.Contains(kanal.text, "900 attempts") {
		t.Fatalf("und mit seinen Belegen: %q", kanal.text)
	}

	// Auch ohne Adresse — er wartet dort auf einen Menschen — steht er im
	// Posteingang, damit hier jemand sieht, was hinausging.
	items, err := s.registry.ListImprovements(ctx, s.orgID, agents.ImprovementFilter{Kind: agents.KindIssue})
	if err != nil || len(items) != 1 {
		t.Fatalf("der Bericht gehoert auch hier in den Posteingang: %d, %v", len(items), err)
	}
}

// Ein Kanal, der nichts annimmt, verschluckt den Befund nicht: der Agent
// erfaehrt, dass er ihn in sein Review schreiben muss.
func TestPlatformIssueSaysWhenNothingCanTakeIt(t *testing.T) {
	s := newStack(t)
	s.orch.Upstream = &kanalDouble{err: errors.New("kein Netz")}

	doctor := reviewAgent(t, s, "doctor-ohne-weg", "agents:review")
	_, msg := laufLassen(t, s, doctor, "Befund melden",
		`[mock:action covey/create_issue {"title":"Etwas","body":"Belege, und zwar genug davon."}]
[mock:result versucht]`)
	if !strings.Contains(msg, "kein Netz") {
		t.Fatalf("der Grund gehoert in die Antwort: %s", msg)
	}
}
