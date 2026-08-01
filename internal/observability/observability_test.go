package observability

import "testing"

// Die Aufzeichnungstiefe ist eine Compliance-Einstellung: Der Org-Level ist der
// Boden, den Security setzt, der Agent darf nur nach OBEN abweichen. Ein Agent,
// der sich leiser stellen könnte, wäre genau die Lücke, die der Boden schließen
// soll — deshalb steht hier jede Kombination, nicht nur der Normalfall.
func TestEffectiveLevel(t *testing.T) {
	p := func(s string) *string { return &s }

	fälle := []struct {
		name  string
		org   string
		agent *string
		want  string
	}{
		{"ohne Override gilt der Boden", LevelStandard, nil, LevelStandard},
		{"Agent lauter als der Boden", LevelStandard, p(LevelFull), LevelFull},
		{"Agent gleich dem Boden", LevelStandard, p(LevelStandard), LevelStandard},
		// Der Kern: nach unten geht nichts.
		{"Agent leiser wird ignoriert", LevelStandard, p(LevelMinimal), LevelStandard},
		{"Agent leiser als voller Boden", LevelFull, p(LevelMinimal), LevelFull},
		{"Agent leiser als voller Boden (standard)", LevelFull, p(LevelStandard), LevelFull},
		{"minimaler Boden, Agent hebt an", LevelMinimal, p(LevelStandard), LevelStandard},
		{"minimaler Boden ohne Override", LevelMinimal, nil, LevelMinimal},
		// Unbekanntes fällt nach oben, nicht nach unten.
		{"unbekannter Boden → standard", "quatsch", nil, LevelStandard},
		{"unbekannter Boden, Agent voll", "quatsch", p(LevelFull), LevelFull},
		{"unbekannter Agent-Wert wird ignoriert", LevelStandard, p("quatsch"), LevelStandard},
		{"leerer Boden → standard", "", nil, LevelStandard},
		{"leerer Agent-Wert wird ignoriert", LevelFull, p(""), LevelFull},
	}
	for _, f := range fälle {
		t.Run(f.name, func(t *testing.T) {
			if got := effectiveLevel(f.org, f.agent); got != f.want {
				t.Errorf("effectiveLevel(%q, %v) = %q, erwartet %q", f.org, f.agent, got, f.want)
			}
		})
	}
}

// Die Eigenschaft hinter der Tabelle: Das Ergebnis liegt NIE unter dem Boden.
// Sie gilt auch dann noch, wenn jemand die Tabelle bewusst umschreibt.
func TestEffectiveLevelNieUnterDemBoden(t *testing.T) {
	stufen := []string{LevelMinimal, LevelStandard, LevelFull}
	for _, org := range stufen {
		for _, agent := range stufen {
			a := agent
			got := effectiveLevel(org, &a)
			if levelRank[got] < levelRank[org] {
				t.Errorf("org=%s agent=%s ergibt %s — unter dem Boden", org, agent, got)
			}
		}
	}
}

func TestValidLevel(t *testing.T) {
	for _, s := range []string{LevelMinimal, LevelStandard, LevelFull} {
		if !ValidLevel(s) {
			t.Errorf("%q muss gültig sein", s)
		}
	}
	for _, s := range []string{"", "quatsch", "MINIMAL", "voll"} {
		if ValidLevel(s) {
			t.Errorf("%q darf nicht gültig sein", s)
		}
	}
}

// normalizeBucket entscheidet, in welcher Körnung die Kostenreihe aggregiert
// wird. Ein unbekannter Wert darf nicht in die SQL-Query durchschlagen.
func TestNormalizeBucket(t *testing.T) {
	erlaubt := map[string]bool{}
	for _, b := range []string{"hour", "day", "week", "month"} {
		got := normalizeBucket(b)
		erlaubt[got] = true
		if got != b {
			t.Errorf("normalizeBucket(%q) = %q — bekannte Körnung darf nicht umgebogen werden", b, got)
		}
	}
	for _, b := range []string{"", "jahr", "'; DROP TABLE cost_entries; --", "HOUR"} {
		if got := normalizeBucket(b); !erlaubt[got] {
			t.Errorf("normalizeBucket(%q) = %q — unbekannte Eingabe muss auf eine bekannte Körnung fallen", b, got)
		}
	}
}
