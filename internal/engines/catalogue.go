// Package engines carries the engine catalogue: which agent runtime is to be
// installed where, as data rather than as a line in a Dockerfile.
//
// The problem it removes is a multiplication. A runtime binary used to be part
// of the sandbox image, so every engine had to be baked into every workplace
// that might want it: n engines × m profiles, and an installation that wants an
// engine the project does not publish could not have it without building the
// image itself (spec/26). With a catalogue the image carries no engine at all,
// the runner materialises the one an agent's runtime names, and adding an
// engine is one JSON entry.
//
// The mechanism is the one this repository already has twice: one JSON file
// behind one URL, fetched with a cache, pinned, deciding nothing on its own
// (internal/marketplace.Feed — spec/22 for plugins, spec/16 for workplaces).
// This is the third such document and it deliberately borrows the fourth
// property too: the last good copy survives a restart, so an installation whose
// catalogue host is down starts the engines it already knows about.
//
// What is new here, and why this catalogue is more sensitive than the two that
// exist, is that its content is CODE THAT RUNS INSIDE EVERY SANDBOX OF EVERY
// TENANT. The safeguards that follow from that are not decoration and are
// stated where they act: the digest (§ Digest), the organisation sets the URL
// and never an agent, and nothing installs while a sandbox starts (§ When).
package engines

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"path/filepath"
	"strings"
	"time"

	"covey/internal/buildinfo"
	"covey/internal/marketplace"
)

// CatalogSchema is the format this build reads. A catalogue announcing another
// one is refused rather than guessed at, as with the other two catalogues.
const CatalogSchema = 1

// Kinds an entry may name. Three, because those are the three shapes an agent
// runtime ships in: a tarball the publisher hosts, a package in an npm
// registry, or one file the publisher serves by itself — which is how a CLI
// that is a single Node bundle arrives, and the shape that needs no archive
// around it and no second home of ours (see #288).
const (
	KindTarball = "tarball"
	KindNpm     = "npm"
	KindFile    = "file"
)

// Catalog is the document behind the URL.
type Catalog struct {
	Schema      int     `json:"schema"`
	GeneratedAt string  `json:"generated_at"`
	Engines     []Entry `json:"engines"`
}

// Entry is one engine. Name is the runtime name as the daemon registry knows it
// (`claude-code`, `codex`, `educa-ai`, `sevencode`) — an engine a build does not
// register is carried along and ignored rather than refused, so a newer
// catalogue does not break an older covey.
type Entry struct {
	Name string `json:"name"`
	// Versions is append-only, newest last: a published release is never
	// edited, only superseded. The last entry is therefore the one this build
	// takes unless the instance pins a version.
	Versions []Release `json:"versions"`
}

