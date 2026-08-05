package vulndb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"covey/internal/target"
)

// Das Plugin muss sich selbst in die Registry eintragen — und mit den
// Eigenschaften, auf denen der Broker aufbaut. CredentialsOptional ist keine
// Kosmetik: ohne die Angabe verweigert der Broker jeden Aufruf, solange kein
// vulndb_token hinterlegt ist, und das Plugin funktioniert ohne einen.
func TestRegistered(t *testing.T) {
	d, ok := target.Describe("vulndb")
	if !ok {
		t.Fatal("vulndb is not in the registry")
	}
	if !d.CredentialsOptional {
		t.Error("CredentialsOptional must be set — the plugin works without a secret")
	}
	if d.NoCredentials {
		t.Error("NoCredentials must NOT be set — a stored NVD key has to reach the plugin")
	}
	if d.SetupDoc == "" || d.System == nil {
		t.Error("setup doc and implementation belong in the descriptor")
	}
	if !strings.Contains(d.System.PromptDoc(), "scan_lockfile") {
		t.Error("the prompt doc must describe the main action")
	}
}

func TestActionSubject(t *testing.T) {
	if got := (System{}).ActionSubject("scan_lockfile", nil); got != "vulndb:scan_lockfile" {
		t.Errorf("subject = %q — every action needs its own guard-rail subject", got)
	}
}

