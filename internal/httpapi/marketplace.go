package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"covey/internal/buildinfo"
	"covey/internal/marketplace"
	"github.com/benjaminLedel/covey-plugin-sdk/target"
)

// The marketplace: the catalogue the instance reads, and installing
// from it (spec/22).
//
// The split follows the two areas of control. WHICH catalogue is
// configured the instance decides (COVEY_MARKETPLACE_URL) — an
// organisation cannot bend that. What it installs from it it decides
// itself, with the same rights as when uploading by hand.

// marketplaceEntry is a catalogue entry, enriched with what only this
// instance knows: whether it is installed and whether a newer version awaits.
type marketplaceEntry struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Kind        string `json:"kind"`
	Publisher   string `json:"publisher"`
	Homepage    string `json:"homepage"`
	License     string `json:"license"`
	Deprecated  string `json:"deprecated,omitempty"`
	// Icon is the embedded badge (data:-URI) — delivered by the catalogue,
	// checked here against the allowed forms. What does not pass is simply
	// absent; the card then falls back to its category symbol.
	Icon string `json:"icon,omitempty"`
	// Version is the newest published version (empty for builtin).
	Version string `json:"version,omitempty"`
	Notes   string `json:"notes,omitempty"`
	// BuiltinSince: shipped along since this covey release — activate instead
	// of install.
	BuiltinSince string `json:"builtin_since,omitempty"`
	// No endpoint field: with kind=mcp the host sits IN the artefact, not in
	// the catalogue — the card could only show it if the instance fetched
	// every listed artefact up front. It becomes visible after the
	// install, in the detail view, and because an installed plugin
	// arrives switched off, nothing reaches this host until then.

	// Installed in THIS organisation:
	Installed        bool   `json:"installed"`
	InstalledVersion string `json:"installed_version,omitempty"`
	UpdateAvailable  bool   `json:"update_available"`
	// InstalledElsewhere: the name is taken here, but not from this
	// catalogue (uploaded by hand or a built-in). Installing would
	// overwrite — that has to be visible beforehand.
	InstalledElsewhere bool `json:"installed_elsewhere,omitempty"`
}

type marketplaceResponse struct {
	Enabled bool               `json:"enabled"`
	Source  string             `json:"source,omitempty"`
	Fetched *time.Time         `json:"fetched_at,omitempty"`
	Entries []marketplaceEntry `json:"entries"`
	// Error stands beside the entries, not instead of them: a catalogue that
	// is unreachable may not empty the page, but it may not
	// look healthy either.
	Error string `json:"error,omitempty"`
}