// Release is one engine version and how to get it.
type Release struct {
	// Version is the ENGINE's version ("1.0.7"), not covey's. It is the
	// directory the layer lands in and the figure a run is recorded against —
	// which engine version produced an answer is part of the answer's evidence.
	Version string `json:"version"`
	// Kind selects the fetch: KindTarball and KindFile need URL+Integrity (the
	// first is unpacked, the second written as it arrives), KindNpm needs
	// Package (+Version) and runs with lifecycle scripts disabled.
	Kind string `json:"kind"`

	// URL of the artefact for KindTarball and KindFile. Signed URLs are fine —
	// the fetch is one plain GET — but the integrity below is what makes the
	// artefact known.
	URL string `json:"url,omitempty"`
	// Integrity is hex sha256 over the artefact bytes for KindTarball and
	// KindFile, and over the packed tarball of the npm package for KindNpm where
	// the publisher supplies it. Required for both fetched kinds: without it this
	// would be "download whatever is behind this URL today", which is not a pin.
	Integrity string `json:"integrity,omitempty"`
	// Package and Registry for KindNpm. Registry defaults to the public one; an
	// installation on a private registry sets its own catalogue and with it its
	// own trust, which is the point of the mechanism.
	Package  string `json:"package,omitempty"`
	Registry string `json:"registry,omitempty"`

	// Binary is the executable inside the layer, relative to its root
	// ("bin/sevencode"). Empty means <name> at the root, or the npm bin of the
	// same name.
	Binary string `json:"binary,omitempty"`
	// BinaryEnv is the variable the adapter in the daemon reads to find its
	// CLI. Convention: COVEY_<NAME>_BIN. It is spelled out here because the
	// mapping is not derivable in every case — the engine `claude-code` reads
	// COVEY_CLAUDE_BIN — and because the runner that sets it must not have to
	// know the daemon's internals.
	BinaryEnv string `json:"binary_env,omitempty"`
	// Env are further KEY=value pairs the run needs (an endpoint the CLI reads
	// from its environment). Values here are catalogue content, not secrets:
	// brokered credentials travel the path in spec/04 and never appear in a
	// document behind a public URL.
	Env []string `json:"env,omitempty"`
	// AllowScripts runs the package's own lifecycle scripts during install.
	// Off by default and off unless the entry says otherwise: an npm postinstall
	// is code the runner executes as the runner, outside every sandbox
	// boundary this platform maintains. An entry that needs it (a native build
	// step) is a deliberate decision by whoever publishes the catalogue, not a
	// default.
	AllowScripts bool `json:"allow_scripts,omitempty"`
	// Requires states what the engine needs from the host ("node>=22"). The
	// npm kind cannot be installed without node anyway; the field exists so the
	// reason says which requirement was not met rather than exiting 127.
	Requires []string `json:"requires,omitempty"`
	// AuthHeader says that the artefact sits behind a login and which header
	// opens it. Where the value comes from is the other half, and an entry names
	// it: AuthSecret is the NAME of a secret the organisation holds for the agent
	// whose engine this is — brokered for the one install that needs it, the way
	// every other credential of this platform arrives (spec/04) — and AuthEnv is
	// the fallback for a host that holds a token of its own, a mirror with
	// nothing in covey behind it. A name and never a value, either way: the
	// document has to be safe to publish on its own, and a credential inside a
	// fetched file is a credential shipped to everyone who can read the
	// catalogue. The token opens the door; the digest above is still what decides
	// whether a layer may run.
	AuthHeader string `json:"auth_header,omitempty"`
	AuthSecret string `json:"auth_secret,omitempty"`
	AuthEnv    string `json:"auth_env,omitempty"`
	// Notes are for the human reading the catalogue screen: what this release
	// is, what it costs, what to watch.
	Notes string `json:"notes,omitempty"`

	// engine is the entry's name, filled on parse. Unexported and never
	// serialized: a release cannot be resolved without the entry it stands in,
	// and a document that said it twice could contradict itself.
	engine string
}

