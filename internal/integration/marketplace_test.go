package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"covey/internal/marketplace"
)

// The artefact a catalogue points at: a perfectly ordinary manifest plugin,
// the kind a stranger would publish.
const redmineManifest = `{
  "name": "redmine",
  "label": "Redmine",
  "description": "Issue tracker",
  "category": "ticketing",
  "auth": {"header": "X-Redmine-API-Key", "format": "{token}"},
  "webhook": {"id_field": "issue.id"},
  "scopes": ["read"],
  "probe": {"path": "/users/current.json", "identity_field": "user.login"},
  "actions": {"get_issue": {"method": "GET", "path": "/issues/{id}.json", "scope": "read", "doc": "read one issue"}}
}`

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// fakeCatalog is a plugin index like the one in the covey-plugins repository:
// a catalogue file plus an artefact hosted "somewhere else".
type fakeCatalog struct {
	srv *httptest.Server
	// body is what the artefact URL answers with. A test changes it to play the
	// case the digest exists for: the foreign repository serving something
	// other than what the entry pins.
	body string
}

func newFakeCatalog(t *testing.T, pinned string) *fakeCatalog {
	t.Helper()
	c := &fakeCatalog{body: redmineManifest}
	mux := http.NewServeMux()
	c.srv = httptest.NewServer(mux)
	t.Cleanup(c.srv.Close)
	mux.HandleFunc("/artifacts/redmine-1.1.0.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, c.body)
	})
	mux.HandleFunc("/catalog.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(marketplace.Catalog{
			Schema: 1, GeneratedAt: "2026-08-13T09:00:00Z",
			Plugins: []marketplace.Entry{
				{
					Name: "redmine", Label: "Redmine", Description: "Issue tracker",
					Category: "ticketing", Kind: "custom", Publisher: "example",
					Homepage: "https://example.invalid", License: "MIT",
					Versions: []marketplace.Version{{
						Version: "1.1.0",
						URL:     c.srv.URL + "/artifacts/redmine-1.1.0.json",
						SHA256:  pinned,
					}},
				},
				{
					Name: "zammad", Label: "Zammad", Kind: "builtin", Category: "ticketing",
					Publisher: "covey", Homepage: "https://example.invalid",
					License: "AGPL-3.0-only", BuiltinSince: "0.1.0",
				},
			},
		})
	})
	return c
}

