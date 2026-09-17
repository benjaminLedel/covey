package httpapi

import (
	"strings"
	"testing"

	"covey/internal/daemon"
)

// The thinking effort is checked against the ENGINE, not against a list in
// this layer. Otherwise an agent on an engine without the knob takes a level
// that nobody ever reads — configured, visible, without effect.
func TestCheckEffortAsksTheEngine(t *testing.T) {
	for _, tc := range []struct {
		runtime, effort string
		wantOK          bool
	}{
		{"claude-code", "", true},
		{"claude-code", "max", true},
		{"claude-code", "xhigh", true},
		{"claude-code", "hoch", false},    // typo out of a bundle
		{"claude-code", "minimal", false}, // a level a DIFFERENT engine knows
		{"codex", "", true},               // empty means "engine default" everywhere
		{"codex", "max", false},           // codex declares no levels
		{"nope", "", true},
		{"nope", "low", false}, // unknown engine: fail-closed
	} {
		msg := checkEffort(tc.runtime, tc.effort)
		if gotOK := msg == ""; gotOK != tc.wantOK {
			t.Errorf("checkEffort(%q, %q) = %q, wantOK=%v", tc.runtime, tc.effort, msg, tc.wantOK)
		}
	}
}

// The error message has to name the levels this engine really has — a message
// with foreign levels sends the reader off in the wrong direction.
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
