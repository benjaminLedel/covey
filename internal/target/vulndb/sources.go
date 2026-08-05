package vulndb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// Die drei Quellen neben OSV. Jede hat genau eine Aufgabe, und sie sind NICHT
// austauschbar:
//
//   - GitHub Advisory Database: die konkreteste Fix-Aussage
//     (first_patched_version) und eine brauchbare Beschreibung. Öffentlich
//     ohne Token (60 Anfragen/Stunde), was für die Handvoll Befunde eines
//     Scans reicht.
//   - NVD: der CVSS-Vektor, wenn ihn sonst niemand mitliefert. Paket-BLIND —
//     NVD identifiziert über CPE, nicht über Paketnamen; als Quelle für
//     „ist diese Version betroffen?" ist es deshalb ungeeignet.
//   - Die Registries: ob es die genannte Fix-Version überhaupt gibt und was
//     seither erschienen ist.

// Basisadressen als Variablen — wie bei OSV nur, damit die Tests einen
// httptest-Server dazwischenschieben können. Zur Laufzeit unverändert.
var (
	ghsaBase     = "https://api.github.com"
	nvdBase      = "https://services.nvd.nist.gov"
	npmBase      = "https://registry.npmjs.org"
	packagistAPI = "https://repo.packagist.org"
	pubBase      = "https://pub.dev"
)

// Advisory ist die zusammengeführte Detailansicht einer Schwachstelle über
// alle Quellen, die geantwortet haben.
type Advisory struct {
	ID          string   `json:"id"`
	CVE         string   `json:"cve,omitempty"`
	Aliases     []string `json:"aliases,omitempty"`
	Summary     string   `json:"summary,omitempty"`
	Description string   `json:"description,omitempty"`

	Severity   string  `json:"severity,omitempty"`
	CVSSVector string  `json:"cvss_vector,omitempty"`
	CVSSScore  float64 `json:"cvss_score,omitempty"`

	Affected   []AdvisoryPackage `json:"affected,omitempty"`
	References []string          `json:"references,omitempty"`

	// Sources nennt, welche Quellen geantwortet haben, Notes warum eine
	// gefehlt hat. Beides gehört ins Ergebnis: ein Agent, der die Herkunft
	// nicht kennt, kann sie nicht ins Ticket schreiben — und ein fehlender
	// CVSS-Wert ist etwas anderes als ein CVSS-Wert von 0.
	Sources []string `json:"sources"`
	Notes   []string `json:"notes,omitempty"`
}

// AdvisoryPackage ist ein betroffenes Paket laut Advisory.
type AdvisoryPackage struct {
	Ecosystem       string `json:"ecosystem"`
	Name            string `json:"name"`
	VulnerableRange string `json:"vulnerable_range,omitempty"`
	FirstPatched    string `json:"first_patched,omitempty"`
}

// --- GitHub Advisory Database ---

type ghsaAdvisory struct {
	GHSAID      string `json:"ghsa_id"`
	CVEID       string `json:"cve_id"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	Severity    string `json:"severity"`
	HTMLURL     string `json:"html_url"`
	CVSS        struct {
		VectorString string  `json:"vector_string"`
		Score        float64 `json:"score"`
	} `json:"cvss"`
	Vulnerabilities []struct {
		Package struct {
			Ecosystem string `json:"ecosystem"`
			Name      string `json:"name"`
		} `json:"package"`
		VulnerableVersionRange string `json:"vulnerable_version_range"`
		// first_patched_version wird von der API mal als Zeichenkette, mal als
		// Objekt {"identifier":"…"} geliefert. Beides wird akzeptiert — ein
		// Plugin, das an dieser Stelle bricht, verliert genau die Angabe, für
		// die man GHSA überhaupt fragt.
		FirstPatchedVersion json.RawMessage `json:"first_patched_version"`
	} `json:"vulnerabilities"`
	References []string `json:"references"`
}

// firstPatched entpackt beide Bauformen.
func firstPatched(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var obj struct {
		Identifier string `json:"identifier"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return obj.Identifier
	}
	return ""
}

func ghsaHeader() map[string]string {
	return map[string]string{
		"Accept":               "application/vnd.github+json",
		"X-GitHub-Api-Version": "2022-11-28",
	}
}

