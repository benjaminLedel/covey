package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"covey/internal/buildinfo"
	"covey/internal/marketplace"
)

// The workplaces, from a catalogue instead of from an environment variable.
//
// Where the image of a profile lies used to be a compiled default plus
// COVEY_SANDBOX_IMAGE_<PROFILE>. That put the same question to every
// installation that the platform can answer for itself — and answered it wrong
// by default: the reference pointed at a name that only exists on a machine
// that built the image (`covey-sandbox-dev:latest`), so every container
// installation had to be told where its own workplace lives, and an upgrade
// that moved agents to a new profile broke every instance that had not been.
//
// The catalogue is the same shape the plugin marketplace uses (spec/22): one
// JSON file behind one URL, fetched with a cache, and it decides nothing on its
// own. It carries what only the side that BUILDS the images knows — which image
// belongs to which Covey version, pinned by digest.
//
// The digest is the hinge here too. A tag can be moved; a digest cannot, so
// `covey serve` of a given version always starts the exact image that was built
// and published for it. Verification costs nothing extra: docker refuses a
// digest that does not match, without anybody here writing a check.
//
// Precedence, and it stays this order:
//
//	COVEY_SANDBOX_IMAGE_<PROFILE>   an operator's explicit word — always wins
//	the catalogue                    what the project published for this version
//	the compiled default             `covey-sandbox:latest`, the locally built name
//
// So an air-gapped installation sets the variable and never touches a
// catalogue; everyone else sets nothing at all.

// CatalogSchema is the format this build reads. A catalogue announcing another
// one is refused rather than guessed at.
const CatalogSchema = 1

// RollingVersion is the entry an untagged build uses — the state of main. A
// binary built from a release tag looks for that tag instead.
const RollingVersion = "main"

// Catalog is the file an instance fetches.
type Catalog struct {
	Schema      int            `json:"schema"`
	GeneratedAt string         `json:"generated_at"`
	Workplaces  []CatalogEntry `json:"workplaces"`
}

// CatalogEntry is one workplace. Name matches the profile in this catalogue
// (`base`, `dev`) — a published workplace this build does not know is carried
// along and offered, because a newer catalogue may name one that a later Covey
// registers.
type CatalogEntry struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
	// Images: one per Covey version. Append-only, like the marketplace's
	// versions — a published entry is never edited, only superseded.
	Images []CatalogImage `json:"images"`
}

// CatalogImage is the image for one Covey version.
type CatalogImage struct {
	// CoveyVersion is the release tag ("v0.4.0") or RollingVersion.
	CoveyVersion string `json:"covey_version"`
	// Ref is the image, pinned by digest: ghcr.io/…/covey-sandbox@sha256:…
	// A tag would be a moving target, and a moving target is not a pin. This is
	// what gets STARTED.
	Ref string `json:"ref"`
	// Tag is the same image under the name it was published as
	// ("base-v0.4.0"). This is what gets SHOWN.
	//
	// Both, because they answer different questions and neither answers the
	// other's. A digest says "exactly this, on every host, forever" — and it
	// says it in sixty-four characters nobody reads. A tag says which image
	// this is, in a form a human can compare with a release note. Showing only
	// the digest turned every screen it appeared on into noise; pinning only
	// the tag would give away the one property that makes the catalogue worth
	// having.
	Tag string `json:"tag,omitempty"`
	// Platforms is what the manifest carries (linux/amd64, linux/arm64) —
	// display, not a decision: the daemon picks its own architecture.
	Platforms []string `json:"platforms,omitempty"`
}

// Find returns the image for a Covey version, or false.
func (e CatalogEntry) Find(version string) (CatalogImage, bool) {
	for _, img := range e.Images {
		if img.CoveyVersion == version {
			return img, true
		}
	}
	return CatalogImage{}, false
}

// Source reads the workplace catalogue. Nil, or one without a URL, simply has
// nothing to say — then the compiled defaults and the environment stand, which
// is exactly the state before the catalogue existed.
type Source struct{ feed *marketplace.Feed[Catalog] }

// NewSource wires a catalogue behind one URL. store may be nil (no cache across
// restarts); log may be nil.
func NewSource(url string, store marketplace.Cache, log *slog.Logger) *Source {
	return &Source{feed: &marketplace.Feed[Catalog]{
		URL: url, Store: store, Log: log,
		Parse: parseCatalog, Name: "workplaces",
		// A catalogue of two images changes when a release is published.
		// Checking every quarter of an hour would ask a foreign host far more
		// often than anything can have changed.
		TTL: time.Hour,
	}}
}

func (s *Source) Enabled() bool { return s != nil && s.feed.Enabled() }

