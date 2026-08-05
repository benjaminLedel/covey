package vulndb

import (
	"strconv"
	"strings"
)

// Versionsvergleich. Er hat genau eine Aufgabe: aus den Intervallen eines
// Advisories dasjenige zu finden, in dem die installierte Version liegt — damit
// die richtige Fix-Version im Ticket landet. Ein Advisory hat oft MEHRERE
// Fix-Zweige (2.4.5 für die 2.x-Reihe, 3.1.2 für die 3.x-Reihe); wer den
// falschen nennt, schlägt einen Major-Upgrade vor, wo eine Patch-Version
// gereicht hätte.
//
// Bewusst kein vollständiger semver: die Ökosysteme hier schreiben Versionen
// unterschiedlich (npm semver, Packagist mit führendem "v" und dev-Branches,
// Pub semver), und ein Vergleich, der bei "dev-master" eine Ordnung behauptet,
// wäre falsch statt unvollständig. Was nicht vergleichbar ist, meldet
// compareVersions als solches — der Aufrufer nennt dann alle Kandidaten statt
// einen zu raten.

// compareVersions vergleicht zwei Versionen. ok=false heißt: mindestens eine
// von beiden ist keine Punktversion (z. B. "dev-master"), eine Ordnung gibt es
// dann nicht.
func compareVersions(a, b string) (cmp int, ok bool) {
	ca, pa, oka := splitVersion(a)
	cb, pb, okb := splitVersion(b)
	if !oka || !okb {
		return 0, false
	}
	if c := compareNumericParts(ca, cb); c != 0 {
		return c, true
	}
	// Gleicher Kern: eine Vorabversion steht VOR der Freigabe (1.0.0-rc1 <
	// 1.0.0). Das ist die eine semver-Regel, ohne die ein Fix-Vergleich
	// systematisch danebenliegt.
	switch {
	case pa == "" && pb == "":
		return 0, true
	case pa == "":
		return 1, true
	case pb == "":
		return -1, true
	default:
		return comparePrerelease(pa, pb), true
	}
}

// splitVersion zerlegt in Kern-Segmente und Vorabteil. ok=false, wenn der Kern
// keine Zahlenfolge ist.
func splitVersion(v string) (core []string, prerelease string, ok bool) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if v == "" {
		return nil, "", false
	}
	// Build-Metadaten (+sha) sind laut semver für die Ordnung bedeutungslos.
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v, prerelease = v[:i], v[i+1:]
	}
	core = strings.Split(v, ".")
	for _, seg := range core {
		if seg == "" {
			return nil, "", false
		}
		if _, err := strconv.Atoi(seg); err != nil {
			return nil, "", false
		}
	}
	return core, prerelease, true
}

// compareNumericParts vergleicht die Kern-Segmente stellenweise; fehlende
// Stellen zählen als 0 ("1.2" == "1.2.0").
func compareNumericParts(a, b []string) int {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(a) {
			x, _ = strconv.Atoi(a[i])
		}
		if i < len(b) {
			y, _ = strconv.Atoi(b[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// comparePrerelease vergleicht zwei Vorabteile nach semver-Regeln: punktweise,
// numerische Bezeichner numerisch und vor alphanumerischen.
func comparePrerelease(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		if i >= len(pa) {
			return -1 // weniger Bezeichner = niedriger
		}
		if i >= len(pb) {
			return 1
		}
		x, errX := strconv.Atoi(pa[i])
		y, errY := strconv.Atoi(pb[i])
		switch {
		case errX == nil && errY == nil:
			if x != y {
				if x < y {
					return -1
				}
				return 1
			}
		case errX == nil:
			return -1 // numerisch steht vor alphanumerisch
		case errY == nil:
			return 1
		default:
			if pa[i] != pb[i] {
				if pa[i] < pb[i] {
					return -1
				}
				return 1
			}
		}
	}
	return 0
}