// Valid refuses an entry that cannot be acted on. This runs at parse time: an
// installation must not discover at the first wake of an agent that the
// catalogue it accepted describes something nothing can install.
func (r Release) Valid() error {
	if strings.TrimSpace(r.Version) == "" {
		return fmt.Errorf("a release without a version cannot be pinned")
	}
	switch r.Kind {
	case KindTarball, KindFile:
		// Both are fetched by this process and pinned by a digest. The npm kind
		// is installed by the package manager, which reads its own credentials —
		// see its case below, where a header would be read by nothing.
		if strings.TrimSpace(r.URL) == "" || strings.TrimSpace(r.Integrity) == "" {
			return fmt.Errorf("a %s needs a URL and an integrity — one without the other is not a pin", r.Kind)
		}
		// An artefact may sit behind a login, but only as a reference: the entry
		// names the header and what holds its token, never the token. A credential
		// inside the document would be shipped to everyone who can read the
		// catalogue, which is the one thing about this mechanism that is not a
		// trade-off. A header without a place to read from, and a place to read
		// from without a header, both do nothing — and an entry that does nothing
		// is discovered at the first wake rather than at the parse that could have
		// said so.
		header, secretName, env := strings.TrimSpace(r.AuthHeader), strings.TrimSpace(r.AuthSecret), strings.TrimSpace(r.AuthEnv)
		if header != "" && secretName == "" && env == "" {
			return fmt.Errorf("%s %s names %s without %s or %s — the header needs a secret name or a variable to read its token from", r.engine, r.Version, "auth_header", "auth_secret", "auth_env")
		}
		if header == "" && (secretName != "" || env != "") {
			return fmt.Errorf("%s %s names where its token sits but no %s to send it in", r.engine, r.Version, "auth_header")
		}
		if secretName != "" && !isSecretName(secretName) {
			return fmt.Errorf("%s %s has %s %q, which is not the name of a secret", r.engine, r.Version, "auth_secret", secretName)
		}
		if env != "" && !isEnvName(env) {
			return fmt.Errorf("%s %s has %s %q, which is not the name of a variable", r.engine, r.Version, "auth_env", env)
		}
	case KindNpm:
		if strings.TrimSpace(r.Package) == "" {
			return fmt.Errorf("an npm release without a package name cannot be installed")
		}
		// The npm kind is installed by the package manager, not fetched here, so a
		// header declared on it would be read by nothing — an inert flag is worse
		// than a missing one, because it says the installation is protected when it
		// is not. A private registry is the `registry` field's decision.
		if strings.TrimSpace(r.AuthHeader) != "" || strings.TrimSpace(r.AuthEnv) != "" || strings.TrimSpace(r.AuthSecret) != "" {
			return fmt.Errorf("%s %s is installed by the package manager, so %s would be read by nothing", r.engine, r.Version, "auth_header")
		}
	default:
		return fmt.Errorf("unknown kind %q (known: %s, %s, %s)", r.Kind, KindTarball, KindFile, KindNpm)
	}
	return nil
}

// isEnvName accepts what the host's environment can actually be asked for. The
// test is small and deliberate: a path or a URL in this field means somebody
// wrote the secret itself where the name of a secret belongs, and refusing it at
// the parse is the one moment at which that can still be said quietly.
func isEnvName(s string) bool {
	for i, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return s != ""
}

// isSecretName accepts what the secret store can be asked for: a key of this
// organisation, the same shape a `{secret:…}` reference in a configuration
// carries (`sevencode_api_token`). Letters, digits and `_ - .`, starting with a
// letter. The test is small and deliberate for the same reason isEnvName's is:
// a path, a URL or a value with a space in it means somebody wrote the token
// itself where the name of a secret belongs, and refusing it at the parse is
// the one moment at which that can still be said quietly.
func isSecretName(s string) bool {
	if s == "" || !(s[0] >= 'a' && s[0] <= 'z' || s[0] >= 'A' && s[0] <= 'Z') {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '_', r == '-', r == '.':
		default:
			return false
		}
	}
	return true
}

// SecretName is the name of the organisation's secret that opens this artefact,
// or empty when the entry does not name one (an artefact that needs a host
// variable instead, or none at all).
func (r Release) SecretName() string { return strings.TrimSpace(r.AuthSecret) }

// Authenticated says whether this artefact sits behind a login at all.
func (r Release) Authenticated() bool {
	return strings.TrimSpace(r.AuthHeader) != ""
}

// Executable is the path of the CLI inside a layer root. The default follows the
// layout each kind produces by itself: an npm install leaves a bin link under
// bin/, and a tarball is expected to carry bin/ for the same reason — an
// artefact that lays its binary elsewhere says so in `binary`.
func (r Release) Executable(root string) string {
	b := strings.TrimSpace(r.Binary)
	if b == "" {
		name := r.Package
		if r.Kind == KindTarball || name == "" {
			name = r.engine
		}
		b = "bin/" + name
	}
	return path.Join(root, filepath.ToSlash(b))
}

// BinaryEnvName is the variable to set for a run of this release.
func (r Release) BinaryEnvName(engine string) string {
	if v := strings.TrimSpace(r.BinaryEnv); v != "" {
		return v
	}
	return "COVEY_" + strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(engine)) + "_BIN"
}

