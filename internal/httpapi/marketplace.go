package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"covey/internal/marketplace"
	"covey/internal/target"
)

// Der Marktplatz: der Katalog, den die Instanz liest, und das Installieren
// daraus (spec/22).
//
// Die Aufteilung folgt den zwei Verwaltungsbereichen. WELCHER Katalog
// konfiguriert ist, entscheidet die Instanz (COVEY_MARKETPLACE_URL) — eine
// Organisation kann ihn nicht umbiegen. Was sie daraus installiert, entscheidet
// sie selbst, mit denselben Rechten wie beim Hochladen von Hand.

// marketplaceEntry ist ein Katalog-Eintrag, angereichert um das, was nur diese
// Instanz weiß: ob er installiert ist und ob eine neuere Version bereitliegt.
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
	// Version ist die neueste veröffentlichte Version (leer bei builtin).
	Version string `json:"version,omitempty"`
	Notes   string `json:"notes,omitempty"`
	// BuiltinSince: ab dieser Covey-Fassung mitgeliefert — aktivieren statt
	// installieren.
	BuiltinSince string `json:"builtin_since,omitempty"`
	// Kein Endpoint-Feld: bei kind=mcp steht der Host IM Artefakt, nicht im
	// Katalog — die Karte könnte ihn nur zeigen, wenn die Instanz jedes
	// gelistete Artefakt im Voraus holte. Sichtbar wird er nach dem
	// Installieren in der Detailansicht, und weil ein installiertes Plugin
	// abgeschaltet ankommt, erreicht bis dahin nichts diesen Host.

	// Installiert in DIESER Organisation:
	Installed        bool   `json:"installed"`
	InstalledVersion string `json:"installed_version,omitempty"`
	UpdateAvailable  bool   `json:"update_available"`
	// InstalledElsewhere: der Name ist hier belegt, aber nicht aus diesem
	// Katalog (von Hand hochgeladen oder ein Built-in). Installieren würde
	// überschreiben — das muss vorher sichtbar sein.
	InstalledElsewhere bool `json:"installed_elsewhere,omitempty"`
}

type marketplaceResponse struct {
	Enabled bool               `json:"enabled"`
	Source  string             `json:"source,omitempty"`
	Fetched *time.Time         `json:"fetched_at,omitempty"`
	Entries []marketplaceEntry `json:"entries"`
	// Error steht neben den Einträgen, nicht statt ihrer: ein nicht
	// erreichbarer Katalog darf die Seite nicht leeren, aber auch nicht gesund
	// aussehen.
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
		// Ein Built-in ohne Zeile taucht in List trotzdem auf; es ist nicht
		// "installiert", sondern vorhanden. Nur echte Zeilen zählen hier.
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
			BuiltinSince: e.BuiltinSince,
		}
		if v, ok := e.Latest(); ok {
			entry.Version, entry.Notes = v.Version, v.Notes
		}
		if row, ok := have[e.Name]; ok {
			if row.source != "" {
				entry.Installed = true
				entry.InstalledVersion = row.version
				// Ein Update ist eine ANDERE Version, nicht eine größere:
				// Versionsvergleich hieße Semver-Ordnung raten, und ein
				// zurückgezogener Stand ist ebenso eine Änderung wie ein neuer.
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
	// Version ist optional; leer = die neueste des Eintrags. Angegeben
	// installiert sie genau die — ein Downgrade ist damit möglich, und das ist
	// beabsichtigt: die Version, die lief, ist der erste Ausweg, wenn eine neue
	// nicht tut.
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
		// Ein kompiliertes Plugin lässt sich nicht installieren; es ist da oder
		// nicht. Der Katalog führt es nur, damit man es findet.
		writeErr(w, http.StatusBadRequest,
			"this plugin ships with Covey — activate it instead of installing it")
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

	raw, err := s.Marketplace.Artifact(r.Context(), version)
	if err != nil {
		// Der Digest-Fehler ist der einzige, der hier wirklich zählt: das
		// Artefakt ist nicht mehr das, worauf der Eintrag zeigt. Er gehört
		// unverkürzt an den Menschen, der auf "installieren" gedrückt hat.
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
	// Die Audit-Spur (Middleware) hält fest, WER das war; was genau
	// hereingekommen ist, steht dauerhaft in der Zeile (source/version/digest).
	// Der Log-Eintrag hier ist für den Betreiber, der zuschaut, während es
	// passiert.
	s.Log.Info("plugin installed from catalogue", "name", stored, "kind", entry.Kind,
		"version", version.Version, "publisher", entry.Publisher,
		"source", s.Marketplace.URL, "digest", version.SHA256)
	writeJSON(w, http.StatusOK, map[string]any{
		"name": stored, "version": version.Version, "enabled": false,
	})
}
