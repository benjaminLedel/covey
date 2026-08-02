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

// Die Regel, die diese Plattform trägt: Ein Agent einer fremden Organisation
// ist für mich schlicht nicht vorhanden. Sie wird heute an drei verschiedenen
// Stellen im httpapi umgesetzt — einmal als Helper (agentInOrg), zweimal von
// Hand. Bevor das zusammengeführt wird, hält dieser Test fest, dass sie an
// JEDEM Agenten-Endpunkt gilt.
//
// Die Endpunktliste kommt aus der Routen-Tabelle selbst, nicht aus einer
// gepflegten Kopie: Ein neuer Agenten-Endpunkt ist damit automatisch mitgeprüft.
// Eine Liste im Test würde genau in dem Moment veralten, in dem sie gebraucht
// wird — beim nächsten hinzugefügten Endpunkt.
var routenZeile = regexp.MustCompile(`"(GET|POST|PUT|PATCH|DELETE) (/api/v1/agents/\{id\}[^"]*)"`)

// platzhalter füllt die übrigen {…}-Segmente mit harmlosen Werten.
func platzhalter(pfad string) string {
	ersatz := map[string]string{
		"{key}":    "irgendein_secret",
		"{system}": "zammad",
		"{hid}":    uuid.NewString(),
		"{tid}":    uuid.NewString(),
		"{name}":   "irgendein-name",
		"{slug}":   "irgendein-slug",
		"{member}": uuid.NewString(),
		"{taskID}": uuid.NewString(),
		"{stage}":  uuid.NewString(),
	}
	for von, nach := range ersatz {
		pfad = strings.ReplaceAll(pfad, von, nach)
	}
	// Was jetzt noch in Klammern steht, ist neu — dann lieber eine UUID als
	// ein unersetztes "{…}", das der Router nicht auflöst.
	return regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(pfad, uuid.NewString())
}

func agentenEndpunkte(t *testing.T) [][2]string {
	t.Helper()
	roh, err := os.ReadFile("../httpapi/server.go")
	if err != nil {
		t.Fatalf("routen-tabelle nicht lesbar: %v", err)
	}
	var out [][2]string
	for _, m := range routenZeile.FindAllStringSubmatch(string(roh), -1) {
		out = append(out, [2]string{m[1], m[2]})
	}
	if len(out) < 50 {
		t.Fatalf("nur %d Agenten-Endpunkte gefunden — hat sich die Schreibweise der Routen geändert?", len(out))
	}
	return out
}

func TestFremderAgentIstNirgendsErreichbar(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// Ein Agent in einer anderen Organisation. Unser Admin hat auf ihn
	// keinerlei Anspruch — auch nicht auf die Auskunft, dass es ihn gibt.
	fremdeOrg := uuid.New()
	if _, err := s.pool.Exec(ctx, "INSERT INTO organizations (id, name) VALUES ($1,'Fremd-Org')", fremdeOrg); err != nil {
		t.Fatal(err)
	}
	fremd, err := s.registry.Create(ctx, fremdeOrg, "fremder", "Fremder Agent", "mock", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Damit der Fremde nicht nur leer ist, sondern etwas zu holen gäbe.
	if _, err := s.registry.SaveConfig(ctx, fremd.ID,
		map[string]string{"SOUL.md": "# Geheim\n\nInterna der anderen Organisation."}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.backlog.Create(ctx, fremdeOrg, fremd.ID, "Fremde Aufgabe", "vertraulich", "manual", 3); err != nil {
		t.Fatal(err)
	}

	endpunkte := agentenEndpunkte(t)
	t.Logf("prüfe %d Agenten-Endpunkte gegen einen fremden Agenten", len(endpunkte))

	var durchgelassen, nichtGefunden, andere int
	for _, e := range endpunkte {
		method, muster := e[0], e[1]
		pfad := platzhalter(strings.Replace(muster, "{id}", fremd.ID.String(), 1))

		resp := admin.do(method, pfad, map[string]any{})
		resp.Body.Close()

		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			durchgelassen++
			t.Errorf("%s %s: HTTP %d — fremder Agent darf nirgends durchkommen", method, muster, resp.StatusCode)
		case resp.StatusCode == http.StatusNotFound:
			nichtGefunden++
		default:
			// 400 (fehlender Body), 409, 503 … — kein Leck, aber auch kein
			// Beweis für die Org-Prüfung. Bei den lesenden Endpunkten unten
			// wird es genauer.
			andere++
		}
	}
	t.Logf("Ergebnis: %d × nicht gefunden, %d × anders abgewiesen, %d durchgelassen",
		nichtGefunden, andere, durchgelassen)

	// Bei den lesenden Endpunkten gibt es keine Ausrede: Sie brauchen keinen
	// Body und müssen exakt „nicht gefunden" sagen — nicht „verboten" und erst
	// recht keine Daten.
	for _, e := range endpunkte {
		if e[0] != http.MethodGet {
			continue
		}
		pfad := platzhalter(strings.Replace(e[1], "{id}", fremd.ID.String(), 1))
		resp := admin.do(http.MethodGet, pfad, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: HTTP %d, erwartet 404", e[1], resp.StatusCode)
		}
	}

	// Gegenprobe: Am EIGENEN Agenten funktionieren dieselben Endpunkte. Sonst
	// wäre der Test oben auch dann grün, wenn schlicht alles 404 liefert.
	eigen := s.newSupportAgent("eigener")
	for _, pfad := range []string{"", "/config", "/backlog", "/heartbeats", "/recording", "/memories", "/systems"} {
		resp := admin.do(http.MethodGet, "/api/v1/agents/"+eigen.ID.String()+pfad, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET /agents/{eigener}%s: HTTP %d, erwartet 200", pfad, resp.StatusCode)
		}
	}
}