func (s *Server) handleMarketplace(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if !s.Marketplace.Enabled() {
		writeJSON(w, http.StatusOK, marketplaceResponse{Enabled: false, Entries: []marketplaceEntry{}})
		return
	}

	installed, err := s.Targets.List(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	have := map[string]struct {
		version string
		source  string
	}{}
	for _, pl := range installed {
		// A built-in without a row does show up in List; it is not
		// "installed", but present. Only real rows count here.
		if pl.Kind == "builtin" && pl.Source == "" {
			continue
		}
		have[pl.Name] = struct {
			version string
			source  string
		}{pl.SourceVersion, pl.Source}
	}

	cat, fetched, ferr := s.Marketplace.Catalog(r.Context())
	resp := marketplaceResponse{Enabled: true, Source: s.Marketplace.URL, Entries: []marketplaceEntry{}}
	if ferr != nil {
		resp.Error = ferr.Error()
	}
	if !fetched.IsZero() {
		resp.Fetched = &fetched
	}
	if cat == nil {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	for _, e := range cat.Plugins {
		entry := marketplaceEntry{
			Name: e.Name, Label: e.Label, Description: e.Description,
			Category: e.Category, Kind: e.Kind, Publisher: e.Publisher,
			Homepage: e.Homepage, License: e.License, Deprecated: e.Deprecated,
			BuiltinSince: e.BuiltinSince, Icon: e.SafeIcon(),
		}
		if v, ok := e.Latest(); ok {
			entry.Version, entry.Notes = v.Version, v.Notes
		}
		if row, ok := have[e.Name]; ok {
			if row.source != "" {
				entry.Installed = true
				entry.InstalledVersion = row.version
				// An update is a DIFFERENT version, not a larger one: comparing
				// versions would mean guessing Semver order, and a withdrawn
				// build is as much a change as a new one.
				entry.UpdateAvailable = entry.Version != "" && entry.Version != row.version
			} else {
				entry.InstalledElsewhere = true
			}
		} else if _, isBuiltin := target.Describe(e.Name); isBuiltin && e.Kind != "builtin" {
			entry.InstalledElsewhere = true
		}
		resp.Entries = append(resp.Entries, entry)
	}
	writeJSON(w, http.StatusOK, resp)
}

type installRequest struct {
	// Version is optional; empty = the newest of the entry. Given, it
	// installs exactly that — a downgrade is possible this way, and that is
	// intended: the version that ran is the first way out when a new one
	// does not.
	Version string `json:"version"`
}

func (s *Server) handleMarketplaceInstall(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	name := r.PathValue("name")
	if !s.Marketplace.Enabled() {
		writeErr(w, http.StatusNotFound, "no plugin catalogue is configured")
		return
	}

	var req installRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req) // leerer Body = neueste Version
	}

	entry, err := s.Marketplace.Entry(r.Context(), name)
	if err != nil {
		if errors.Is(err, marketplace.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not in the catalogue: "+name)
			return
		}
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if entry.Kind == "builtin" {
		// A compiled plugin cannot be installed; it is there or it is
		// not. The catalogue lists it only so that one can find it.
		writeErr(w, http.StatusBadRequest,
			"this plugin ships with covey — activate it instead of installing it")
		return
	}

	version, ok := entry.Latest()
	if req.Version != "" {
		if version, ok = entry.Find(req.Version); !ok {
			writeErr(w, http.StatusNotFound, "no version "+req.Version+" of "+name)
			return
		}
	}
	if !ok {
		writeErr(w, http.StatusBadRequest, "the catalogue lists no version of "+name)
		return
	}

	// The catalogue may say which covey this artefact needs, and a version that
	// says so has to be believed. It matters in one window in particular: an
	// entry appears BEFORE the release that drops the matching built-in, so
	// that nobody upgrades into a missing target system — and in that window an
	// older instance can see a plugin it cannot run. Installing it there does
	// not fail cleanly, because the store lists a compiled plugin and a stored
	// one under the same name and the compiled one wins.
	//
	// An unknown running version (a dev build, a source checkout) is allowed
	// through: refusing would make it impossible to test an entry before the
	// release it names exists.
	if ok, known := marketplace.MeetsMinVersion(buildinfo.Get().Version, version.CoveyMinVersion); !ok && known {
		writeErr(w, http.StatusConflict, fmt.Sprintf(
			"%s %s needs covey %s or newer, and this is %s — upgrade first, or install an older version of the plugin",
			name, version.Version, version.CoveyMinVersion, buildinfo.Get().Version))
		return
	}

	raw, err := s.Marketplace.Artifact(r.Context(), version, entry.Kind)
	if err != nil {
		// The digest error is the only one that really counts here: the
		// artefact is no longer the one the entry points at. It belongs, in
		// full, to the person who pressed "install".
		status := http.StatusBadGateway
		if errors.Is(err, marketplace.ErrDigest) {
			status = http.StatusConflict
		}
		writeErr(w, status, err.Error())
		return
	}

	stored, err := s.Targets.PutFromCatalog(r.Context(), p.OrgID, entry.Kind, raw,
		s.Marketplace.URL, version.Version, version.SHA256)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// The audit trail (middleware) records WHO this was; what exactly
	// came in stands permanently in the row (source/version/digest).
	// The log entry here is for the operator who is watching while it
	// happens.
	s.Log.Info("plugin installed from catalogue", "name", stored, "kind", entry.Kind,
		"version", version.Version, "publisher", entry.Publisher,
		"source", s.Marketplace.URL, "digest", version.SHA256)
	writeJSON(w, http.StatusOK, map[string]any{
		"name": stored, "version": version.Version, "enabled": false,
	})
}
