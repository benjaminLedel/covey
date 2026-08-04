package integration

import (
	"context"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The rule this platform rests on: an agent of a foreign organization simply
// does not exist for me. Today it is implemented in three different places in
// httpapi — once as a helper (agentInOrg), twice by hand. Before that is merged,
// this test pins down that it holds at EVERY agent endpoint.
//
// The endpoint list comes from the route table itself, not from a maintained
// copy: a new agent endpoint is thereby checked automatically. A list inside the
// test would go stale at exactly the moment it is needed — with the next
// endpoint added.
var routenZeile = regexp.MustCompile(`"(GET|POST|PUT|PATCH|DELETE) (/api/v1/agents/\{id\}[^"]*)"`)

// platzhalter fills the remaining {…} segments with harmless values.
func platzhalter(path string) string {
	ersatz := map[string]string{
		"{key}":    "some_secret",
		"{system}": "zammad",
		"{hid}":    uuid.NewString(),
		"{tid}":    uuid.NewString(),
		"{name}":   "some-name",
		"{slug}":   "some-slug",
		"{member}": uuid.NewString(),
		"{taskID}": uuid.NewString(),
		"{stage}":  uuid.NewString(),
	}
	for von, nach := range ersatz {
		path = strings.ReplaceAll(path, von, nach)
	}
	// Whatever is still in braces now is new — better a UUID than an unreplaced
	// "{…}" that the router does not resolve.
	return regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(path, uuid.NewString())
}

func agentenEndpunkte(t *testing.T) [][2]string {
	t.Helper()
	roh, err := os.ReadFile("../httpapi/server.go")
	if err != nil {
		t.Fatalf("route table not readable: %v", err)
	}
	var out [][2]string
	for _, m := range routenZeile.FindAllStringSubmatch(string(roh), -1) {
		out = append(out, [2]string{m[1], m[2]})
	}
	if len(out) < 50 {
		t.Fatalf("only %d agent endpoints found — has the notation of the routes changed?", len(out))
	}
	return out
}

func TestFremderAgentIstNirgendsErreichbar(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// An agent in another organization. Our admin has no claim on it whatsoever —
	// not even to the information that it exists.
	fremdeOrg := uuid.New()
	if _, err := s.pool.Exec(ctx, "INSERT INTO organizations (id, name) VALUES ($1,'Fremd-Org')", fremdeOrg); err != nil {
		t.Fatal(err)
	}
	fremd, err := s.registry.Create(ctx, fremdeOrg, "fremder", "Fremder Agent", "mock", nil)
	if err != nil {
		t.Fatal(err)
	}
	// So the foreign agent is not merely empty but would have something to fetch.
	if _, err := s.registry.SaveConfig(ctx, fremd.ID,
		map[string]string{"SOUL.md": "# Secret\n\nInternals of the other organization."}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.backlog.Create(ctx, fremdeOrg, fremd.ID, "Fremde Aufgabe", "vertraulich", "manual", 3); err != nil {
		t.Fatal(err)
	}

	endpunkte := agentenEndpunkte(t)
	t.Logf("checking %d agent endpoints against a foreign agent", len(endpunkte))

	var durchgelassen, nichtGefunden, andere int
	for _, e := range endpunkte {
		method, muster := e[0], e[1]
		path := platzhalter(strings.Replace(muster, "{id}", fremd.ID.String(), 1))

		resp := admin.do(method, path, map[string]any{})
		resp.Body.Close()

		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			durchgelassen++
			t.Errorf("%s %s: HTTP %d — a foreign agent must not get through anywhere", method, muster, resp.StatusCode)
		case resp.StatusCode == http.StatusNotFound:
			nichtGefunden++
		default:
			// 400 (missing body), 409, 503 … — no leak, but no proof of the org
			// check either. It gets more precise with the reading endpoints
			// below.
			andere++
		}
	}
	t.Logf("result: %d × not found, %d × rejected otherwise, %d let through",
		nichtGefunden, andere, durchgelassen)

	// The reading endpoints have no excuse: they need no body and have to say
	// exactly "not found" — not "forbidden", and certainly no data.
	for _, e := range endpunkte {
		if e[0] != http.MethodGet {
			continue
		}
		path := platzhalter(strings.Replace(e[1], "{id}", fremd.ID.String(), 1))
		resp := admin.do(http.MethodGet, path, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: HTTP %d, expected 404", e[1], resp.StatusCode)
		}
	}

	// Counter-check: on one's OWN agent the same endpoints work. Otherwise the
	// test above would be green even if everything simply returned 404.
	eigen := s.newSupportAgent("eigener")
	for _, path := range []string{"", "/config", "/backlog", "/heartbeats", "/recording", "/memories", "/systems"} {
		resp := admin.do(http.MethodGet, "/api/v1/agents/"+eigen.ID.String()+path, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET /agents/{own}%s: HTTP %d, expected 200", path, resp.StatusCode)
		}
	}
}
