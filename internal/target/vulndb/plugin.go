// Package vulndb bindet die öffentlichen Schwachstellen-Datenbanken als
// Zielsystem an: OSV.dev als Grundlage für npm, Packagist und Pub, dazu die
// GitHub Advisory Database, NVD und die Paket-Registries.
//
// Warum als Zielsystem und nicht als curl im Sandbox-Shell: erst dadurch
// bekommt der Zugriff, was jeder andere Außenkontakt in Covey hat — einen
// eigenen Guard-Rail-Betreff je Aktion (vulndb:scan_lockfile statt dev:exec),
// eine Aufzeichnung mit Parametern und Ergebnis, und einen gebrokerten
// Schlüssel, der nie als langlebiges Secret in der Sandbox landet. Dazu kommt
// das Naheliegende: Lockfile-Parsen, Stapelbildung und Fix-Zweig-Auswahl sind
// hier einmal geschrieben und getestet, statt in jedem Lauf neu aus einem
// Prompt erzeugt zu werden.
package vulndb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"covey/internal/target"
)

// System bindet die Schwachstellen-Datenbanken an die Target-Registry.
type System struct{}

func init() {
	target.Register(target.Descriptor{
		Name:        "vulndb",
		Label:       "Vulnerability databases",
		Description: "Known vulnerabilities in declared package dependencies: scan a lock file (package-lock.json, composer.lock, pubspec.lock) against OSV.dev in one batch (scan_lockfile), check individual packages (query/query_batch), fetch an advisory's details merged from OSV, the GitHub Advisory Database and NVD (advisory) and look up the next safe version in the registry (latest_version). Covers npm, Packagist (Composer) and Pub (Dart/Flutter). Works without secrets; an NVD API key stored as vulndb_token only raises the rate limit.",
		Kind:        "builtin",
		Category:    target.CategoryDev,
		System:      System{},
		// Alle Quellen sind öffentlich erreichbar. Ein Schlüssel erweitert
		// nur, was erlaubt ist (NVD: 5 → 50 Anfragen je 30 Sekunden) — ohne
		// ihn ist das Plugin langsamer, nicht funktionslos.
		CredentialsOptional: true,
		BaseURLOptional:     true,
		SetupDoc: `1. Activate the plugin here — it works without secrets. All four sources
   (OSV.dev, GitHub Advisory Database, NVD, the package registries) are
   publicly reachable.

2. Enable it in the agent's ACCESS.md:
   - system: vulndb scope: read

3. Release the hosts in the egress allowlist — the actions run in the agent's
   sandbox and therefore go through the egress proxy. The built-in template
   "Vulnerability databases" contains exactly the hosts needed:
   api.osv.dev, osv.dev, api.github.com, services.nvd.nist.gov,
   registry.npmjs.org, packagist.org, repo.packagist.org, pub.dev
   Without them every action fails with a note about the missing host.

4. Optional — raise the rate limit. Only NVD limits noticeably: 5 requests per
   30 seconds anonymously, 50 with a key. Request one at
   https://nvd.nist.gov/developers/request-an-api-key, store it under Secrets
   and assign it to the agent:
   vulndb_token = the NVD API key
   Everything else stays public. The GitHub Advisory Database allows 60
   requests per hour without a token, which is enough for the handful of
   findings of a scan.

Note on scope: the plugin judges the DECLARED dependencies from a lock file.
It replaces neither a SAST scan of your own code nor a container image scan —
and it needs a lock file: a manifest (package.json, composer.json,
pubspec.yaml) declares ranges, and a range is not a fact.

yarn.lock and pnpm-lock.yaml are deliberately not parsed (their formats change
between major versions; a half-read lock file reports fewer packages than
there are). Extract the name/version pairs from them yourself and send them
through the action query_batch.`,
	})
}

func (System) Name() string { return "vulndb" }

// ActionSubject: eine Aktion, ein Betreff. Alle Aktionen sind lesend — die
// Trennung dient der Nachvollziehbarkeit und erlaubt, den teuren Vollscan
// getrennt von der billigen Einzelabfrage zu regeln.
func (System) ActionSubject(action string, _ json.RawMessage) string {
	return "vulndb:" + action
}

// maxLockfileSize begrenzt, was gelesen wird. Ein package-lock.json eines
// großen Monorepos liegt im zweistelligen Megabyte-Bereich; darüber ist es
// keine Lockdatei mehr, sondern ein Versehen.
const maxLockfileSize = 64 << 20

