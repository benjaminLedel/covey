package agents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rules sammelt die Regel-Namen der Befunde, damit Tests auf sie prüfen können.
func rules(fs []Finding) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Rule
	}
	return out
}

func hasRule(fs []Finding, rule string) bool {
	for _, f := range fs {
		if f.Rule == rule {
			return true
		}
	}
	return false
}

// TestLintIntervalDependsOnSystems ist der Kern der Intervall-Regel: Dasselbe
// Intervall ist je nach Zielsystem in Ordnung oder ruinös. Ein Postfach alle
// 2 Minuten zu sichten ist normal; alle 2 Minuten ein Repo zu klonen nicht.
func TestLintIntervalDependsOnSystems(t *testing.T) {
	hb := "- alle: 2m nur-wenn: gitlab:issues:assigned titel: Issues sichten aufgabe: Bearbeite und kommentiere.\n"

	heavy := Lint(Subject{Slug: "dev", Files: map[string]string{
		"ACCESS.md":    "- system: gitlab scope: read,write",
		"HEARTBEAT.md": hb,
	}})
	if !hasRule(heavy, "heartbeat-intervall-zu-kurz") {
		t.Fatalf("2m mit gitlab muss auffallen, got %v", rules(heavy))
	}

	// Dasselbe Intervall bei einem reinen Postfach-Agenten: kein Befund.
	light := Lint(Subject{Slug: "mail", Files: map[string]string{
		"ACCESS.md":    "- system: email scope: read,write",
		"HEARTBEAT.md": "- alle: 3m nur-wenn: email titel: Posteingang aufgabe: Antworte per reply.\n",
	}})
	if hasRule(light, "heartbeat-intervall-zu-kurz") {
		t.Fatalf("3m ohne schweres Zielsystem darf nicht auffallen, got %v", rules(light))
	}
}

// TestLintVisibleTrace prüft die Regel hinter dem beobachteten Endlos-Loop: Ein
// gitlab-gegateter Heartbeat ohne Kommentar-Schritt hinterlässt keine Flanke.
func TestLintVisibleTrace(t *testing.T) {
	stumm := Lint(Subject{Slug: "stumm", Files: map[string]string{
		"ACCESS.md":    "- system: gitlab scope: read,write",
		"HEARTBEAT.md": "- alle: 15m nur-wenn: gitlab:issues titel: Sichten aufgabe: Lies die Issues und setze Labels.\n",
		"PLAYBOOKS.md": "1. list_issues aufrufen.\n2. Label setzen.\n",
	}})
	if !hasRule(stumm, "keine-sichtbare-spur") {
		t.Fatalf("Playbook ohne Kommentar-Schritt muss auffallen, got %v", rules(stumm))
	}

	// Mit Kommentar-Schritt ist die Flanke gesichert — kein Befund.
	laut := Lint(Subject{Slug: "laut", Files: map[string]string{
		"ACCESS.md":    "- system: gitlab scope: read,write,comment",
		"HEARTBEAT.md": "- alle: 15m nur-wenn: gitlab:issues titel: Sichten aufgabe: Bearbeite die Issues.\n",
		"PLAYBOOKS.md": "1. list_issues aufrufen.\n2. Ergebnis per comment im Issue festhalten.\n",
	}})
	if hasRule(laut, "keine-sichtbare-spur") {
		t.Fatalf("Playbook mit comment darf nicht auffallen, got %v", rules(laut))
	}

	// Ohne gitlab-Gate greift die Regel gar nicht — sie gilt nur für die
	// Flanken-Bedingung, nicht als allgemeine Kommentar-Pflicht.
	ohneGate := Lint(Subject{Slug: "frei", Files: map[string]string{
		"ACCESS.md":    "- system: gitlab scope: read",
		"HEARTBEAT.md": "- täglich: 09:00 titel: Bericht aufgabe: Fasse zusammen.\n",
		"PLAYBOOKS.md": "1. Nichts kommentieren.\n",
	}})
	if hasRule(ohneGate, "keine-sichtbare-spur") {
		t.Fatalf("ohne gitlab-Gate darf die Regel nicht greifen, got %v", rules(ohneGate))
	}
}

