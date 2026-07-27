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
- alle: 5m nur-wenn: email titel: Postfach aufgabe: Bearbeite die ungelesenen Mails.
`
	hbs, err := ParseHeartbeat(content)
	if err != nil {
		t.Fatalf("ParseHeartbeat: %v", err)
	}
	if len(hbs) != 4 {
		t.Fatalf("erwartet 4 Einträge, bekommen %d: %+v", len(hbs), hbs)
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
	// Ohne nur-wenn: bleibt OnlyIf leer; mit nur-wenn: trägt es das Zielsystem.
	if hbs[0].OnlyIf != "" || hbs[3].OnlyIf != "email" {
		t.Errorf("OnlyIf falsch: %q / %q", hbs[0].OnlyIf, hbs[3].OnlyIf)
	}
	if hbs[3].Name != "Postfach" || hbs[3].Every != 5*time.Minute {
		t.Errorf("Eintrag 3 falsch: %+v", hbs[3])
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
		"nur-wenn ohne wert":  "- alle: 30m nur-wenn: titel: X",
		"nur-wenn zwei werte": "- alle: 30m nur-wenn: email gitlab titel: X",
	}
	for name, content := range cases {
		if _, err := ParseHeartbeat(content); err == nil {
			t.Errorf("%s: Fehler erwartet für %q", name, content)
		}
	}
}

func TestWikiCleanupHeartbeat(t *testing.T) {
	// Leerer Zeitplan -> Feature aus.
	if _, enabled, err := WikiCleanupHeartbeat(""); enabled || err != nil {
		t.Fatalf("leer: enabled=%v err=%v (erwartet: aus, kein Fehler)", enabled, err)
	}
	if _, enabled, err := WikiCleanupHeartbeat("   "); enabled || err != nil {
		t.Fatalf("whitespace: enabled=%v err=%v", enabled, err)
	}

	// Tageszeit-Form.
	hb, enabled, err := WikiCleanupHeartbeat("03:00")
	if err != nil || !enabled {
		t.Fatalf("03:00: enabled=%v err=%v", enabled, err)
	}
	if hb.Name != WikiCleanupName || hb.DailyAt != "03:00" || hb.Every != 0 || hb.Task == "" {
		t.Fatalf("03:00: unerwarteter Heartbeat %+v", hb)
	}

	// Intervall-Formen (Go-Dauer und Tages-Suffix).
	for _, in := range []string{"24h", "12h", "1d"} {
		hb, enabled, err := WikiCleanupHeartbeat(in)
		if err != nil || !enabled {
			t.Fatalf("%s: enabled=%v err=%v", in, enabled, err)
		}
		if hb.Every <= 0 || hb.DailyAt != "" {
			t.Fatalf("%s: erwartet Intervall-Form, bekam %+v", in, hb)
		}
	}
	if hb, _, _ := WikiCleanupHeartbeat("1d"); hb.Every != 24*time.Hour {
		t.Fatalf("1d: erwartet 24h, bekam %v", hb.Every)
	}

	// Ungültiger Zeitplan -> Fehler.
	for _, bad := range []string{"dreißig", "25:99", "-5m", "morgens"} {
		if _, enabled, err := WikiCleanupHeartbeat(bad); err == nil || enabled {
			t.Errorf("%q: Fehler erwartet, bekam enabled=%v err=%v", bad, enabled, err)
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
