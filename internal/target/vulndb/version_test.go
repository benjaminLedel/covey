package vulndb

import "testing"

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
		ok   bool
	}{
		{"1.0.0", "1.0.1", -1, true},
		{"4.17.21", "4.17.20", 1, true},
		{"2.0.0", "2.0.0", 0, true},
		{"1.2", "1.2.0", 0, true},   // fehlende Stellen zählen als 0
		{"v2.3.0", "2.3.0", 0, true}, // führendes v ist Schreibweise
		{"1.10.0", "1.9.0", 1, true}, // stellenweise numerisch, nicht lexikalisch
		{"1.0.0+abc", "1.0.0+def", 0, true},
		// Eine Vorabversion steht VOR der Freigabe — ohne diese Regel liegt
		// jeder Fix-Vergleich an einem rc systematisch daneben.
		{"1.0.0-rc1", "1.0.0", -1, true},
		{"1.0.0-alpha", "1.0.0-beta", -1, true},
		{"1.0.0-1", "1.0.0-alpha", -1, true}, // numerisch vor alphanumerisch
		// Was keine Punktversion ist, hat keine Ordnung. Ein "true" hier wäre
		// eine erfundene Behauptung.
		{"dev-master", "1.0.0", 0, false},
		{"", "1.0.0", 0, false},
	}
	for _, c := range cases {
		got, ok := compareVersions(c.a, c.b)
		if ok != c.ok {
			t.Errorf("compareVersions(%q,%q) comparable = %v, want %v", c.a, c.b, ok, c.ok)
			continue
		}
		if ok && got != c.want {
			t.Errorf("compareVersions(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestSortVersions(t *testing.T) {
	in := []string{"1.10.0", "1.9.0", "dev-master", "2.0.0-rc1", "2.0.0", "1.9.1"}
	sortVersions(in)
	want := []string{"1.9.0", "1.9.1", "1.10.0", "2.0.0-rc1", "2.0.0", "dev-master"}
	for i := range want {
		if in[i] != want[i] {
			t.Fatalf("sorted = %v, want %v", in, want)
		}
	}
}