// fakeOSV liefert für ein Paket einen Treffer und antwortet sonst leer.
func fakeOSV(t *testing.T, vulnerable string) func() {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/querybatch":
			var body struct {
				Queries []struct {
					Package struct{ Name string } `json:"package"`
				} `json:"queries"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			results := make([]map[string]any, len(body.Queries))
			for i, q := range body.Queries {
				results[i] = map[string]any{"vulns": []any{}}
				if q.Package.Name == vulnerable {
					results[i] = map[string]any{"vulns": []any{map[string]string{"id": "GHSA-x"}}}
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"results": results})
		case "/v1/query":
			w.Write([]byte(`{"vulns":[{"id":"GHSA-x","aliases":["CVE-2021-23337"],
			  "summary":"Command Injection","database_specific":{"severity":"high"},
			  "affected":[{"package":{"name":"` + vulnerable + `","ecosystem":"npm"},
			  "ranges":[{"type":"ECOSYSTEM","events":[{"introduced":"0"},{"fixed":"4.17.21"}]}]}]}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	old := osvBase
	osvBase = srv.URL
	return func() { osvBase = old; srv.Close() }
}

func TestScanLockfile(t *testing.T) {
	defer fakeOSV(t, "lodash")()

	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	lock := `{"lockfileVersion":3,"packages":{
	  "":{"dependencies":{"lodash":"^4.17.0"}},
	  "node_modules/lodash":{"version":"4.17.20"},
	  "node_modules/express":{"version":"4.18.0"}}}`
	if err := os.WriteFile(filepath.Join(repo, "package-lock.json"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}

	// Der Pfad ist relativ zum Sandbox-Arbeitsverzeichnis — genau so, wie ihn
	// ein Agent nach einem checkout angibt.
	ctx := target.WithWorkdir(context.Background(), home)
	out, err := (System{}).Execute(ctx, "scan_lockfile",
		json.RawMessage(`{"path":"repo/package-lock.json"}`), target.Credential{})
	if err != nil {
		t.Fatal(err)
	}
	res, ok := out.(*ScanResult)
	if !ok {
		t.Fatalf("unexpected result type %T", out)
	}
	if res.PackagesScanned != 2 {
		t.Errorf("packages_scanned = %d, want 2", res.PackagesScanned)
	}
	if len(res.Findings) != 1 || res.Findings[0].Package != "lodash" {
		t.Fatalf("findings = %+v", res.Findings)
	}
	if res.Findings[0].Fixed != "4.17.21" {
		t.Errorf("fixed = %q, want 4.17.21", res.Findings[0].Fixed)
	}
	if res.Clean {
		t.Error("clean must be false")
	}
}

// Ein sauberes Ergebnis muss sich AUSDRÜCKLICH als geprüft ausweisen. Eine
// leere Trefferliste allein ist mehrdeutig — sie sieht genauso aus, wenn nichts
// geprüft werden konnte.
func TestScanLockfileClean(t *testing.T) {
	defer fakeOSV(t, "nothing-matches")()

	home := t.TempDir()
	lock := `{"lockfileVersion":3,"packages":{"":{},"node_modules/express":{"version":"4.18.0"}}}`
	if err := os.WriteFile(filepath.Join(home, "package-lock.json"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := target.WithWorkdir(context.Background(), home)
	out, err := (System{}).Execute(ctx, "scan_lockfile",
		json.RawMessage(`{"path":"package-lock.json"}`), target.Credential{})
	if err != nil {
		t.Fatal(err)
	}
	if res := out.(*ScanResult); !res.Clean {
		t.Errorf("clean = false although nothing was found and nothing failed: %+v", res)
	}
}

func TestScanLockfileMissingPath(t *testing.T) {
	_, err := (System{}).Execute(context.Background(), "scan_lockfile", json.RawMessage(`{}`), target.Credential{})
	if err == nil || !strings.Contains(err.Error(), "path") {
		t.Fatalf("expected a hint about the missing path, got %v", err)
	}
}

func TestUnknownAction(t *testing.T) {
	_, err := (System{}).Execute(context.Background(), "delete_everything", nil, target.Credential{})
	if err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("an unknown action must fail: %v", err)
	}
}

// latest_version gegen die drei Registries. Interessant ist je Ökosystem eine
// andere Eigenheit: npm braucht den kodierten Schrägstrich für scoped packages,
// Packagist das führende "v" abgeschnitten, Pub liefert latest getrennt.
func TestLatestVersion(t *testing.T) {
	npm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Der scoped Name muss als EIN Pfadsegment ankommen.
		if !strings.Contains(r.URL.EscapedPath(), "%2f") && strings.Contains(r.URL.Path, "@babel") {
			t.Errorf("scoped package not encoded: %s", r.URL.EscapedPath())
		}
		w.Write([]byte(`{"dist-tags":{"latest":"7.24.0"},"versions":{"7.23.0":{},"7.24.0":{}}}`))
	}))
	defer npm.Close()
	packagist := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"packages":{"monolog/monolog":[{"version":"v2.9.1"},{"version":"2.3.0"}]}}`))
	}))
	defer packagist.Close()
	pub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"latest":{"version":"1.2.0"},"versions":[{"version":"1.1.0"},{"version":"1.2.0"}]}`))
	}))
	defer pub.Close()

	oldNpm, oldPkg, oldPub := npmBase, packagistAPI, pubBase
	npmBase, packagistAPI, pubBase = npm.URL, packagist.URL, pub.URL
	defer func() { npmBase, packagistAPI, pubBase = oldNpm, oldPkg, oldPub }()

	cases := []struct{ ecosystem, name, want string }{
		{"npm", "@babel/core", "7.24.0"},
		{"composer", "monolog/monolog", "2.9.1"},
		{"flutter", "http", "1.2.0"},
	}
	for _, c := range cases {
		params, _ := json.Marshal(map[string]string{"ecosystem": c.ecosystem, "name": c.name})
		out, err := (System{}).Execute(context.Background(), "latest_version", params, target.Credential{})
		if err != nil {
			t.Fatalf("%s: %v", c.ecosystem, err)
		}
		info := out.(VersionInfo)
		if info.Latest != c.want {
			t.Errorf("%s/%s: latest = %q, want %q", c.ecosystem, c.name, info.Latest, c.want)
		}
	}

	// Ein Composer-Paket ohne Vendor kann es nicht geben — das muss auffallen,
	// bevor eine Anfrage rausgeht.
	params, _ := json.Marshal(map[string]string{"ecosystem": "composer", "name": "monolog"})
	if _, err := (System{}).Execute(context.Background(), "latest_version", params, target.Credential{}); err == nil {
		t.Error("a composer package without a vendor must be refused")
	}
}

