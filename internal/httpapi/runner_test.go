package httpapi

import (
	"testing"

	"github.com/google/uuid"

	runnerstore "covey/internal/runner/store"
)

// The data-plane check is instance-wide; what a caller reads of it is only
// what concerns their own hosts (#165).
func TestHealthLinesAreScopedToTheCallersHosts(t *testing.T) {
	mine := uuid.New()
	other := uuid.New()
	own := []runnerstore.Runner{{ID: mine}}
	lines := []string{
		"runner " + mine.String()[:8] + ": image missing",
		"runner " + other.String()[:8] + ": image registry.other.example/private missing",
	}
	got := problemsForOrg(lines, own)
	if len(got) != 1 || got[0] != lines[0] {
		t.Fatalf("only the caller's host may show: %v", got)
	}
	// One host on the instance, no prefix: it belongs to whoever owns a host.
	if got := problemsForOrg([]string{"no Docker daemon reachable"}, own); len(got) != 1 {
		t.Fatalf("an unprefixed line is about the caller's only host: %v", got)
	}
	if got := problemsForOrg([]string{"no Docker daemon reachable"}, nil); len(got) != 0 {
		t.Fatalf("an organisation without hosts reads nothing: %v", got)
	}
}
