package vulndb

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Lockfile-Parser für die drei Ökosysteme, die dieses Plugin abdeckt. Gelesen
// wird IMMER die Lockdatei, nie das Manifest: package.json, composer.json und
// pubspec.yaml deklarieren Bereiche ("^4.17.0"), und ein Bereich ist keine
// Tatsache. Nur die Lockdatei sagt, welche Version wirklich installiert ist —
// und nur mit einer exakten Version lässt sich eine Schwachstelle zuordnen.
//
// Bewusst NICHT unterstützt: yarn.lock und pnpm-lock.yaml. Beide haben ihr
// Format über Major-Versionen hinweg mehrfach gewechselt (yarn classic vs.
// berry, pnpm v6/v9), und ein Parser, der die falsche Variante still
// halb-liest, meldet weniger Pakete als da sind — das ist schlimmer als eine
// klare Absage. Wer sie braucht, zieht die Name/Version-Paare selbst heraus und
// schickt sie durch die Aktion query_batch; die ist genau dafür da.

// Package ist ein installiertes Paket aus einer Lockdatei.
type Package struct {
	Ecosystem string `json:"ecosystem"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	// Dependency ist die Rolle im Abhängigkeitsbaum, soweit die Lockdatei sie
	// hergibt: "direct", "dev" oder "transitive". Leer, wenn das Format sie
	// nicht unterscheidet — dann ist "unbekannt" die ehrliche Antwort.
	Dependency string `json:"dependency,omitempty"`
}

// Rollen im Abhängigkeitsbaum.
const (
	depDirect     = "direct"
	depDev        = "dev"
	depTransitive = "transitive"
)

// LockfileResult ist das Ergebnis des Parsens: die Pakete plus das, was dabei
// übersprungen werden musste. Die Hinweise sind kein Beiwerk — ein Paket aus
// einem git-Repo hat keine Datenbank-Entsprechung, und der Agent muss das als
// Lücke melden können statt als Entwarnung.
type LockfileResult struct {
	Ecosystem string    `json:"ecosystem"`
	Packages  []Package `json:"packages"`
	Notes     []string  `json:"notes,omitempty"`
}

// ParseLockfile erkennt das Format am Dateinamen und liest die installierten
// Pakete heraus.
func ParseLockfile(name string, data []byte) (LockfileResult, error) {
	switch base := strings.ToLower(filepath.Base(name)); base {
	case "package-lock.json", "npm-shrinkwrap.json":
		return parseNpmLock(data)
	case "composer.lock":
		return parseComposerLock(data)
	case "pubspec.lock":
		return parsePubspecLock(data)
	case "yarn.lock", "pnpm-lock.yaml", "pnpm-lock.yml":
		return LockfileResult{}, fmt.Errorf("%s is not supported — its format changes between major versions and a half-read lock file reports fewer packages than there are. Extract the name/version pairs yourself and send them through query_batch", base)
	default:
		return LockfileResult{}, fmt.Errorf("unknown lock file %q — supported: package-lock.json, npm-shrinkwrap.json, composer.lock, pubspec.lock", base)
	}
}

// --- npm ---

// parseNpmLock liest package-lock.json in beiden Bauformen: lockfileVersion 2/3
// mit der flachen Karte `packages`, lockfileVersion 1 mit dem verschachtelten
// `dependencies`.
func parseNpmLock(data []byte) (LockfileResult, error) {
	var doc struct {
		LockfileVersion int `json:"lockfileVersion"`
		Packages        map[string]struct {
			Version         string          `json:"version"`
			Dev             bool            `json:"dev"`
			Link            bool            `json:"link"`
			Dependencies    map[string]any  `json:"dependencies"`
			DevDependencies map[string]any  `json:"devDependencies"`
			Resolved        json.RawMessage `json:"resolved"`
		} `json:"packages"`
		Dependencies map[string]npmV1Entry `json:"dependencies"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return LockfileResult{}, fmt.Errorf("package-lock.json: %w", err)
	}

	res := LockfileResult{Ecosystem: EcosystemNPM}
	seen := map[string]bool{}
	add := func(name, version, dep string) {
		if name == "" || version == "" {
			return
		}
		key := name + "@" + version
		if seen[key] {
			return
		}
		seen[key] = true
		res.Packages = append(res.Packages, Package{
			Ecosystem: EcosystemNPM, Name: name, Version: version, Dependency: dep,
		})
	}

	if len(doc.Packages) > 0 {
		// Der Wurzeleintrag (leerer Schlüssel) ist das Projekt selbst. Er ist
		// aber die einzige Stelle, an der steht, welche Pakete DIREKT verlangt
		// werden — die flache Karte kennt den Unterschied sonst nicht mehr.
		direct := map[string]string{}
		if root, ok := doc.Packages[""]; ok {
			for name := range root.Dependencies {
				direct[name] = depDirect
			}
			for name := range root.DevDependencies {
				direct[name] = depDev
			}
		}
		for path, entry := range doc.Packages {
			if path == "" || entry.Link {
				continue // das Projekt selbst bzw. ein Workspace-Symlink
			}
			idx := strings.LastIndex(path, "node_modules/")
			if idx < 0 {
				continue
			}
			name := path[idx+len("node_modules/"):]
			dep := depTransitive
			if entry.Dev {
				dep = depDev
			}
			if d, ok := direct[name]; ok {
				dep = d
			}
			add(name, entry.Version, dep)
		}
	} else {
		var walk func(map[string]npmV1Entry)
		walk = func(m map[string]npmV1Entry) {
			for name, e := range m {
				dep := depTransitive
				if e.Dev {
					dep = depDev
				}
				add(name, e.Version, dep)
				walk(e.Dependencies)
			}
		}
		walk(doc.Dependencies)
		if len(res.Packages) > 0 {
			res.Notes = append(res.Notes, "lockfileVersion 1 — the lock file does not distinguish direct from transitive dependencies; the roles are a guess")
		}
	}

	if len(res.Packages) == 0 {
		return res, fmt.Errorf("package-lock.json contains no packages")
	}
	sortPackages(res.Packages)
	return res, nil
}

