// Package marketplace reads a plugin catalogue and fetches the artefacts it
// points at (spec/22).
//
// The catalogue is one JSON file behind one configurable URL — deliberately not
// an API, a token or a git clone, so that the same mechanism works against
// GitHub raw, GitLab raw, an S3 bucket, an internal web server or a file://
// path. An operator who wants their own catalogue serves their own file; that
// is also how internal, non-public plugins are distributed.
//
// What this package does NOT do is decide anything. It fetches, it verifies a
// digest, and it hands the bytes back. Installing is a separate, deliberate act
// by a person, and nothing here ever runs on its own — a catalogue that could
// install or update by itself would be a supply-chain back door into every
// organization at once, which is precisely what pinning by digest exists to
// prevent.
package marketplace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// SchemaVersion is the catalogue format this build understands. A catalogue
// that announces a different one is refused rather than guessed at — a reader
// that improvises on an unknown format is how a plugin ends up installed from a
// field it misread.
const SchemaVersion = 1

const (
	// maxCatalog caps the catalogue; it is a list of descriptions.
	maxCatalog = 8 << 20
	// maxArtifact caps a JSON plugin file. A manifest is a description of a
	// REST API, not a payload — anything larger is not the thing we asked for.
	maxArtifact = 1 << 20
	// maxModule caps a wasm plugin. Compiled code is legitimately larger: a Go
	// module carries its runtime and lands around 3–4 MB (TinyGo an order of
	// magnitude less). Still a ceiling, because the module is held in memory,
	// stored in a row and shipped to every sandbox that uses it.
	maxModule    = 16 << 20
	fetchTimeout = 20 * time.Second
	// cacheTTL: how long a fetched catalogue is served without asking again.
	// The catalogue changes when somebody merges a pull request somewhere else
	// — minutes of staleness cost nothing, and a store page that hits a foreign
	// host on every render costs both sides.
	cacheTTL = 15 * time.Minute
)

var (
	// ErrDisabled: no catalogue configured (COVEY_MARKETPLACE_URL empty).
	ErrDisabled = errors.New("marketplace: no catalogue configured")
	// ErrNotFound: no such plugin or version in the catalogue.
	ErrNotFound = errors.New("marketplace: not in the catalogue")
	// ErrDigest: the artefact does not match the digest the entry pins. This is
	// the error the whole design exists to be able to produce.
	ErrDigest = errors.New("marketplace: artefact does not match its digest")
)

// Catalog is the file an instance fetches.
type Catalog struct {
	Schema      int     `json:"schema"`
	GeneratedAt string  `json:"generated_at"`
	Plugins     []Entry `json:"plugins"`
}

// Entry is one plugin in the catalogue. The fields mirror the entry schema of
// the index repository; unknown ones are ignored on purpose — a catalogue may
// be newer than the instance reading it, and a field this build does not know
// is not a reason to refuse the whole file.
type Entry struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Kind        string `json:"kind"` // custom | mcp | builtin
	Publisher   string `json:"publisher"`
	Homepage    string `json:"homepage"`
	License     string `json:"license"`
	Deprecated  string `json:"deprecated,omitempty"`
	// Icon is the plugin's mark, EMBEDDED as a data: URI — never a link to a
	// picture on somebody's server. A remote image would fire a request to a
	// foreign host for everyone who opens the store page: a counting pixel
	// nobody asked for, and one more host to hold the page hostage when it is
	// slow. Embedded, the catalogue is still one file that either arrives or
	// does not. See SafeIcon for what is accepted.
	Icon     string    `json:"icon,omitempty"`
	Versions []Version `json:"versions,omitempty"`
	// BuiltinSince: kind=builtin only — the release this plugin ships in.
	// Those are activated, not installed.
	BuiltinSince string `json:"builtin_since,omitempty"`
}

// Version is one published, immutable version of a plugin.
type Version struct {
	Version         string `json:"version"`
	URL             string `json:"url"`
	SHA256          string `json:"sha256"`
	CoveyMinVersion string `json:"covey_min_version,omitempty"`
	Notes           string `json:"notes,omitempty"`
}

// maxIcon caps an embedded mark. A logo is a few hundred bytes of vector or a
// small bitmap; anything past this is not a mark, and it would ride along in
// every catalogue response.
const maxIcon = 32 << 10

// iconPrefixes are the only things an icon may be. The value goes into an
// <img src> in someone's browser, so this is a security boundary, not a
// formatting preference: without it a catalogue could hand out `javascript:`
// or a tracking URL and the store page would dutifully load it.
//
// SVG is allowed because it is drawn in an <img>, where scripts do not run.
var iconPrefixes = []string{
	"data:image/svg+xml;base64,",
	"data:image/png;base64,",
	"data:image/webp;base64,",
}

