package httpapi

import (
	"testing"
	"time"
)

// The keepalive a client asks for stays between the default and what a
// reverse proxy tolerates (#447).
func TestKeepaliveEvery(t *testing.T) {
	for raw, want := range map[string]time.Duration{
		"":     20 * time.Second,
		"abc":  20 * time.Second,
		"5":    20 * time.Second,
		"45":   45 * time.Second,
		"600":  55 * time.Second,
		" 30 ": 30 * time.Second,
	} {
		if got := keepaliveEvery(raw); got != want {
			t.Errorf("keepaliveEvery(%q) = %v, want %v", raw, got, want)
		}
	}
}
