package vulndb

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// OSV.dev ist die Grundlage dieses Plugins: eine API, die npm, Packagist und
// Pub gleich behandelt, ohne Schlüssel, mit Stapelabfrage. Die anderen Quellen
// (GHSA, NVD, Registries) ergänzen Details — die Frage „ist diese Version
// betroffen?" beantwortet OSV.
//
// Zwei Eigenheiten der API, die den Aufbau hier erklären:
//
//  1. querybatch antwortet ABGEKÜRZT — pro Anfrage nur die IDs der Treffer,
//     keine Details. Wer die Details braucht, muss nachfassen.
//  2. Die Antwort ist POSITIONELL: results[i] gehört zu queries[i]. Ohne die
//     Reihenfolge ist ein Treffer keinem Paket mehr zuzuordnen.
//
// Daraus folgt das Vorgehen: erst ein Stapel über alle Pakete (billig, sagt
// WELCHE betroffen sind), dann eine Einzelabfrage pro betroffenem Paket —
// /v1/query liefert die vollständigen Datensätze, und betroffen sind
// erfahrungsgemäß wenige Prozent. Der umgekehrte Weg (ein Detailabruf je
// Schwachstellen-ID) wäre mehr Aufrufe für dasselbe Ergebnis.

// Basisadresse als Variable, damit die Tests einen httptest-Server
// dazwischenschieben können. Zur Laufzeit wird sie nie verändert.
var osvBase = "https://api.osv.dev"

// Ökosystem-Namen, wie OSV sie schreibt. Groß-/Kleinschreibung ist Teil des
// Namens: "packagist" liefert null Treffer statt eines Fehlers — der stillste
// aller Fehlschläge, und der Grund, warum normalizeEcosystem existiert.
const (
	EcosystemNPM       = "npm"
	EcosystemPackagist = "Packagist"
	EcosystemPub       = "Pub"
)

// osvBatchSize begrenzt eine Stapelanfrage. Ein großes Monorepo bringt
// mehrere tausend Pakete mit; in einem Aufruf wäre das weder für die API noch
// für den Zeitrahmen zumutbar.
const osvBatchSize = 500

// normalizeEcosystem übersetzt, was ein Agent naheliegenderweise schreibt, in
// den Namen, den OSV kennt.
func normalizeEcosystem(name string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "npm", "node", "nodejs", "javascript":
		return EcosystemNPM, nil
	case "packagist", "composer", "php":
		return EcosystemPackagist, nil
	case "pub", "dart", "flutter":
		return EcosystemPub, nil
	default:
		return "", fmt.Errorf("unknown ecosystem %q — supported: npm, Packagist (composer/php), Pub (dart/flutter)", name)
	}
}

// Finding ist ein Treffer, wie der Agent ihn braucht: das Paket, die
// Schwachstelle und — das eigentlich Schwierige — die Fix-Version für GENAU
// den Versionszweig, auf dem dieses Projekt sitzt.
type Finding struct {
	Package    string `json:"package"`
	Ecosystem  string `json:"ecosystem"`
	Version    string `json:"version"`
	Dependency string `json:"dependency,omitempty"`

	ID      string   `json:"id"`
	Aliases []string `json:"aliases,omitempty"`
	// CVE ist der bevorzugte Bezeichner fürs Ticket: er ist über alle Quellen
	// hinweg derselbe und taugt deshalb als Suchschlüssel für die
	// Duplikatsprüfung. Leer, wenn das Advisory keine CVE hat.
	CVE     string `json:"cve,omitempty"`
	Summary string `json:"summary,omitempty"`

	Severity   string `json:"severity,omitempty"`
	CVSSVector string `json:"cvss_vector,omitempty"`

	AffectedRange string `json:"affected_range,omitempty"`
	// Fixed ist die Fix-Version des Zweigs, auf dem die installierte Version
	// liegt. Leer bedeutet: es gibt (noch) keinen Fix in diesem Zweig.
	Fixed string `json:"fixed,omitempty"`
	// FixedCandidates steht nur, wenn sich der Zweig NICHT bestimmen ließ —
	// dann bekommt der Agent alle Fix-Versionen des Advisories und entscheidet
	// selbst, statt eine geraten zu bekommen.
	FixedCandidates []string `json:"fixed_candidates,omitempty"`

	Reference string `json:"reference,omitempty"`
}