func (System) Execute(ctx context.Context, action string, params json.RawMessage, cred target.Credential) (any, error) {
	var in struct {
		Path      string    `json:"path"`
		Ecosystem string    `json:"ecosystem"`
		Name      string    `json:"name"`
		Version   string    `json:"version"`
		ID        string    `json:"id"`
		Packages  []Package `json:"packages"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &in); err != nil {
			return nil, fmt.Errorf("params: %w", err)
		}
	}

	switch action {
	case "scan_lockfile":
		return scanLockfile(ctx, target.Workdir(ctx), in.Path)
	case "query_batch":
		return queryPackages(ctx, in.Packages)
	case "query":
		eco, err := normalizeEcosystem(in.Ecosystem)
		if err != nil {
			return nil, err
		}
		return queryPackages(ctx, []Package{{Ecosystem: eco, Name: in.Name, Version: in.Version}})
	case "advisory":
		return lookupAdvisory(ctx, in.ID, cred.Token)
	case "latest_version":
		eco, err := normalizeEcosystem(in.Ecosystem)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.Name) == "" {
			return nil, fmt.Errorf("name missing")
		}
		return fetchVersions(ctx, newHTTPClient(), eco, strings.TrimSpace(in.Name))
	default:
		return nil, fmt.Errorf("unknown action %q", strings.TrimSpace(action))
	}
}

// ScanResult ist das Ergebnis von scan_lockfile.
type ScanResult struct {
	Lockfile        string    `json:"lockfile"`
	Ecosystem       string    `json:"ecosystem"`
	PackagesScanned int       `json:"packages_scanned"`
	Findings        []Finding `json:"findings"`
	// Clean sagt ausdrücklich, dass geprüft und nichts gefunden wurde — das
	// ist etwas anderes als eine leere Liste, weil nichts geprüft werden
	// konnte.
	Clean bool     `json:"clean"`
	Notes []string `json:"notes,omitempty"`
}

func scanLockfile(ctx context.Context, workdir, path string) (*ScanResult, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("path missing — name the lock file, e.g. \"repo/package-lock.json\"")
	}
	full := path
	if !filepath.IsAbs(full) {
		full = filepath.Join(workdir, path)
	}
	info, err := os.Stat(full)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if info.Size() > maxLockfileSize {
		return nil, fmt.Errorf("%s is %d MB — that is no longer a lock file", path, info.Size()>>20)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	parsed, err := ParseLockfile(full, data)
	if err != nil {
		return nil, err
	}
	res := &ScanResult{
		Lockfile: path, Ecosystem: parsed.Ecosystem,
		PackagesScanned: len(parsed.Packages), Notes: parsed.Notes,
	}

	findings, err := queryPackages(ctx, parsed.Packages)
	if err != nil {
		return nil, err
	}
	res.Findings = findings.Findings
	res.Notes = append(res.Notes, findings.Notes...)
	res.Clean = len(res.Findings) == 0
	return res, nil
}

// BatchResult ist das Ergebnis von query/query_batch.
type BatchResult struct {
	PackagesScanned int       `json:"packages_scanned"`
	Findings        []Finding `json:"findings"`
	Clean           bool      `json:"clean"`
	Notes           []string  `json:"notes,omitempty"`
}

// queryPackages ist der gemeinsame Kern: erst ein Stapel über alle Pakete (der
// sagt, WELCHE betroffen sind), dann eine Detailabfrage nur für die
// betroffenen. Bei einem üblichen Projekt sind das wenige Prozent — der
// umgekehrte Weg wäre ein Vielfaches an Aufrufen.
func queryPackages(ctx context.Context, pkgs []Package) (*BatchResult, error) {
	res := &BatchResult{}
	clean := make([]Package, 0, len(pkgs))
	for _, p := range pkgs {
		eco, err := normalizeEcosystem(p.Ecosystem)
		if err != nil {
			return nil, err
		}
		p.Ecosystem = eco
		p.Name = strings.TrimSpace(p.Name)
		p.Version = strings.TrimSpace(p.Version)
		if p.Name == "" || p.Version == "" {
			return nil, fmt.Errorf("package without a name or version: %+v", p)
		}
		clean = append(clean, p)
	}
	if len(clean) == 0 {
		return nil, fmt.Errorf("no packages given")
	}
	res.PackagesScanned = len(clean)

	osv := newOSV()
	hits, err := osv.batch(ctx, clean)
	if err != nil {
		return nil, err
	}
	for i, hit := range hits {
		if !hit {
			continue
		}
		vulns, err := osv.query(ctx, clean[i])
		if err != nil {
			// Ein einzelner Fehlschlag darf den Scan nicht wertlos machen —
			// aber er darf auch nicht als „sauber" durchgehen. Deshalb als
			// Notiz, die im Bericht landet.
			res.Notes = append(res.Notes, fmt.Sprintf("%s@%s: details not fetchable (%v) — the package IS affected, the finding is incomplete", clean[i].Name, clean[i].Version, err))
			continue
		}
		for _, v := range vulns {
			res.Findings = append(res.Findings, toFinding(clean[i], v))
		}
	}
	res.Clean = len(res.Findings) == 0 && len(res.Notes) == 0
	return res, nil
}

// lookupAdvisory führt die Detailansicht aus allen Quellen zusammen, die
// antworten. Fällt eine aus, steht das als Notiz im Ergebnis — ein fehlender
// CVSS-Wert ist etwas anderes als ein CVSS-Wert von 0.
func lookupAdvisory(ctx context.Context, id, nvdKey string) (*Advisory, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("id missing — expected CVE-…, GHSA-… or an OSV ID")
	}
	client := newHTTPClient()
	var notes []string

	osvRec, err := newOSV().vuln(ctx, id)
	if err != nil {
		osvRec = nil
		notes = append(notes, sourceNote("osv", err))
	}

	// Die GHSA-ID steht entweder direkt in der Anfrage oder in den Aliassen
	// des OSV-Datensatzes.
	ghsaID := ""
	if strings.HasPrefix(id, "GHSA-") {
		ghsaID = id
	} else if osvRec != nil {
		if strings.HasPrefix(osvRec.ID, "GHSA-") {
			ghsaID = osvRec.ID
		} else {
			for _, a := range osvRec.Aliases {
				if strings.HasPrefix(a, "GHSA-") {
					ghsaID = a
					break
				}
			}
		}
	}
	cve := id
	if !strings.HasPrefix(cve, "CVE-") {
		cve = ""
		if osvRec != nil {
			cve = pickCVE(*osvRec)
		}
	}

	var ghsa *ghsaAdvisory
	switch {
	case ghsaID != "":
		ghsa, err = fetchGHSA(ctx, client, ghsaID)
	case cve != "":
		ghsa, err = fetchGHSAByCVE(ctx, client, cve)
	default:
		err = errNotFound
	}
	if err != nil {
		ghsa = nil
		notes = append(notes, sourceNote("ghsa", err))
	}
	if cve == "" && ghsa != nil {
		cve = ghsa.CVEID
	}

	var score float64
	var severity, vector string
	if cve != "" {
		score, severity, vector, err = fetchNVD(ctx, client, cve, nvdKey)
		if err != nil {
			notes = append(notes, sourceNote("nvd", err))
		}
	} else {
		notes = append(notes, "nvd: not queried — no CVE number known for this advisory")
	}

	if osvRec == nil && ghsa == nil && vector == "" {
		return nil, fmt.Errorf("%s: no source answered — %s", id, strings.Join(notes, "; "))
	}
	a := mergeAdvisory(id, osvRec, ghsa, score, severity, vector)
	a.Notes = notes
	if nvdKey == "" {
		a.Notes = append(a.Notes, "no vulndb_token stored — NVD answers at the public rate limit (5 requests/30 s)")
	}
	return &a, nil
}

func (System) PromptDoc() string {
	return `Available vulndb actions — the known vulnerabilities of declared package dependencies.
   Covered ecosystems: npm, Packagist (Composer/PHP), Pub (Dart/Flutter). Write them exactly like
   that or in their common form (composer/php, dart/flutter) — the plugin normalises it.
   scan_lockfile {"path":"<the path to the lock file, relative to your home or absolute>"} —
   THE main action. Reads package-lock.json, npm-shrinkwrap.json, composer.lock or pubspec.lock,
   determines the installed versions and matches ALL of them against OSV.dev in one batch. The result:
   {"lockfile":…,"ecosystem":…,"packages_scanned":812,"clean":false,"findings":[…],"notes":[…]}.
   Use it after a checkout — the path is the checkout path plus the lock file. A monorepo has several;
   scan each one.
   Every finding carries what a ticket needs: package, version, dependency (direct/dev/transitive),
   id, cve, summary, severity, cvss_vector, affected_range, fixed and reference.
   READ "fixed" CAREFULLY: it is the fix version of the branch YOUR version sits on. An advisory often
   has several (2.4.5 for the 2.x line, 3.1.2 for 3.x) — the plugin picks the fitting one. If it stands
   empty and "fixed_candidates" is filled, the branch could NOT be determined (an unusual version
   scheme): then name the candidates in the ticket instead of picking one yourself.
   "clean":true means checked and nothing found. An empty findings list WITH notes means the opposite —
   something could not be checked. Never report that as an all-clear.
   query_batch {"packages":[{"ecosystem":"npm","name":"lodash","version":"4.17.20"},…]} — the same
   check for name/version pairs you determined yourself. That is the way for yarn.lock and
   pnpm-lock.yaml (they are deliberately not parsed: their format changes between major versions, and a
   half-read lock file reports fewer packages than there are) — pull the pairs out with the dev tool
   and send them through here.
   query {"ecosystem":"npm","name":"lodash","version":"4.17.20"} — a single package, the same result.
   advisory {"id":"CVE-2021-23337"} — the details of ONE advisory, merged from OSV, the GitHub Advisory
   Database and NVD: description, severity, cvss_vector, cvss_score, the affected packages with their
   first patched version, references. Also takes GHSA-… and OSV IDs. "sources" says who answered,
   "notes" who did not — a missing CVSS score is something other than a score of 0.
   Take it for every finding that is to become a ticket; scan_lockfile stays deliberately terse so that
   a scan over hundreds of packages does not flood your context.
   latest_version {"ecosystem":"npm","name":"lodash"} — the registry's version list: does the proposed
   fix version exist at all, and what has appeared since? Use it before you propose an upgrade.
   What this system does NOT do: it judges the DECLARED dependencies of a lock file. It finds neither
   vulnerabilities in your own code nor ones in container images, and without a lock file it cannot
   work — a manifest declares ranges, and a range is not a fact. If a project has no lock file, that is
   a finding of its own, not a clean result.
   Every call goes out through the egress proxy. If an action reports a host as unreachable, that host
   is missing from the allowlist — name it in your answer instead of retrying.`
}