// Catalog hands the document over, with the time it was fetched. An error next
// to a non-nil catalogue means "this is the last good copy, the refresh
// failed" — the caller shows both.
func (s *Source) Catalog(ctx context.Context) (*Catalog, time.Time, error) {
	if !s.Enabled() {
		return nil, time.Time{}, marketplace.ErrDisabled
	}
	return s.feed.Get(ctx)
}

// Images resolves profile → image for THIS build: the catalogue's entry for the
// running version, under the profile names this instance knows.
//
// An unreachable catalogue returns nothing rather than an error the caller
// would have to translate into "keep what you had" — that is what the empty map
// means here, and Resolve applies it in the right order.
func (s *Source) Images(ctx context.Context) map[string]string {
	cat, _, err := s.Catalog(ctx)
	if cat == nil || err != nil && len(cat.Workplaces) == 0 {
		return nil
	}
	version := CatalogVersion()
	out := map[string]string{}
	for _, e := range cat.Workplaces {
		if img, ok := e.Find(version); ok && strings.TrimSpace(img.Ref) != "" {
			out[e.Name] = img.Ref
		}
	}
	return out
}

// Resolved is Images with the whole entry instead of only the reference — what
// a view needs that wants to show the tag and say which platforms it carries.
func (s *Source) Resolved(ctx context.Context) map[string]CatalogImage {
	cat, _, _ := s.Catalog(ctx)
	if cat == nil {
		return nil
	}
	version := CatalogVersion()
	out := map[string]CatalogImage{}
	for _, e := range cat.Workplaces {
		if img, ok := e.Find(version); ok && strings.TrimSpace(img.Ref) != "" {
			out[e.Name] = img
		}
	}
	return out
}

// CatalogVersion is the entry this build looks for: its release tag, or the
// rolling one when it does not sit exactly on a tag.
//
// Not the commit: the images are built per release and for main, not per
// commit — a build from between two releases takes the rolling image, and the
// startup log says which build it is anyway.
func CatalogVersion() string {
	if ref, isTag := buildinfo.Ref(); isTag && ref != "" {
		return ref
	}
	return RollingVersion
}

// Resolve puts the three sources in their order and returns what every profile
// resolves to on this installation.
//
// env beats catalogue beats compiled default — an operator who has named an
// image has said the last word, and a catalogue that could overrule it would
// make a remote file decide what runs on somebody's host.
func Resolve(env, catalogue map[string]string) map[string]string {
	out := Images(nil) // the compiled defaults
	for name, ref := range catalogue {
		if strings.TrimSpace(ref) != "" {
			out[name] = ref
		}
	}
	for name, ref := range env {
		if strings.TrimSpace(ref) != "" {
			out[name] = ref
		}
	}
	return out
}

// Pullable answers whether a reference names a registry — whether a host that
// does not have this image can simply get it.
//
// The distinction matters wherever "the image is not here" is turned into
// advice. `covey-sandbox:latest` is a name that exists only on a machine that
// built it: not there means build it. A published reference is a different
// case entirely — `docker run` fetches it on its own, so the honest answer is
// "the first wake takes longer", not a build command that a container
// installation cannot even run.
//
// The rule is docker's own: the first path segment is a registry when it
// carries a dot or a port, or is localhost. Everything else is a name on
// Docker Hub or, without a slash, a purely local tag — and Hub images we treat
// as pullable too, because they are.
func Pullable(ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return false
	}
	name, _, _ := strings.Cut(ref, "@") // Digest ab
	first, rest, hatSlash := strings.Cut(name, "/")
	if !hatSlash {
		// Kein Schrägstrich: ein lokaler Name (covey-sandbox:latest) oder ein
		// offizielles Hub-Image (postgres:16). Der Unterschied ist von außen
		// nicht zu sehen, und die vorsichtige Antwort ist die richtige: Wer
		// hier "ziehbar" sagt, verschluckt den Bau-Hinweis für genau den Fall,
		// für den er gedacht ist.
		return false
	}
	_ = rest
	return strings.Contains(first, ".") || strings.Contains(first, ":") || first == "localhost"
}

func parseCatalog(body []byte) (*Catalog, error) {
	var cat Catalog
	if err := json.Unmarshal(body, &cat); err != nil {
		return nil, fmt.Errorf("workplaces: catalogue is not valid JSON: %w", err)
	}
	if cat.Schema != CatalogSchema {
		return nil, fmt.Errorf("workplaces: catalogue announces schema %d, this build reads %d",
			cat.Schema, CatalogSchema)
	}
	return &cat, nil
}

// DefaultCatalogURL is where the project publishes the workplaces it builds —
// derived from the source address (buildinfo.SourceRepo), so a fork that
// publishes its own images and its own catalogue is right without editing
// anything here. Empty when the source is not on GitHub.
func DefaultCatalogURL() string {
	system, project := buildinfo.SourceRepo()
	if system != "github" || project == "" {
		return ""
	}
	return "https://raw.githubusercontent.com/" + project + "/catalog/sandbox-catalog.json"
}