// npmV1Entry ist ein Knoten des verschachtelten Baums von lockfileVersion 1.
type npmV1Entry struct {
	Version      string                `json:"version"`
	Dev          bool                  `json:"dev"`
	Dependencies map[string]npmV1Entry `json:"dependencies"`
}

// --- Composer ---

func parseComposerLock(data []byte) (LockfileResult, error) {
	var doc struct {
		Packages    []composerPkg `json:"packages"`
		PackagesDev []composerPkg `json:"packages-dev"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return LockfileResult{}, fmt.Errorf("composer.lock: %w", err)
	}
	res := LockfileResult{Ecosystem: EcosystemPackagist}
	collect := func(list []composerPkg, dep string) {
		for _, p := range list {
			// Das führende "v" ist Schreibweise des Tags, nicht Teil der
			// Version — Packagist und damit OSV kennen "2.3.0", nicht "v2.3.0".
			version := strings.TrimPrefix(strings.TrimSpace(p.Version), "v")
			if p.Name == "" || version == "" {
				continue
			}
			res.Packages = append(res.Packages, Package{
				Ecosystem: EcosystemPackagist, Name: p.Name, Version: version, Dependency: dep,
			})
		}
	}
	// composer.lock listet den aufgelösten Baum flach; direkt und transitiv
	// stehen beide in `packages`. Unterscheidbar ist nur Produktion vs. dev.
	collect(doc.Packages, "")
	collect(doc.PackagesDev, depDev)

	if len(res.Packages) == 0 {
		return res, fmt.Errorf("composer.lock contains no packages")
	}
	res.Notes = append(res.Notes, "composer.lock lists the resolved tree flat — direct and transitive are not distinguishable in it; use `composer why <package>` in the checkout for the path")
	sortPackages(res.Packages)
	return res, nil
}

type composerPkg struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// --- Dart / Flutter ---

// parsePubspecLock liest pubspec.lock. Die Datei ist YAML, aber von einem
// Generator geschrieben und deshalb streng regelmäßig: Paketnamen auf Einzug 2,
// ihre Felder auf Einzug 4. Ein YAML-Parser als Abhängigkeit wäre für diese
// eine Datei unverhältnismäßig — der zeilenweise Weg ist überschaubar und
// testbar, und er scheitert laut statt still, wenn die Struktur nicht passt.
func parsePubspecLock(data []byte) (LockfileResult, error) {
	res := LockfileResult{Ecosystem: EcosystemPub}
	var (
		inPackages bool
		current    string
		version    string
		source     string
		dependency string
		skipped    int
	)
	flush := func() {
		if current == "" {
			return
		}
		// Nur `source: hosted` kommt von pub.dev und hat damit überhaupt eine
		// Entsprechung in den Datenbanken. git-, path- und sdk-Abhängigkeiten
		// werden gezählt und gemeldet, nicht stillschweigend übergangen.
		if source != "hosted" {
			skipped++
		} else if version != "" {
			res.Packages = append(res.Packages, Package{
				Ecosystem: EcosystemPub, Name: current, Version: version, Dependency: dependency,
			})
		}
		current, version, source, dependency = "", "", "", ""
	}

	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		trimmed := strings.TrimSpace(line)

		if indent == 0 {
			flush()
			inPackages = trimmed == "packages:"
			continue
		}
		if !inPackages {
			continue
		}
		switch indent {
		case 2:
			flush()
			current = strings.TrimSuffix(trimmed, ":")
		case 4:
			key, value, ok := strings.Cut(trimmed, ":")
			if !ok {
				continue
			}
			value = strings.Trim(strings.TrimSpace(value), `"'`)
			switch key {
			case "version":
				version = value
			case "source":
				source = value
			case "dependency":
				switch {
				case strings.Contains(value, "dev"):
					dependency = depDev
				case strings.Contains(value, "direct"):
					dependency = depDirect
				default:
					dependency = depTransitive
				}
			}
		}
		// Einzug 6 und tiefer ist der description-Block — für uns ohne Belang.
	}
	flush()

	if len(res.Packages) == 0 {
		return res, fmt.Errorf("pubspec.lock contains no hosted packages")
	}
	if skipped > 0 {
		res.Notes = append(res.Notes, fmt.Sprintf("%d packages skipped (source git/path/sdk) — they do not come from pub.dev and have no entry in the databases", skipped))
	}
	sortPackages(res.Packages)
	return res, nil
}

// sortPackages sorgt für eine stabile Reihenfolge: Go-Kartenläufe sind zufällig,
// und ein Ergebnis, das bei jedem Lauf anders sortiert ist, macht jeden Diff
// eines Berichts unlesbar.
func sortPackages(pkgs []Package) {
	sort.Slice(pkgs, func(i, j int) bool {
		if pkgs[i].Name != pkgs[j].Name {
			return pkgs[i].Name < pkgs[j].Name
		}
		return pkgs[i].Version < pkgs[j].Version
	})
}