// TestLintBlockedNegationsNotFlagged: Gute Configs schreiben „ende mit done,
// NIE blocked". Eine Regel, die darauf anschlägt, wäre wertlos — sie meldete
// genau bei den Agenten, die es richtig machen.
func TestLintBlockedNegationsNotFlagged(t *testing.T) {
	gut := Lint(Subject{Slug: "gut", Files: map[string]string{
		"ACCESS.md": "- system: gitlab scope: read,write",
		"SOUL.md":   "Ende jeden Lauf mit done, NIE mit blocked (GitLab hat keinen Webhook).",
	}})
	if hasRule(gut, "blocked-bei-polling-system") {
		t.Fatalf("Verneinung darf nicht auffallen, got %v", rules(gut))
	}

	schlecht := Lint(Subject{Slug: "schlecht", Files: map[string]string{
		"ACCESS.md": "- system: gitlab scope: read,write",
		"SOUL.md":   "Wartest du auf eine Antwort, beende den Lauf mit blocked.",
	}})
	if !hasRule(schlecht, "blocked-bei-polling-system") {
		t.Fatalf("echte blocked-Anweisung muss auffallen, got %v", rules(schlecht))
	}
}

// TestLintStages prüft die Board-Regeln gegen den real beobachteten Wildwuchs.
func TestLintStages(t *testing.T) {
	f := Lint(Subject{Slug: "board", AgentStages: []string{"Issue-Triage", "#83 CSV-Import"}})
	if !hasRule(f, "stage-benennt-vorgang") {
		t.Fatalf("Spalte mit Vorgangs-ID muss auffallen, got %v", rules(f))
	}
	if hasRule(f, "stage-wildwuchs") {
		t.Fatalf("zwei Spalten sind kein Wildwuchs, got %v", rules(f))
	}

	viele := make([]string, 12)
	for i := range viele {
		viele[i] = string(rune('A' + i))
	}
	if !hasRule(Lint(Subject{Slug: "board", AgentStages: viele}), "stage-wildwuchs") {
		t.Fatalf("12 Spalten müssen auffallen")
	}
}

// TestLintTurnLimit meldet erst bei Häufung — einzelne Abbrüche fängt die
// Plattform selbst mit einer Folgeaufgabe auf.
func TestLintTurnLimit(t *testing.T) {
	if hasRule(Lint(Subject{Slug: "a", TurnLimitFailures: 1}), "haeufige-turn-limit-abbrueche") {
		t.Fatalf("ein einzelner Abbruch ist kein Befund")
	}
	f := Lint(Subject{Slug: "a", TurnLimitFailures: 7, MaxTurns: 20})
	if !hasRule(f, "haeufige-turn-limit-abbrueche") {
		t.Fatalf("Häufung muss auffallen, got %v", rules(f))
	}
	if !strings.Contains(f[0].Hint, "max_turns") {
		t.Fatalf("bei kleinem max_turns muss der Hinweis es nennen: %q", f[0].Hint)
	}
}

// TestLintCleanConfigIsSilent ist die wichtigste Zusicherung: Ein Lint, der bei
// guten Configs meckert, wird ignoriert und ist damit wertlos. Geprüft an den
// mitgelieferten Beispiel-Bundles — sie sind die Vorlage für neue Agenten und
// müssen befundfrei sein.
func TestLintCleanConfigIsSilent(t *testing.T) {
	paths, err := filepath.Glob("../../examples/*.bundle.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("Beispiel-Bundles nicht gefunden: %v", err)
	}
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var bundle struct {
			Agent struct {
				Slug string `json:"slug"`
			} `json:"agent"`
			Files map[string]string `json:"files"`
		}
		if err := json.Unmarshal(raw, &bundle); err != nil {
			t.Fatal(err)
		}
		if f := Lint(Subject{Slug: bundle.Agent.Slug, Files: bundle.Files}); len(f) > 0 {
			t.Errorf("%s: Beispiel-Bundle muss befundfrei sein, got %+v", filepath.Base(p), f)
		}
	}
}
