package httpapi

import (
	"strings"
	"testing"

	"covey/internal/daemon"
)

// Der Denkaufwand wird gegen die ENGINE geprüft, nicht gegen eine Liste in
// dieser Schicht. Sonst nimmt ein Agent auf einer Engine ohne den Regler eine
// Stufe an, die nie jemand liest — konfiguriert, sichtbar, ohne Wirkung.
func TestCheckEffortAsksTheEngine(t *testing.T) {
	for _, tc := range []struct {
		runtime, effort string
		wantOK          bool
	}{
		{"claude-code", "", true},
		{"claude-code", "max", true},
		{"claude-code", "xhigh", true},
		{"claude-code", "hoch", false},    // Tippfehler aus einem Bundle
		{"claude-code", "minimal", false}, // eine Stufe, die eine ANDERE Engine kennt
		{"codex", "", true},               // leer heißt überall „Engine-Default"
		{"codex", "max", false},           // codex deklariert keine Stufen
		{"nope", "", true},
		{"nope", "low", false}, // unbekannte Engine: fail-closed
	} {
		msg := checkEffort(tc.runtime, tc.effort)
		if gotOK := msg == ""; gotOK != tc.wantOK {
			t.Errorf("checkEffort(%q, %q) = %q, wantOK=%v", tc.runtime, tc.effort, msg, tc.wantOK)
		}
	}
}

// Die Fehlermeldung muss die Stufen nennen, die diese Engine wirklich kann —
// eine Meldung mit fremden Stufen schickt den Leser in die falsche Richtung.
func TestCheckEffortNamesTheEnginesLevels(t *testing.T) {
	msg := checkEffort("claude-code", "hoch")
	for _, lvl := range daemon.EffortLevels("claude-code") {
		if !strings.Contains(msg, lvl) {
			t.Errorf("Meldung nennt Stufe %q nicht: %q", lvl, msg)
		}
	}
	if msg := checkEffort("codex", "max"); !strings.Contains(msg, "no reasoning-effort") {
		t.Errorf("codex sollte als Engine ohne Regler antworten, bekommen: %q", msg)
	}
}
