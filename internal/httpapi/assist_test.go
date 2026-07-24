package httpapi

import "testing"

func TestParseAssistReply(t *testing.T) {
	t.Run("reines JSON", func(t *testing.T) {
		out := parseAssistReply(`{"reply":"Hier ein Entwurf.","proposals":[{"file":"SOUL.md","content":"# Rolle"}]}`)
		if out.Reply != "Hier ein Entwurf." {
			t.Fatalf("reply = %q", out.Reply)
		}
		if len(out.Proposals) != 1 || out.Proposals[0].File != "SOUL.md" || out.Proposals[0].Content != "# Rolle" {
			t.Fatalf("proposals = %+v", out.Proposals)
		}
	})

	t.Run("in Codefence gewickelt", func(t *testing.T) {
		out := parseAssistReply("```json\n{\"reply\":\"ok\",\"proposals\":[]}\n```")
		if out.Reply != "ok" || len(out.Proposals) != 0 {
			t.Fatalf("out = %+v", out)
		}
	})

	t.Run("kein JSON — Fallback auf Prosa", func(t *testing.T) {
		out := parseAssistReply("Ich brauche mehr Details, bevor ich etwas vorschlage.")
		if out.Reply != "Ich brauche mehr Details, bevor ich etwas vorschlage." {
			t.Fatalf("reply = %q", out.Reply)
		}
		if len(out.Proposals) != 0 {
			t.Fatalf("proposals sollten leer sein: %+v", out.Proposals)
		}
	})

	t.Run("ACCESS.md/EGRESS.md sind erlaubt", func(t *testing.T) {
		out := parseAssistReply(`{"reply":"x","proposals":[{"file":"ACCESS.md","content":"# Zugänge"},{"file":"EGRESS.md","content":"# Egress"}]}`)
		if len(out.Proposals) != 2 {
			t.Fatalf("proposals = %+v", out.Proposals)
		}
	})

	t.Run("Legacy TOOLS.md wird verworfen", func(t *testing.T) {
		out := parseAssistReply(`{"reply":"x","proposals":[{"file":"TOOLS.md","content":"nope"},{"file":"PLAYBOOKS.md","content":"ja"}]}`)
		if len(out.Proposals) != 1 || out.Proposals[0].File != "PLAYBOOKS.md" {
			t.Fatalf("proposals = %+v", out.Proposals)
		}
	})
}