// advisory führt die Quellen zusammen. Geprüft wird, dass GHSA die konkretere
// Fix-Angabe beisteuert und NVD den Wert, den sonst niemand liefert — und dass
// eine ausgefallene Quelle als Notiz sichtbar wird statt still zu fehlen.
func TestAdvisoryMergesSources(t *testing.T) {
	osv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"GHSA-x","aliases":["CVE-2021-23337"],"summary":"Command Injection",
		  "details":"Long text","affected":[{"package":{"name":"lodash","ecosystem":"npm"},
		  "ranges":[{"type":"ECOSYSTEM","events":[{"introduced":"0"},{"fixed":"4.17.21"}]}]}],
		  "references":[{"type":"ADVISORY","url":"https://osv.dev/x"}],
		  "database_specific":{"severity":"high"}}`))
	}))
	defer osv.Close()
	ghsa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ghsa_id":"GHSA-x","cve_id":"CVE-2021-23337","summary":"Command Injection in lodash",
		  "severity":"high","html_url":"https://github.com/advisories/GHSA-x",
		  "vulnerabilities":[{"package":{"ecosystem":"npm","name":"lodash"},
		  "vulnerable_version_range":"< 4.17.21","first_patched_version":"4.17.21"}]}`))
	}))
	defer ghsa.Close()
	nvd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cveId") != "CVE-2021-23337" {
			t.Errorf("NVD was asked with %q", r.URL.RawQuery)
		}
		w.Write([]byte(`{"vulnerabilities":[{"cve":{"metrics":{"cvssMetricV31":[
		  {"cvssData":{"baseScore":7.2,"baseSeverity":"HIGH","vectorString":"CVSS:3.1/AV:N"}}]}}}]}`))
	}))
	defer nvd.Close()

	oldOSV, oldGHSA, oldNVD := osvBase, ghsaBase, nvdBase
	osvBase, ghsaBase, nvdBase = osv.URL, ghsa.URL, nvd.URL
	defer func() { osvBase, ghsaBase, nvdBase = oldOSV, oldGHSA, oldNVD }()

	out, err := (System{}).Execute(context.Background(), "advisory",
		json.RawMessage(`{"id":"GHSA-x"}`), target.Credential{})
	if err != nil {
		t.Fatal(err)
	}
	a := out.(*Advisory)
	if a.CVE != "CVE-2021-23337" {
		t.Errorf("cve = %q", a.CVE)
	}
	if a.CVSSScore != 7.2 || a.CVSSVector == "" {
		t.Errorf("the CVSS value from NVD is missing: score=%v vector=%q", a.CVSSScore, a.CVSSVector)
	}
	if len(a.Affected) != 1 || a.Affected[0].FirstPatched != "4.17.21" {
		t.Errorf("GHSA's fix statement was not merged in: %+v", a.Affected)
	}
	if len(a.Sources) != 3 {
		t.Errorf("sources = %v, all three answered", a.Sources)
	}
	// Ohne Schlüssel muss das Ergebnis sagen, dass NVD am öffentlichen Limit
	// hängt — sonst wundert sich der Betrieb über langsame Läufe.
	if !strings.Contains(strings.Join(a.Notes, " "), "vulndb_token") {
		t.Errorf("notes = %v, must mention the missing key", a.Notes)
	}
}

// Antwortet keine Quelle, ist ein leeres Advisory kein Ergebnis, sondern ein
// Fehler — sonst landet ein Ticket ohne Beleg im Backlog.
func TestAdvisoryAllSourcesDown(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer down.Close()
	oldOSV, oldGHSA, oldNVD := osvBase, ghsaBase, nvdBase
	osvBase, ghsaBase, nvdBase = down.URL, down.URL, down.URL
	defer func() { osvBase, ghsaBase, nvdBase = oldOSV, oldGHSA, oldNVD }()

	if _, err := (System{}).Execute(context.Background(), "advisory",
		json.RawMessage(`{"id":"CVE-2021-23337"}`), target.Credential{}); err == nil {
		t.Fatal("expected an error when no source answers")
	}
}