// Verify a fetched tarball against the declared integrity. Exported because the
// store verifies at install time and the caller reports the difference: what
// arrived is not what the catalogue promised.
func Verify(got []byte, want string) error {
	want = strings.TrimPrefix(strings.TrimSpace(want), "sha256:")
	sum := sha256.Sum256(got)
	have := hex.EncodeToString(sum[:])
	if !strings.EqualFold(have, want) {
		return fmt.Errorf("integrity mismatch: the catalogue promises %s, the artefact is %s", want, have)
	}
	return nil
}

// Source reads the engine catalogue. Nil, or one without a URL, has nothing to
// say — then the sandbox image and the per-engine environment variables stand,
// which is exactly the state before this catalogue existed.
type Source struct{ feed *marketplace.Feed[Catalog] }

// NewSource wires the catalogue behind one URL. store may be nil (no cache
// across restarts); log may be nil.
func NewSource(url string, store marketplace.Cache, log *slog.Logger) *Source {
	return &Source{feed: &marketplace.Feed[Catalog]{
		URL: url, Store: store, Log: log,
		Parse: parse, Name: "engines",
		// An engine release is published maybe weekly, and a new one is worth
		// an hour at worst: the layer stays usable while the document is old.
		TTL: time.Hour,
	}}
}

func (s *Source) Enabled() bool { return s != nil && s.feed.Enabled() }

// Catalog hands the document over with the time it was fetched. An error beside
// a non-nil catalogue means "last good copy, the refresh failed" — the caller
// shows both, as with the other catalogues.
func (s *Source) Catalog(ctx context.Context) (*Catalog, time.Time, error) {
	if !s.Enabled() {
		return nil, time.Time{}, marketplace.ErrDisabled
	}
	return s.feed.Get(ctx)
}

// For is the release to install for one engine: the entry's last version, or
// the one the instance pins. ok=false means the catalogue says nothing about
// this engine, which is not an error — it means the image has to carry it, or
// the operator names its path.
func (s *Source) For(ctx context.Context, engine, pinned string) (Release, bool) {
	cat, _, _ := s.Catalog(ctx)
	if cat == nil {
		return Release{}, false
	}
	return cat.Release(engine, pinned)
}

// Release resolves one engine's release out of a parsed catalogue. Split from
// Source so the resolution can be tested, and used by the runner with a copy it
// already holds.
func (c *Catalog) Release(engine, pinned string) (Release, bool) {
	for _, e := range c.Engines {
		if e.Name != engine || len(e.Versions) == 0 {
			continue
		}
		if pinned == "" {
			r := e.Versions[len(e.Versions)-1]
			r.engine = engine
			return r, true
		}
		for _, r := range e.Versions {
			if r.Version == pinned {
				r.engine = engine
				return r, true
			}
		}
		return Release{}, false
	}
	return Release{}, false
}

// DefaultCatalogURL is where the project publishes the engines it verifies —
// derived from the source address, so a fork publishing its own engines is right
// without editing anything here. Empty when the source is not on GitHub.
//
// An installation with its own catalogue (a private registry, an internal
// mirror) sets COVEY_ENGINE_CATALOG_URL and this default stops mattering —
// the URL is the organisation's decision, see spec/26.
func DefaultCatalogURL() string {
	system, project := buildinfo.SourceRepo()
	if system != "github" || project == "" {
		return ""
	}
	return "https://raw.githubusercontent.com/" + project + "/catalog/engine-catalog.json"
}

func parse(body []byte) (*Catalog, error) {
	var cat Catalog
	if err := json.Unmarshal(body, &cat); err != nil {
		return nil, fmt.Errorf("engines: catalogue is not valid JSON: %w", err)
	}
	if cat.Schema != CatalogSchema {
		return nil, fmt.Errorf("engines: catalogue announces schema %d, this build reads %d",
			cat.Schema, CatalogSchema)
	}
	for i, e := range cat.Engines {
		if strings.TrimSpace(e.Name) == "" {
			return nil, fmt.Errorf("engines: entry %d has no name", i)
		}
		for j, r := range e.Versions {
			r.engine = e.Name
			if err := r.Valid(); err != nil {
				return nil, fmt.Errorf("engines: %s version %q: %w", e.Name, r.Version, err)
			}
			cat.Engines[i].Versions[j] = r
		}
	}
	return &cat, nil
}