// osvVuln ist ein OSV-Datensatz, auf die Felder reduziert, die hier gebraucht
// werden.
type osvVuln struct {
	ID       string   `json:"id"`
	Aliases  []string `json:"aliases"`
	Summary  string   `json:"summary"`
	Details  string   `json:"details"`
	Severity []struct {
		Type  string `json:"type"`
		Score string `json:"score"`
	} `json:"severity"`
	Affected   []osvAffected `json:"affected"`
	References []struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"references"`
	DatabaseSpecific struct {
		Severity string `json:"severity"`
	} `json:"database_specific"`
}

// osvAffected ist ein betroffenes Paket eines Datensatzes.
type osvAffected struct {
	Package struct {
		Name      string `json:"name"`
		Ecosystem string `json:"ecosystem"`
	} `json:"package"`
	Ranges           []osvRange `json:"ranges"`
	Versions         []string   `json:"versions"`
	DatabaseSpecific struct {
		Severity string `json:"severity"`
	} `json:"database_specific"`
}

// osvRange ist ein Versionsbereich. Die Ereignisse sind eine FOLGE, keine
// Menge: auf ein "introduced" folgt das zugehörige "fixed", danach beginnt der
// nächste Zweig. Wer sie als Menge liest, mischt die Fix-Zweige.
type osvRange struct {
	Type   string              `json:"type"`
	Events []map[string]string `json:"events"`
}

type osvClient struct{ http *http.Client }

func newOSV() *osvClient { return &osvClient{http: newHTTPClient()} }

// batch fragt eine Liste von Paketen in Stapeln ab und liefert je Paket die
// Anzahl gefundener Schwachstellen — die Vorauswahl, welche Pakete überhaupt
// eine Detailabfrage wert sind.
func (c *osvClient) batch(ctx context.Context, pkgs []Package) ([]bool, error) {
	hits := make([]bool, len(pkgs))
	for start := 0; start < len(pkgs); start += osvBatchSize {
		end := min(start+osvBatchSize, len(pkgs))
		chunk := pkgs[start:end]

		type queryEntry struct {
			Package struct {
				Name      string `json:"name"`
				Ecosystem string `json:"ecosystem"`
			} `json:"package"`
			Version string `json:"version"`
		}
		body := struct {
			Queries []queryEntry `json:"queries"`
		}{Queries: make([]queryEntry, len(chunk))}
		for i, p := range chunk {
			body.Queries[i].Package.Name = p.Name
			body.Queries[i].Package.Ecosystem = p.Ecosystem
			body.Queries[i].Version = p.Version
		}

		var out struct {
			Results []struct {
				Vulns []struct {
					ID string `json:"id"`
				} `json:"vulns"`
			} `json:"results"`
		}
		if err := postJSON(ctx, c.http, osvBase+"/v1/querybatch", body, &out); err != nil {
			return nil, err
		}
		// Die positionelle Zuordnung ist die einzige Verbindung zwischen
		// Anfrage und Antwort. Stimmt die Länge nicht, ist jede Zuordnung
		// geraten — dann lieber abbrechen als falsch berichten.
		if len(out.Results) != len(chunk) {
			return nil, fmt.Errorf("osv querybatch: %d answers for %d queries — the positional assignment does not hold", len(out.Results), len(chunk))
		}
		for i, r := range out.Results {
			hits[start+i] = len(r.Vulns) > 0
		}
	}
	return hits, nil
}

// query holt die vollständigen Datensätze für ein einzelnes Paket in einer
// bestimmten Version.
func (c *osvClient) query(ctx context.Context, p Package) ([]osvVuln, error) {
	body := map[string]any{
		"package": map[string]string{"name": p.Name, "ecosystem": p.Ecosystem},
		"version": p.Version,
	}
	var out struct {
		Vulns []osvVuln `json:"vulns"`
	}
	if err := postJSON(ctx, c.http, osvBase+"/v1/query", body, &out); err != nil {
		return nil, err
	}
	return out.Vulns, nil
}

// vuln holt einen Datensatz über seine ID (GHSA-…, OSV-…, teils auch CVE-…).
func (c *osvClient) vuln(ctx context.Context, id string) (*osvVuln, error) {
	var v osvVuln
	if err := getJSON(ctx, c.http, osvBase+"/v1/vulns/"+id, nil, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// toFinding formt einen OSV-Datensatz für ein konkretes Paket zum Befund.
func toFinding(p Package, v osvVuln) Finding {
	f := Finding{
		Package: p.Name, Ecosystem: p.Ecosystem, Version: p.Version, Dependency: p.Dependency,
		ID: v.ID, Aliases: v.Aliases, CVE: pickCVE(v), Summary: firstLine(v.Summary, v.Details),
		Severity: strings.ToUpper(v.DatabaseSpecific.Severity),
	}
	for _, s := range v.Severity {
		if strings.HasPrefix(s.Type, "CVSS") && s.Score != "" {
			f.CVSSVector = s.Score
			break
		}
	}
	for _, a := range v.Affected {
		if !strings.EqualFold(a.Package.Name, p.Name) || !strings.EqualFold(a.Package.Ecosystem, p.Ecosystem) {
			continue
		}
		if f.Severity == "" {
			f.Severity = strings.ToUpper(a.DatabaseSpecific.Severity)
		}
		fixed, candidates, rangeText := resolveFix(a.Ranges, p.Version)
		f.Fixed, f.FixedCandidates, f.AffectedRange = fixed, candidates, rangeText
		break
	}
	for _, r := range v.References {
		if r.Type == "ADVISORY" && r.URL != "" {
			f.Reference = r.URL
			break
		}
	}
	if f.Reference == "" {
		f.Reference = "https://osv.dev/vulnerability/" + v.ID
	}
	return f
}

// resolveFix ist der Kern: aus den Intervallen eines Advisories dasjenige
// heraussuchen, in dem die installierte Version liegt, und dessen Fix-Version
// melden. Lässt sich das nicht entscheiden — weil eine der Versionen keine
// Punktversion ist —, werden ALLE Fix-Versionen als Kandidaten gemeldet.
// Lieber eine offene Frage im Ticket als eine falsche Zahl darin.
func resolveFix(ranges []osvRange, version string) (fixed string, candidates []string, rangeText string) {
	var all []string
	var texts []string
	certain := true

	for _, r := range ranges {
		if r.Type == "GIT" {
			continue // Commit-Bereiche sagen über eine Paketversion nichts
		}
		var introduced string
		for _, ev := range r.Events {
			if v, ok := ev["introduced"]; ok {
				introduced = v
				continue
			}
			v, ok := ev["fixed"]
			if !ok {
				if v, ok = ev["last_affected"]; !ok {
					continue
				}
				// last_affected ist die letzte betroffene Version, nicht die
				// erste heile — als Fix-Version taugt sie nicht.
				texts = append(texts, rangeLabel(introduced, "", v))
				introduced = ""
				continue
			}
			all = append(all, v)
			texts = append(texts, rangeLabel(introduced, v, ""))
			if inInterval(version, introduced, v) {
				fixed = v
			}
			introduced = ""
		}
		if introduced != "" {
			texts = append(texts, rangeLabel(introduced, "", ""))
		}
	}
	for _, v := range all {
		if _, ok := compareVersions(version, v); !ok {
			certain = false
		}
	}
	rangeText = strings.Join(texts, ", ")
	if fixed == "" || !certain {
		return fixed, all, rangeText
	}
	return fixed, nil, rangeText
}

// inInterval: liegt version in [introduced, fixed)? Unvergleichbare Versionen
// zählen als „nicht sicher enthalten".
func inInterval(version, introduced, fixed string) bool {
	if introduced != "" && introduced != "0" {
		c, ok := compareVersions(version, introduced)
		if !ok || c < 0 {
			return false
		}
	}
	c, ok := compareVersions(version, fixed)
	return ok && c < 0
}

func rangeLabel(introduced, fixed, lastAffected string) string {
	var b strings.Builder
	if introduced != "" && introduced != "0" {
		fmt.Fprintf(&b, ">=%s ", introduced)
	}
	switch {
	case fixed != "":
		fmt.Fprintf(&b, "<%s", fixed)
	case lastAffected != "":
		fmt.Fprintf(&b, "<=%s", lastAffected)
	default:
		b.WriteString("(no fix known)")
	}
	return strings.TrimSpace(b.String())
}

// pickCVE zieht die CVE aus den Aliassen — sie ist der über alle Quellen
// stabile Bezeichner und damit der Suchschlüssel der Duplikatsprüfung.
func pickCVE(v osvVuln) string {
	if strings.HasPrefix(v.ID, "CVE-") {
		return v.ID
	}
	for _, a := range v.Aliases {
		if strings.HasPrefix(a, "CVE-") {
			return a
		}
	}
	return ""
}

// firstLine liefert eine knappe Zusammenfassung: das Summary-Feld, ersatzweise
// die erste Zeile der Details. Der volle Details-Text ist oft seitenlang und
// gehört nicht in ein Scan-Ergebnis über hunderte Pakete.
func firstLine(summary, details string) string {
	if s := strings.TrimSpace(summary); s != "" {
		return s
	}
	for _, line := range strings.Split(details, "\n") {
		if s := strings.TrimSpace(line); s != "" && !strings.HasPrefix(s, "#") {
			const max = 200
			if len(s) > max {
				return s[:max] + "…"
			}
			return s
		}
	}
	return ""
}
