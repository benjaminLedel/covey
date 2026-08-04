package observability

import "testing"

// The recording depth is a compliance setting: the org level is the floor that
// security sets, the agent may only deviate UPWARDS. An agent that could turn
// itself down would be exactly the gap the floor is meant to close — that is why
// every combination is listed here, not only the normal case.
func TestEffectiveLevel(t *testing.T) {
	p := func(s string) *string { return &s }

	cases := []struct {
		name  string
		org   string
		agent *string
		want  string
	}{
		{"without an override the floor applies", LevelStandard, nil, LevelStandard},
		{"agent louder than the floor", LevelStandard, p(LevelFull), LevelFull},
		{"agent equal to the floor", LevelStandard, p(LevelStandard), LevelStandard},
		// The core: downwards nothing goes.
		{"agent quieter is ignored", LevelStandard, p(LevelMinimal), LevelStandard},
		{"agent quieter than a full floor", LevelFull, p(LevelMinimal), LevelFull},
		{"agent quieter than a full floor (standard)", LevelFull, p(LevelStandard), LevelFull},
		{"minimal floor, agent raises it", LevelMinimal, p(LevelStandard), LevelStandard},
		{"minimal floor without an override", LevelMinimal, nil, LevelMinimal},
		// Unknown values fall upwards, not downwards.
		{"unknown floor → standard", "nonsense", nil, LevelStandard},
		{"unknown floor, agent full", "nonsense", p(LevelFull), LevelFull},
		{"unknown agent value is ignored", LevelStandard, p("nonsense"), LevelStandard},
		{"empty floor → standard", "", nil, LevelStandard},
		{"empty agent value is ignored", LevelFull, p(""), LevelFull},
	}
	for _, f := range cases {
		t.Run(f.name, func(t *testing.T) {
			if got := effectiveLevel(f.org, f.agent); got != f.want {
				t.Errorf("effectiveLevel(%q, %v) = %q, expected %q", f.org, f.agent, got, f.want)
			}
		})
	}
}

// The property behind the table: the result is NEVER below the floor. It still
// holds when someone deliberately rewrites the table.
func TestEffectiveLevelNieUnterDemBoden(t *testing.T) {
	levels := []string{LevelMinimal, LevelStandard, LevelFull}
	for _, org := range levels {
		for _, agent := range levels {
			a := agent
			got := effectiveLevel(org, &a)
			if levelRank[got] < levelRank[org] {
				t.Errorf("org=%s agent=%s yields %s — below the floor", org, agent, got)
			}
		}
	}
}

func TestValidLevel(t *testing.T) {
	for _, s := range []string{LevelMinimal, LevelStandard, LevelFull} {
		if !ValidLevel(s) {
			t.Errorf("%q must be valid", s)
		}
	}
	for _, s := range []string{"", "nonsense", "MINIMAL", "full-on"} {
		if ValidLevel(s) {
			t.Errorf("%q must not be valid", s)
		}
	}
}

// normalizeBucket decides at which granularity the cost series is aggregated.
// An unknown value must not leak into the SQL query.
func TestNormalizeBucket(t *testing.T) {
	allowed := map[string]bool{}
	for _, b := range []string{"hour", "day", "week", "month"} {
		got := normalizeBucket(b)
		allowed[got] = true
		if got != b {
			t.Errorf("normalizeBucket(%q) = %q — a known granularity must not be bent", b, got)
		}
	}
	for _, b := range []string{"", "year", "'; DROP TABLE cost_entries; --", "HOUR"} {
		if got := normalizeBucket(b); !allowed[got] {
			t.Errorf("normalizeBucket(%q) = %q — an unknown input must fall back to a known granularity", b, got)
		}
	}
}
