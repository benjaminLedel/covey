package agents

import (
	"strings"
	"testing"
	"time"
)

func TestParseHeartbeat(t *testing.T) {
	content := `# HEARTBEAT.md — recurring tasks

Prose lines like this one are ignored.

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
		t.Fatalf("expected 4 entries, got %d: %+v", len(hbs), hbs)
	}
	if hbs[0].Name != "Posteingang sichten" || hbs[0].Every != 30*time.Minute {
		t.Errorf("entry 0 wrong: %+v", hbs[0])
	}
	if hbs[0].Task != "Prüfe neue Tickets und triagiere sie." {
		t.Errorf("task 0 wrong: %q", hbs[0].Task)
	}
	if hbs[1].DailyAt != "09:00" || hbs[1].Every != 0 {
		t.Errorf("entry 1 wrong: %+v", hbs[1])
	}
	if hbs[2].Every != 24*time.Hour {
		t.Errorf("1d not parsed as 24h: %+v", hbs[2])
	}
	// Without aufgabe: the task falls back to the title.
	if hbs[2].Task != "Wochenrückblick" {
		t.Errorf("task fallback missing: %q", hbs[2].Task)
	}
	// Without nur-wenn: OnlyIf stays empty; with nur-wenn: it carries the target system.
	if hbs[0].OnlyIf != "" || hbs[3].OnlyIf != "email" {
		t.Errorf("OnlyIf wrong: %q / %q", hbs[0].OnlyIf, hbs[3].OnlyIf)
	}
	if hbs[3].Name != "Postfach" || hbs[3].Every != 5*time.Minute {
		t.Errorf("entry 3 wrong: %+v", hbs[3])
	}
}

func TestParseHeartbeatLeer(t *testing.T) {
	hbs, err := ParseHeartbeat("")
	if err != nil || len(hbs) != 0 {
		t.Fatalf("empty file: hbs=%v err=%v", hbs, err)
	}
}

func TestParseHeartbeatFehler(t *testing.T) {
	cases := map[string]string{
		"titel missing":            "- alle: 30m aufgabe: irgendwas",
		"no schedule":              "- titel: X aufgabe: irgendwas",
		"two schedules":            "- alle: 30m täglich: 09:00 titel: X",
		"broken interval":          "- alle: dreißig titel: X",
		"negative interval":        "- alle: -5m titel: X",
		"broken time of day":       "- täglich: 25:99 titel: X",
		"nur-wenn without value":   "- alle: 30m nur-wenn: titel: X",
		"nur-wenn with two values": "- alle: 30m nur-wenn: email gitlab titel: X",
	}
	for name, content := range cases {
		if _, err := ParseHeartbeat(content); err == nil {
			t.Errorf("%s: expected an error for %q", name, content)
		}
	}
}

func TestWikiCleanupHeartbeat(t *testing.T) {
	// Empty schedule -> feature off.
	if _, enabled, err := WikiCleanupHeartbeat(""); enabled || err != nil {
		t.Fatalf("empty: enabled=%v err=%v (expected: off, no error)", enabled, err)
	}
	if _, enabled, err := WikiCleanupHeartbeat("   "); enabled || err != nil {
		t.Fatalf("whitespace: enabled=%v err=%v", enabled, err)
	}

	// Time-of-day form.
	hb, enabled, err := WikiCleanupHeartbeat("03:00")
	if err != nil || !enabled {
		t.Fatalf("03:00: enabled=%v err=%v", enabled, err)
	}
	if hb.Name != WikiCleanupName || hb.DailyAt != "03:00" || hb.Every != 0 || hb.Task == "" {
		t.Fatalf("03:00: unexpected heartbeat %+v", hb)
	}

	// Interval forms (Go duration and day suffix).
	for _, in := range []string{"24h", "12h", "1d"} {
		hb, enabled, err := WikiCleanupHeartbeat(in)
		if err != nil || !enabled {
			t.Fatalf("%s: enabled=%v err=%v", in, enabled, err)
		}
		if hb.Every <= 0 || hb.DailyAt != "" {
			t.Fatalf("%s: expected the interval form, got %+v", in, hb)
		}
	}
	if hb, _, _ := WikiCleanupHeartbeat("1d"); hb.Every != 24*time.Hour {
		t.Fatalf("1d: expected 24h, got %v", hb.Every)
	}

	// Invalid schedule -> error.
	for _, bad := range []string{"dreißig", "25:99", "-5m", "morgens"} {
		if _, enabled, err := WikiCleanupHeartbeat(bad); err == nil || enabled {
			t.Errorf("%q: expected an error, got enabled=%v err=%v", bad, enabled, err)
		}
	}
}

func TestCompilePromptOhneHeartbeat(t *testing.T) {
	prompt := CompilePrompt(map[string]string{
		"SOUL.md":      "Du bist der Test-Agent.",
		"HEARTBEAT.md": "- alle: 30m titel: Posteingang sichten",
	})
	if strings.Contains(prompt, "Posteingang sichten") {
		t.Error("HEARTBEAT.md must not be compiled into the system prompt")
	}
}