// SafeIcon returns the entry's icon if it is one we are willing to put in front
// of a person, and "" otherwise. Silently dropping is right here: a malformed
// mark is a cosmetic defect in somebody else's file, not a reason to refuse
// the plugin — the fallback symbol is perfectly good.
func (e Entry) SafeIcon() string {
	if len(e.Icon) == 0 || len(e.Icon) > maxIcon {
		return ""
	}
	for _, p := range iconPrefixes {
		if strings.HasPrefix(e.Icon, p) {
			return e.Icon
		}
	}
	return ""
}

// Latest is the newest published version — the first entry, because the
// catalogue lists them newest first. Returns false for a built-in, which has
// no artefact to install.
func (e Entry) Latest() (Version, bool) {
	if len(e.Versions) == 0 {
		return Version{}, false
	}
	return e.Versions[0], true
}

// Find returns a specific version of an entry.
func (e Entry) Find(version string) (Version, bool) {
	for _, v := range e.Versions {
		if v.Version == version {
			return v, true
		}
	}
	return Version{}, false
}

// Client fetches and caches one catalogue.
type Client struct {
	URL  string
	HTTP *http.Client
	// Store keeps the last good catalogue across restarts (optional).
	Store Cache
	// Log receives background refresh failures. Optional; without it a failed
	// refresh is only visible through the error the next call carries.
	Log *slog.Logger

	// feed does the fetching, the caching and the stale handling — the part
	// that is not about plugins and is therefore shared with the workplace
	// catalogue (spec/16). Created on first use so that a Client stays usable
	// as a plain struct literal, which is how the tests build it.
	once sync.Once
	feed *Feed[Catalog]
}

// catalog returns the feed, wired from the fields the caller set.
func (c *Client) catalog() *Feed[Catalog] {
	c.once.Do(func() {
		c.feed = &Feed[Catalog]{
			URL: c.URL, HTTP: c.HTTP, Store: c.Store, Log: c.Log,
			Parse: parseCatalog, Name: "marketplace",
		}
	})
	return c.feed
}

func New(catalogURL string) *Client {
	return &Client{URL: catalogURL, HTTP: &http.Client{Timeout: fetchTimeout}}
}

// Enabled: is a catalogue configured at all?
func (c *Client) Enabled() bool { return c != nil && strings.TrimSpace(c.URL) != "" }

// Catalog returns the catalogue, from the cache when it is fresh enough.
//
// On a failed refresh the last good copy is served with the error alongside it,
// not instead of it: an unreachable catalogue must not make a store page empty,
// and it must not look healthy either.
func (c *Client) Catalog(ctx context.Context) (*Catalog, time.Time, error) {
	return c.catalog().Get(ctx)
}

// Entry looks one plugin up in the catalogue.
func (c *Client) Entry(ctx context.Context, name string) (Entry, error) {
	cat, _, err := c.Catalog(ctx)
	if cat == nil {
		return Entry{}, err
	}
	for _, e := range cat.Plugins {
		if e.Name == name {
			return e, nil
		}
	}
	return Entry{}, fmt.Errorf("%w: %s", ErrNotFound, name)
}

func parseCatalog(body []byte) (*Catalog, error) {
	var cat Catalog
	if err := json.Unmarshal(body, &cat); err != nil {
		return nil, fmt.Errorf("marketplace: catalogue is not valid JSON: %w", err)
	}
	if cat.Schema != SchemaVersion {
		return nil, fmt.Errorf("marketplace: catalogue announces schema %d, this build reads %d",
			cat.Schema, SchemaVersion)
	}
	return &cat, nil
}

// Artifact fetches a version's plugin file and verifies it against the digest
// the entry pins.
//
// This is the hinge of the whole arrangement. The artefact lives in a
// repository nobody here controls — it may be force-pushed, retagged, sold or
// deleted. None of that can change what gets installed, because a changed
// artefact no longer matches its digest and fails loudly instead of quietly
// becoming something else.
func (c *Client) Artifact(ctx context.Context, v Version, kind string) ([]byte, error) {
	if !c.Enabled() {
		return nil, ErrDisabled
	}
	if len(v.SHA256) != 64 {
		return nil, fmt.Errorf("%w: entry pins no usable digest", ErrDigest)
	}
	limit := int64(maxArtifact)
	if kind == "wasm" {
		limit = maxModule
	}
	body, err := fetchBytes(ctx, c.HTTP, v.URL, limit)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	if got := hex.EncodeToString(sum[:]); got != strings.ToLower(v.SHA256) {
		return nil, fmt.Errorf("%w: entry says %s, artefact is %s", ErrDigest, v.SHA256, got)
	}
	return body, nil
}

// air-gapped case, where the catalogue is a mirror on disk rather than
// something on the network.
