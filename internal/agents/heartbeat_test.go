package agents

import (
	"strings"
	"testing"
	"time"
)

func TestParseHeartbeat(t *testing.T) {
	content := `# HEARTBEAT.md — wiederkehrende Aufgaben

Prosa-Zeilen wie diese werden ignoriert.

- alle: 30m titel: Posteingang sichten aufgabe: Prüfe neue Tickets und triagiere sie.
- täglich: 09:00 titel: Tagesbericht aufgabe: Fasse den gestrigen Tag zusammen.
- alle: 1d titel: Wochenrückblick
`
	hbs, err := ParseHeartbeat(content)
	if err != nil {
		t.Fatalf("ParseHeartbeat: %v", err)
	}
	if len(hbs) != 3 {
		t.Fatalf("erwartet 3 Einträge, bekommen %d: %+v", len(hbs), hbs)
	}
	if hbs[0].Name != "Posteingang sichten" || hbs[0].Every != 30*time.Minute {
		t.Errorf("Eintrag 0 falsch: %+v", hbs[0])
	}
	if hbs[0].Task != "Prüfe neue Tickets und triagiere sie." {
		t.Errorf("Aufgabe 0 falsch: %q", hbs[0].Task)
	}
	if hbs[1].DailyAt != "09:00" || hbs[1].Every != 0 {
		t.Errorf("Eintrag 1 falsch: %+v", hbs[1])
	}
	if hbs[2].Every != 24*time.Hour {
		t.Errorf("1d nicht als 24h geparst: %+v", hbs[2])
	}
	// Ohne aufgabe: fällt der Task auf den Titel zurück.
	if hbs[2].Task != "Wochenrückblick" {
		t.Errorf("Task-Fallback fehlt: %q", hbs[2].Task)
	}
}

func TestParseHeartbeatLeer(t *testing.T) {
	hbs, err := ParseHeartbeat("")
	if err != nil || len(hbs) != 0 {
		t.Fatalf("leere Datei: hbs=%v err=%v", hbs, err)
	}
}

func TestParseHeartbeatFehler(t *testing.T) {
	cases := map[string]string{
		"titel fehlt":         "- alle: 30m aufgabe: irgendwas",
		"kein zeitplan":       "- titel: X aufgabe: irgendwas",
		"doppelter zeitplan":  "- alle: 30m täglich: 09:00 titel: X",
		"kaputtes intervall":  "- alle: dreißig titel: X",
		"negatives intervall": "- alle: -5m titel: X",
		"kaputte tageszeit":   "- täglich: 25:99 titel: X",
	}
	for name, content := range cases {
		if _, err := ParseHeartbeat(content); err == nil {
			t.Errorf("%s: Fehler erwartet für %q", name, content)
		}
	}
}

func TestCompilePromptOhneHeartbeat(t *testing.T) {
	prompt := CompilePrompt(map[string]string{
		"SOUL.md":      "Du bist der Test-Agent.",
		"HEARTBEAT.md": "- alle: 30m titel: Posteingang sichten",
	})
	if strings.Contains(prompt, "Posteingang sichten") {
		t.Error("HEARTBEAT.md darf nicht in den System-Prompt kompiliert werden")
	}
}
