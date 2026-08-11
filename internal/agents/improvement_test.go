package agents

import (
	"slices"
	"testing"
)

// TestMergeConfigLoeschtNichts: ein Vorschlag trägt nur die Dateien, die er
// ändert. Ersetzte er den ganzen Satz, verschwände der Vorschlag zur
// PLAYBOOKS.md die SOUL.md — derselbe Fehler, den set_agent_config im ersten
// echten Lauf gemacht hat (internal/orchestrator/hiring.go).
func TestMergeConfigLoeschtNichts(t *testing.T) {
	current := map[string]string{"SOUL.md": "# Rolle", "PLAYBOOKS.md": "alt", "KPIS.md": "x"}
	merged := MergeConfig(current, map[string]string{"PLAYBOOKS.md": "neu"})

	if merged["SOUL.md"] != "# Rolle" || merged["KPIS.md"] != "x" {
		t.Fatalf("unangetastete Dateien müssen stehen bleiben: %v", merged)
	}
	if merged["PLAYBOOKS.md"] != "neu" {
		t.Fatalf("die vorgeschlagene Datei muss gewinnen: %v", merged)
	}
	// Und die Quelle bleibt unberührt — der Aufrufer zeigt sie danach noch an.
	if current["PLAYBOOKS.md"] != "alt" {
		t.Fatal("MergeConfig darf den aktuellen Stand nicht verändern")
	}
}

// TestChangedFilesIgnoriertUnveraendertes: was der Vorschlag identisch
// mitschickt, ist keine Änderung. Das entscheidet nicht nur die Anzeige,
// sondern auch, wer annehmen darf.
func TestChangedFilesIgnoriertUnveraendertes(t *testing.T) {
	current := map[string]string{"SOUL.md": "gleich", "PLAYBOOKS.md": "alt"}
	changed := ChangedFiles(current, map[string]string{
		"SOUL.md":      "gleich",
		"PLAYBOOKS.md": "neu",
		"NEU.md":       "dazu",
	})
	if !slices.Equal(changed, []string{"NEU.md", "PLAYBOOKS.md"}) {
		t.Fatalf("erwartet [NEU.md PLAYBOOKS.md], bekommen %v", changed)
	}
}

// TestRestrictedChangesErbtDieRollengrenze: ACCESS.md und EGRESS.md sind die
// Textansicht auf Zustand, dessen Schreibweg bei platform_admin/security liegt
// (spec/02). Ein Vorschlag, der sie anfasst, erbt diese Grenze.
//
// Der zweite Fall ist der wichtige: eine zusätzliche `scope:`-Zeile schaltet
// weder ein Tool noch ein Egress-Ziel um — die Änderungserkennung des
// normalen Schreibwegs sieht sie nicht. Die Annahme-Oberfläche liest deshalb
// die DATEIEN und nicht die Wirkung.
func TestRestrictedChangesErbtDieRollengrenze(t *testing.T) {
	current := map[string]string{
		"SOUL.md":   "# Rolle",
		"ACCESS.md": "- system: zammad scope: ticket.read",
	}

	if got := RestrictedChanges(current, map[string]string{"SOUL.md": "# Neue Rolle"}); len(got) != 0 {
		t.Fatalf("ein Vorschlag zur SOUL.md braucht keine Security: %v", got)
	}
	if got := RestrictedChanges(current, map[string]string{
		"ACCESS.md": "- system: zammad scope: ticket.read",
	}); len(got) != 0 {
		t.Fatalf("eine unveränderte ACCESS.md ist keine Änderung: %v", got)
	}
	got := RestrictedChanges(current, map[string]string{
		"ACCESS.md": "- system: zammad scope: ticket.read,ticket.write",
	})
	if !slices.Equal(got, []string{"ACCESS.md"}) {
		t.Fatalf("ein weiterer Scope weitet den Zugang: %v", got)
	}
	got = RestrictedChanges(current, map[string]string{"EGRESS.md": "- host: example.com"})
	if !slices.Equal(got, []string{"EGRESS.md"}) {
		t.Fatalf("eine neue EGRESS.md ist eine Änderung: %v", got)
	}
}

// TestProposalConflictsNurBeiDerselbenDatei: dass jemand zwischenzeitlich eine
// ANDERE Datei bearbeitet hat, macht den Vorschlag nicht falsch. Erst wenn
// dieselbe Datei angefasst wurde, würde die Annahme eine fremde Änderung
// überschreiben — derselbe Konflikt wie bei einem Pull Request, und dieselbe
// Antwort.
func TestProposalConflictsNurBeiDerselbenDatei(t *testing.T) {
	base := map[string]string{"SOUL.md": "# Rolle", "KPIS.md": "alt"}
	changes := map[string]string{"SOUL.md": "# Geschärfte Rolle"}

	// Der Agent wurde zwischendurch bearbeitet — aber an der KPIS.md.
	nebenbei := map[string]string{"SOUL.md": "# Rolle", "KPIS.md": "neu"}
	if got := ProposalConflicts(base, nebenbei, changes); len(got) != 0 {
		t.Fatalf("eine fremde Änderung an einer anderen Datei ist kein Konflikt: %v", got)
	}

	// An derselben Datei schon.
	direkt := map[string]string{"SOUL.md": "# Von Hand geändert", "KPIS.md": "alt"}
	if got := ProposalConflicts(base, direkt, changes); !slices.Equal(got, []string{"SOUL.md"}) {
		t.Fatalf("dieselbe Datei muss als Konflikt gelten: %v", got)
	}

	// Ohne Basis (Vorschlag gegen einen Agenten ohne Config) ist alles, was
	// heute Inhalt hat, ein Konflikt — und nur das.
	if got := ProposalConflicts(map[string]string{}, direkt, changes); !slices.Equal(got, []string{"SOUL.md"}) {
		t.Fatalf("ohne Basis zählt der heutige Inhalt: %v", got)
	}
	if got := ProposalConflicts(map[string]string{}, map[string]string{}, changes); len(got) != 0 {
		t.Fatalf("gegen einen leeren Agenten gibt es nichts zu überschreiben: %v", got)
	}
}
