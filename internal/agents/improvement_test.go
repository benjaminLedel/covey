package agents

import (
	"slices"
	"testing"
)

// TestMergeConfigLoeschtNichts: a proposal carries only the files it
// changes. If it replaced the whole set, the proposal to PLAYBOOKS.md would
// make the SOUL.md disappear — the same error set_agent_config made in the
// first real run (internal/orchestrator/hiring.go).
func TestMergeConfigLoeschtNichts(t *testing.T) {
	current := map[string]string{"SOUL.md": "# Rolle", "PLAYBOOKS.md": "alt", "KPIS.md": "x"}
	merged := MergeConfig(current, map[string]string{"PLAYBOOKS.md": "neu"})

	if merged["SOUL.md"] != "# Rolle" || merged["KPIS.md"] != "x" {
		t.Fatalf("unangetastete Dateien müssen stehen bleiben: %v", merged)
	}
	if merged["PLAYBOOKS.md"] != "neu" {
		t.Fatalf("die vorgeschlagene Datei muss gewinnen: %v", merged)
	}
	// And the source stays untouched — the caller still displays it afterwards.
	if current["PLAYBOOKS.md"] != "alt" {
		t.Fatal("MergeConfig darf den aktuellen Stand nicht verändern")
	}
}

// TestChangedFilesIgnoriertUnveraendertes: what the proposal sends along
// unchanged is not a change. This decides not only the display, but also who
// may accept.
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

// TestRestrictedChangesErbtDieRollengrenze: ACCESS.md and EGRESS.md are the
// text view of state whose write path sits with org_admin/security
// (spec/02). A proposal that touches them inherits this boundary.
//
// The second case is the important one: an extra `scope:` line switches
// neither a tool nor an egress target — the change detection of the normal
// write path does not see it. The acceptance UI therefore reads the FILES
// and not the effect.
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

// TestProposalConflictsNurBeiDerselbenDatei: that somebody edited a
// DIFFERENT file in the meantime does not make the proposal wrong. Only when
// the same file was touched would the acceptance overwrite a foreign change
// — the same conflict as with a pull request, and the same
// answer.
func TestProposalConflictsNurBeiDerselbenDatei(t *testing.T) {
	base := map[string]string{"SOUL.md": "# Rolle", "KPIS.md": "alt"}
	changes := map[string]string{"SOUL.md": "# Geschärfte Rolle"}

	// The agent was edited in the meantime — but on the KPIS.md.
	nebenbei := map[string]string{"SOUL.md": "# Rolle", "KPIS.md": "neu"}
	if got := ProposalConflicts(base, nebenbei, changes); len(got) != 0 {
		t.Fatalf("eine fremde Änderung an einer anderen Datei ist kein Konflikt: %v", got)
	}

	// On the same file, it is.
	direkt := map[string]string{"SOUL.md": "# Von Hand geändert", "KPIS.md": "alt"}
	if got := ProposalConflicts(base, direkt, changes); !slices.Equal(got, []string{"SOUL.md"}) {
		t.Fatalf("dieselbe Datei muss als Konflikt gelten: %v", got)
	}

	// Without a base (a proposal against an agent without a config) everything
	// that has content today is a conflict — and only that.
	if got := ProposalConflicts(map[string]string{}, direkt, changes); !slices.Equal(got, []string{"SOUL.md"}) {
		t.Fatalf("ohne Basis zählt der heutige Inhalt: %v", got)
	}
	if got := ProposalConflicts(map[string]string{}, map[string]string{}, changes); len(got) != 0 {
		t.Fatalf("gegen einen leeren Agenten gibt es nichts zu überschreiben: %v", got)
	}
}
