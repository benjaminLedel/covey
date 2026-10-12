package workflowhost

import (
	"testing"
	"time"

	"covey/internal/workflow"
)

// A workflow's schedule is due as a heartbeat's is: `every` since the last
// firing, `daily` once a day from its time on.
func TestSchedulesAreDueLikeHeartbeats(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 10, 12, h, m, 0, 0, time.UTC) }
	ptr := func(t time.Time) *time.Time { return &t }
	every := &workflow.Step{Every: time.Hour}
	daily := &workflow.Step{Daily: "08:30"}
	for _, c := range []struct {
		name string
		step *workflow.Step
		last *time.Time
		now  time.Time
		want bool
	}{
		{"every, never fired", every, nil, at(9, 0), true},
		{"every, too soon", every, ptr(at(9, 0)), at(9, 59), false},
		{"every, on time", every, ptr(at(9, 0)), at(10, 0), true},
		{"daily, before its time", daily, nil, at(8, 29), false},
		{"daily, from its time", daily, nil, at(8, 30), true},
		{"daily, fired today", daily, ptr(at(8, 31)), at(17, 0), false},
		{"daily, fired yesterday", daily, ptr(at(8, 31).AddDate(0, 0, -1)), at(9, 0), true},
	} {
		if got := due(c.step, c.last, c.now); got != c.want {
			t.Errorf("%s: due = %v, want %v", c.name, got, c.want)
		}
	}
}
