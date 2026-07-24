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

func TestTeamSection(t *testing.T) {
	s := TeamSection([]TeamMember{
		{Name: "Max Mustermann", JobTitle: "QA Engineer", Email: "max@example.com",
			Identities:       []TeamIdentity{{Label: "GitLab", Value: "maxm"}, {Label: "Zammad", Value: "max@firma.de"}},
			Responsibilities: "testet Bugfixes im Projekt educa"},
		{Name: "Erika Beispiel"}, // nur Name — keine leeren Klammern/Bindestriche
		{Name: "  "},             // ohne Name: überspringen
	})
	if !strings.Contains(s, "## Team (menschliche Mitarbeiter)") {
		t.Fatalf("Überschrift fehlt: %q", s)
	}
	if !strings.Contains(s, "- Max Mustermann — QA Engineer (E-Mail: max@example.com, GitLab: maxm, Zammad: max@firma.de) — zuständig für: testet Bugfixes im Projekt educa") {
		t.Fatalf("Vollprofil-Zeile falsch: %q", s)
	}
	if !strings.Contains(s, "- Erika Beispiel\n") && !strings.HasSuffix(s, "- Erika Beispiel") {
		t.Fatalf("Minimalprofil-Zeile falsch: %q", s)
	}
	if strings.Contains(s, "()") || strings.Contains(s, "Erika Beispiel —") {
		t.Fatalf("leere Felder dürfen keine Artefakte hinterlassen: %q", s)
	}

	if TeamSection(nil) != "" || TeamSection([]TeamMember{{Name: " "}}) != "" {
		t.Fatal("ohne Mitglieder muss der Abschnitt leer sein")
	}
}

func TestTeamAgentsSection(t *testing.T) {
	s := TeamAgentsSection([]AgentColleague{
		{Name: "covey-qa", JobTitle: "QA-Agent", Department: "Engineering", SameTeam: true,
			Identities:       []TeamIdentity{{Label: "GitLab", Value: "covey-qa"}},
			Responsibilities: "testet Merge Requests"},
		{Name: "covey-support", JobTitle: "Support-Agent", Department: "Support", SameTeam: false,
			Identities: []TeamIdentity{{Label: "GitLab", Value: "covey-support"}}},
		{Name: "  "}, // ohne Name: überspringen
	})
	if !strings.Contains(s, "## Team (KI-Kollegen)") {
		t.Fatalf("Überschrift fehlt: %q", s)
	}
	if !strings.Contains(s, "- covey-qa — QA-Agent — DEIN TEAM (Engineering) (GitLab: covey-qa) — zuständig für: testet Merge Requests") {
		t.Fatalf("Team-Kollege-Zeile falsch: %q", s)
	}
	if !strings.Contains(s, "- covey-support — Support-Agent — Team: Support (GitLab: covey-support)") {
		t.Fatalf("Fremd-Team-Zeile falsch: %q", s)
	}
	if strings.Contains(s, "covey-support — DEIN TEAM") {
		t.Fatalf("nur Kollegen aus dem eigenen Team dürfen als DEIN TEAM markiert sein: %q", s)
	}
	if TeamAgentsSection(nil) != "" || TeamAgentsSection([]AgentColleague{{Name: " "}}) != "" {
		t.Fatal("ohne Kollegen muss der Abschnitt leer sein")
	}
}

func TestTeamAgentsSectionSupervisor(t *testing.T) {
	s := TeamAgentsSection([]AgentColleague{
		{Name: "covey-lead", JobTitle: "Lead-Agent", Supervisor: true,
			Identities: []TeamIdentity{{Label: "GitLab", Value: "covey-lead"}}},
	})
	if !strings.Contains(s, "- covey-lead — Lead-Agent — DEIN VORGESETZTER (GitLab: covey-lead)") {
		t.Fatalf("Vorgesetzten-Markierung fehlt oder falsch platziert: %q", s)
	}
}

func TestTeamSectionSupervisor(t *testing.T) {
	s := TeamSection([]TeamMember{
		{Name: "Lena Lead", JobTitle: "Engineering Lead", Supervisor: true,
			Identities: []TeamIdentity{{Label: "GitLab", Value: "leaddev"}}},
		{Name: "Max Mustermann"},
	})
	if !strings.Contains(s, "- Lena Lead — Engineering Lead — DEIN VORGESETZTER (GitLab: leaddev)") {
		t.Fatalf("Vorgesetzten-Markierung fehlt oder falsch platziert: %q", s)
	}
	if strings.Contains(s, "Max Mustermann — DEIN VORGESETZTER") {
		t.Fatalf("nur der Vorgesetzte darf markiert sein: %q", s)
	}
	if !strings.Contains(s, "Merge\nRequests") && !strings.Contains(s, "Merge Requests") {
		t.Fatalf("Hinweis auf Merge Requests an den Vorgesetzten fehlt: %q", s)
	}
}