// TestMarketplaceInstall: a plugin nobody compiled in reaches an organization
// through the catalogue, arrives disabled, and behaves like any other target
// system afterwards — same row, same enforcement points (spec/22).
func TestMarketplaceInstall(t *testing.T) {
	s := newStack(t)
	cat := newFakeCatalog(t, sha256Hex(redmineManifest))
	s.srv.Marketplace = marketplace.New(cat.srv.URL + "/catalog.json")
	ctx := context.Background()

	admin := login(t, s, "admin@test.local", "admin-passwort")

	// The catalogue is visible, and it says what is installable and what only
	// wants activating.
	var list struct {
		Enabled bool `json:"enabled"`
		Entries []struct {
			Name             string `json:"name"`
			Kind             string `json:"kind"`
			Version          string `json:"version"`
			Installed        bool   `json:"installed"`
			InstalledVersion string `json:"installed_version"`
			UpdateAvailable  bool   `json:"update_available"`
			BuiltinSince     string `json:"builtin_since"`
		} `json:"entries"`
	}
	resp := admin.do(http.MethodGet, "/api/v1/marketplace", nil)
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if !list.Enabled || len(list.Entries) != 2 {
		t.Fatalf("catalogue = %+v, want 2 entries", list)
	}
	for _, e := range list.Entries {
		switch e.Name {
		case "redmine":
			if e.Installed || e.Version != "1.1.0" {
				t.Errorf("redmine should be offered and not installed: %+v", e)
			}
		case "zammad":
			if e.Kind != "builtin" || e.BuiltinSince == "" {
				t.Errorf("a compiled plugin has to be recognisable as one: %+v", e)
			}
		}
	}

	// A built-in cannot be installed — it is there or it is not.
	admin.expect(http.MethodPost, "/api/v1/marketplace/zammad/install", nil, http.StatusBadRequest)

	// Install.
	admin.expect(http.MethodPost, "/api/v1/marketplace/redmine/install", nil, http.StatusOK)

	// It arrives DISABLED: fetching a file and granting it credentials are two
	// decisions, and the second one belongs to a person.
	var plugins []map[string]any
	resp = admin.do(http.MethodGet, "/api/v1/targets", nil)
	json.NewDecoder(resp.Body).Decode(&plugins)
	resp.Body.Close()
	var found map[string]any
	for _, p := range plugins {
		if p["name"] == "redmine" {
			found = p
		}
	}
	if found == nil {
		t.Fatalf("redmine is not in the store after installing: %v", plugins)
	}
	if found["enabled"] != false {
		t.Errorf("an installed plugin must arrive disabled: %v", found)
	}
	if found["kind"] != "custom" {
		t.Errorf("kind = %v, want custom", found["kind"])
	}

	// The provenance is recorded — from where, which version, which digest.
	var source, version, digest string
	if err := s.pool.QueryRow(ctx,
		`SELECT source, source_version, source_digest FROM target_plugins WHERE org_id=$1 AND name='redmine'`,
		s.orgID).Scan(&source, &version, &digest); err != nil {
		t.Fatal(err)
	}
	if source != cat.srv.URL+"/catalog.json" || version != "1.1.0" || digest != sha256Hex(redmineManifest) {
		t.Errorf("provenance = (%s, %s, %s)", source, version, digest)
	}

	// And it is a target system like any other: the manifest's capabilities
	// arrive with it, so the setup assistant offers the connection test and the
	// scope vocabulary the file declares.
	var setup struct {
		Probe  bool     `json:"probe"`
		Scopes []string `json:"scopes"`
	}
	resp = admin.do(http.MethodGet, "/api/v1/targets/redmine/setup", nil)
	json.NewDecoder(resp.Body).Decode(&setup)
	resp.Body.Close()
	if !setup.Probe {
		t.Error("the manifest declares a probe — the assistant has to offer the connection test")
	}
	if len(setup.Scopes) != 1 || setup.Scopes[0] != "read" {
		t.Errorf("scopes = %v, want [read] from the manifest", setup.Scopes)
	}

	// Installed once, the catalogue says so — and offers no update, because the
	// version has not moved.
	resp = admin.do(http.MethodGet, "/api/v1/marketplace", nil)
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	for _, e := range list.Entries {
		if e.Name == "redmine" {
			if !e.Installed || e.InstalledVersion != "1.1.0" {
				t.Errorf("redmine should show as installed at 1.1.0: %+v", e)
			}
			if e.UpdateAvailable {
				t.Error("no update available — nothing changed in the catalogue")
			}
		}
	}
}

// TestMarketplaceRefusesAChangedArtefact: the foreign repository serves
// something other than what the entry pins. Nothing is installed, and the
// refusal says so — this is the property that makes third-party hosting
// acceptable in the first place.
func TestMarketplaceRefusesAChangedArtefact(t *testing.T) {
	s := newStack(t)
	cat := newFakeCatalog(t, sha256Hex(redmineManifest))
	s.srv.Marketplace = marketplace.New(cat.srv.URL + "/catalog.json")
	ctx := context.Background()

	// Somebody force-pushes over the tag.
	cat.body = `{"name":"redmine","webhook":{"id_field":"issue.id"},` +
		`"actions":{"exfiltrate":{"method":"POST","path":"/anything"}}}`

	admin := login(t, s, "admin@test.local", "admin-passwort")
	admin.expect(http.MethodPost, "/api/v1/marketplace/redmine/install", nil, http.StatusConflict)

	var n int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM target_plugins WHERE org_id=$1 AND name='redmine'`, s.orgID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("a plugin that failed its digest check must not be stored at all")
	}
}

// TestMarketplaceDisabled: without a configured catalogue the store is simply
// what it always was — no half-open marketplace, no empty page pretending to be
// one.
func TestMarketplaceDisabled(t *testing.T) {
	s := newStack(t)
	s.srv.Marketplace = marketplace.New("")
	admin := login(t, s, "admin@test.local", "admin-passwort")

	var list struct {
		Enabled bool  `json:"enabled"`
		Entries []any `json:"entries"`
	}
	resp := admin.do(http.MethodGet, "/api/v1/marketplace", nil)
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if list.Enabled || len(list.Entries) != 0 {
		t.Errorf("catalogue = %+v, want disabled and empty", list)
	}
	admin.expect(http.MethodPost, "/api/v1/marketplace/redmine/install", nil, http.StatusNotFound)
}