// fetchGHSA holt ein Advisory über seine GHSA-ID.
func fetchGHSA(ctx context.Context, c *http.Client, id string) (*ghsaAdvisory, error) {
	var a ghsaAdvisory
	if err := getJSON(ctx, c, ghsaBase+"/advisories/"+url.PathEscape(id), ghsaHeader(), &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// fetchGHSAByCVE sucht das Advisory zu einer CVE-Nummer.
func fetchGHSAByCVE(ctx context.Context, c *http.Client, cve string) (*ghsaAdvisory, error) {
	var list []ghsaAdvisory
	u := ghsaBase + "/advisories?cve_id=" + url.QueryEscape(cve)
	if err := getJSON(ctx, c, u, ghsaHeader(), &list); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, errNotFound
	}
	return &list[0], nil
}

// --- NVD ---

// fetchNVD holt Bewertung und Vektor zu einer CVE. apiKey darf leer sein —
// dann gilt das öffentliche Limit von 5 Anfragen je 30 Sekunden.
func fetchNVD(ctx context.Context, c *http.Client, cve, apiKey string) (score float64, severity, vector string, err error) {
	header := map[string]string{}
	if apiKey != "" {
		header["apiKey"] = apiKey
	}
	var out struct {
		Vulnerabilities []struct {
			CVE struct {
				Metrics map[string][]struct {
					CVSSData struct {
						BaseScore    float64 `json:"baseScore"`
						BaseSeverity string  `json:"baseSeverity"`
						VectorString string  `json:"vectorString"`
					} `json:"cvssData"`
					BaseSeverity string `json:"baseSeverity"`
				} `json:"metrics"`
			} `json:"cve"`
		} `json:"vulnerabilities"`
	}
	u := nvdBase + "/rest/json/cves/2.0?cveId=" + url.QueryEscape(cve)
	if err := getJSON(ctx, c, u, header, &out); err != nil {
		return 0, "", "", err
	}
	if len(out.Vulnerabilities) == 0 {
		return 0, "", "", errNotFound
	}
	// Die neueste CVSS-Fassung zuerst — eine ältere überschreibt sie nicht.
	metrics := out.Vulnerabilities[0].CVE.Metrics
	for _, key := range []string{"cvssMetricV40", "cvssMetricV31", "cvssMetricV30", "cvssMetricV2"} {
		for _, m := range metrics[key] {
			if m.CVSSData.VectorString == "" {
				continue
			}
			sev := m.CVSSData.BaseSeverity
			if sev == "" {
				sev = m.BaseSeverity
			}
			return m.CVSSData.BaseScore, strings.ToUpper(sev), m.CVSSData.VectorString, nil
		}
	}
	return 0, "", "", errNotFound
}

// --- Registries ---

// VersionInfo ist die Antwort der Aktion latest_version: gibt es die
// vorgeschlagene Fix-Version, und was ist seither erschienen?
type VersionInfo struct {
	Ecosystem string   `json:"ecosystem"`
	Name      string   `json:"name"`
	Latest    string   `json:"latest,omitempty"`
	Versions  []string `json:"versions,omitempty"`
	Truncated bool     `json:"truncated,omitempty"`
}

// maxVersions kappt die Versionsliste. Ein altes Paket hat hunderte Versionen;
// für die Frage „gibt es 4.17.21, und was kam danach?" reichen die neuesten.
const maxVersions = 40

func fetchVersions(ctx context.Context, c *http.Client, ecosystem, name string) (VersionInfo, error) {
	info := VersionInfo{Ecosystem: ecosystem, Name: name}
	switch ecosystem {
	case EcosystemNPM:
		var out struct {
			DistTags map[string]string          `json:"dist-tags"`
			Versions map[string]json.RawMessage `json:"versions"`
		}
		// Das abgekürzte Format spart bei großen Paketen zweistellige
		// Megabyte — die vollen Metadaten enthalten jede README jeder Version.
		header := map[string]string{"Accept": "application/vnd.npm.install-v1+json"}
		if err := getJSON(ctx, c, npmBase+"/"+npmPath(name), header, &out); err != nil {
			return info, err
		}
		info.Latest = out.DistTags["latest"]
		for v := range out.Versions {
			info.Versions = append(info.Versions, v)
		}
	case EcosystemPackagist:
		if !strings.Contains(name, "/") {
			return info, fmt.Errorf("composer packages are named vendor/package — %q is missing the vendor", name)
		}
		var out struct {
			Packages map[string][]struct {
				Version string `json:"version"`
			} `json:"packages"`
		}
		if err := getJSON(ctx, c, packagistAPI+"/p2/"+name+".json", nil, &out); err != nil {
			return info, err
		}
		for _, rel := range out.Packages[name] {
			info.Versions = append(info.Versions, strings.TrimPrefix(rel.Version, "v"))
		}
	case EcosystemPub:
		var out struct {
			Latest struct {
				Version string `json:"version"`
			} `json:"latest"`
			Versions []struct {
				Version string `json:"version"`
			} `json:"versions"`
		}
		if err := getJSON(ctx, c, pubBase+"/api/packages/"+url.PathEscape(name), nil, &out); err != nil {
			return info, err
		}
		info.Latest = out.Latest.Version
		for _, v := range out.Versions {
			info.Versions = append(info.Versions, v.Version)
		}
	default:
		return info, fmt.Errorf("unknown ecosystem %q", ecosystem)
	}

	sortVersions(info.Versions)
	if info.Latest == "" && len(info.Versions) > 0 {
		info.Latest = info.Versions[len(info.Versions)-1]
	}
	if len(info.Versions) > maxVersions {
		info.Versions = info.Versions[len(info.Versions)-maxVersions:]
		info.Truncated = true
	}
	return info, nil
}

// npmPath kodiert einen npm-Namen für den Pfad. Ein scoped package trägt einen
// Schrägstrich im Namen (@babel/core) — unkodiert wäre das ein Pfadsegment
// mehr und die Anfrage ginge ins Leere.
func npmPath(name string) string {
	if scope, pkg, ok := strings.Cut(name, "/"); ok {
		return url.PathEscape(scope) + "%2f" + url.PathEscape(pkg)
	}
	return url.PathEscape(name)
}

// sortVersions sortiert aufsteigend. Was sich nicht als Punktversion lesen
// lässt (dev-Branches), wandert ans Ende — es ist keine Freigabe.
func sortVersions(versions []string) {
	sort.SliceStable(versions, func(i, j int) bool {
		if c, ok := compareVersions(versions[i], versions[j]); ok {
			return c < 0
		}
		_, _, okI := splitVersion(versions[i])
		_, _, okJ := splitVersion(versions[j])
		return okI && !okJ
	})
}

// mergeAdvisory führt zusammen, was die Quellen geliefert haben. Reihenfolge
// der Vorrangigkeit: OSV bestimmt die Struktur, GHSA ergänzt Fix-Versionen und
// Beschreibung, NVD nur den Bewertungsvektor, wenn er sonst fehlt.
func mergeAdvisory(id string, osv *osvVuln, ghsa *ghsaAdvisory, nvdScore float64, nvdSeverity, nvdVector string) Advisory {
	a := Advisory{ID: id}

	if osv != nil {
		a.ID = osv.ID
		a.Aliases = osv.Aliases
		a.CVE = pickCVE(*osv)
		a.Summary = osv.Summary
		a.Description = osv.Details
		a.Severity = strings.ToUpper(osv.DatabaseSpecific.Severity)
		for _, s := range osv.Severity {
			if strings.HasPrefix(s.Type, "CVSS") && s.Score != "" {
				a.CVSSVector = s.Score
				break
			}
		}
		for _, aff := range osv.Affected {
			_, candidates, rangeText := resolveFix(aff.Ranges, "")
			ap := AdvisoryPackage{
				Ecosystem: aff.Package.Ecosystem, Name: aff.Package.Name,
				VulnerableRange: rangeText,
			}
			if len(candidates) > 0 {
				ap.FirstPatched = candidates[0]
			}
			a.Affected = append(a.Affected, ap)
		}
		for _, r := range osv.References {
			a.References = append(a.References, r.URL)
		}
		a.Sources = append(a.Sources, "osv")
	}

	if ghsa != nil {
		if a.CVE == "" {
			a.CVE = ghsa.CVEID
		}
		if a.Summary == "" {
			a.Summary = ghsa.Summary
		}
		if a.Description == "" {
			a.Description = ghsa.Description
		}
		if a.Severity == "" {
			a.Severity = strings.ToUpper(ghsa.Severity)
		}
		if a.CVSSVector == "" {
			a.CVSSVector = ghsa.CVSS.VectorString
		}
		if a.CVSSScore == 0 {
			a.CVSSScore = ghsa.CVSS.Score
		}
		// GHSA nennt die erste heile Version je Paket — die konkreteste
		// Fix-Aussage überhaupt. Sie ergänzt bestehende Einträge und legt
		// fehlende an.
		for _, v := range ghsa.Vulnerabilities {
			patched := firstPatched(v.FirstPatchedVersion)
			found := false
			for i := range a.Affected {
				if strings.EqualFold(a.Affected[i].Name, v.Package.Name) {
					if a.Affected[i].FirstPatched == "" {
						a.Affected[i].FirstPatched = patched
					}
					if a.Affected[i].VulnerableRange == "" {
						a.Affected[i].VulnerableRange = v.VulnerableVersionRange
					}
					found = true
					break
				}
			}
			if !found {
				a.Affected = append(a.Affected, AdvisoryPackage{
					Ecosystem: v.Package.Ecosystem, Name: v.Package.Name,
					VulnerableRange: v.VulnerableVersionRange, FirstPatched: patched,
				})
			}
		}
		if ghsa.HTMLURL != "" {
			a.References = append(a.References, ghsa.HTMLURL)
		}
		a.Sources = append(a.Sources, "ghsa")
	}

	if nvdVector != "" {
		if a.CVSSVector == "" {
			a.CVSSVector = nvdVector
		}
		if a.CVSSScore == 0 {
			a.CVSSScore = nvdScore
		}
		if a.Severity == "" {
			a.Severity = nvdSeverity
		}
		a.Sources = append(a.Sources, "nvd")
	}

	a.References = dedupe(a.References)
	return a
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// sourceNote formt die Notiz zu einer Quelle, die nicht geantwortet hat.
func sourceNote(source string, err error) string {
	if errors.Is(err, errNotFound) {
		return source + ": does not know this advisory"
	}
	return source + ": " + err.Error()
}
