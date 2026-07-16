package agents

import (
	"strings"
	"testing"
)

func TestCompilePromptOrderAndProtocol(t *testing.T) {
	files := map[string]string{
		"SOUL.md":         "# Support-Agent\n\n## Rolle\nFirst-Level-Support.",
		"PLAYBOOKS.md":    "## Ticket-Triage\n1. Lesen.",
		"CAPABILITIES.md": "## Zuständig\nTickets.",
		"ACCESS.md":       "- system: zammad scope: read,write",
		"TONE.md":         "Sie-Form.",
	}
	p := CompilePrompt(files)

	soul := strings.Index(p, "Support-Agent")
	caps := strings.Index(p, "Zuständig")
	play := strings.Index(p, "Ticket-Triage")
	tone := strings.Index(p, "Sie-Form")
	if !(soul < caps && caps < play && play < tone) {
		t.Fatalf("Reihenfolge falsch: soul=%d caps=%d play=%d tone=%d", soul, caps, play, tone)
	}
	if strings.Contains(p, "system: zammad") {
		t.Fatal("ACCESS.md darf nicht in den Prompt kompiliert werden")
	}
	if !strings.Contains(p, "COVEY_STATUS") {
		t.Fatal("Protokoll-Instruktionen fehlen")
	}
}

func TestCompilePromptEmptyFiles(t *testing.T) {
	p := CompilePrompt(map[string]string{"SOUL.md": "  \n"})
	if !strings.HasPrefix(p, "## Covey-Plattform-Protokoll") {
		t.Fatalf("leere Dateien sollen übersprungen werden, got: %.60q", p)
	}
}

func TestParseAccess(t *testing.T) {
	content := `# Zugänge

- system: zammad     scope: read,write,comment
- system: confluence scope: read
-   system: teams   scopes: send-message
irrelevante Zeile
`
	accs := ParseAccess(content)
	if len(accs) != 3 {
		t.Fatalf("erwartet 3 Zugänge, got %d: %+v", len(accs), accs)
	}
	if accs[0].System != "zammad" || len(accs[0].Scopes) != 3 || accs[0].Scopes[1] != "write" {
		t.Fatalf("zammad-Zugang falsch geparst: %+v", accs[0])
	}
	if accs[2].System != "teams" || accs[2].Scopes[0] != "send-message" {
		t.Fatalf("teams-Zugang falsch geparst: %+v", accs[2])
	}
}

func TestParseAccessEmpty(t *testing.T) {
	if accs := ParseAccess(""); len(accs) != 0 {
		t.Fatalf("leere ACCESS.md soll leer parsen, got %+v", accs)
	}
}
