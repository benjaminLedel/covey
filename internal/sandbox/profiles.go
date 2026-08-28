// Package sandbox carries the catalogue of workplaces an agent can work in.
//
// A profile is a name for an image plus the knowledge of what is inside it and
// how one gets it. That knowledge existed before this package too — but four
// times over: in the wiring of the pool, in a `strings.Contains(image,
// "sandbox-dev")` that guessed the build command from the image name, in the
// doctor, and twice in the interface's dropdown. Four copies of one list is
// three too many; the third profile would have had to be added in four places,
// and would have been forgotten in two of them.
//
// It follows the target systems, which learned this earlier: the plugin
// declares its metadata, and the interface derives its list from what is
// registered rather than from one of its own. Registration happens here in
// init() and not spread over subpackages, because a profile is data and not
// code — there is nothing to compile in per profile.
package sandbox

import (
	"sort"
	"strings"
	"sync"

	"covey/internal/buildinfo"
)

// Profile is a workplace: an image, and what one needs to know about it.
type Profile struct {
	Name string `json:"name"`
	// Label and Description are English, like the target systems' — the
	// interface may translate them under `agent.settings.sandboxProfile.<name>`
	// and falls back to this text. So a profile added tomorrow is readable
	// without touching the locale files.
	Label       string `json:"label"`
	Description string `json:"description"`
	// Image is the default reference. The instance may override it per profile
	// (COVEY_SANDBOX_IMAGE_<NAME>); Images() resolves that.
	Image string `json:"image"`
	// Build is the command that produces this image, Dockerfile the file it
	// comes from. Both belong to the profile because only it knows them — an
	// error message that has to guess the build command from the image name is
	// guessing about its own repository.
	Build      string `json:"build"`
	Dockerfile string `json:"dockerfile"`
	// Default marks the profile an agent without a choice ends up in.
	Default bool `json:"default"`
}

var (
	mu       sync.RWMutex
	registry = map[string]Profile{}
	order    []string
)

// Register enters a profile. Registration order is display order.
func Register(p Profile) {
	mu.Lock()
	defer mu.Unlock()
	if _, ok := registry[p.Name]; !ok {
		order = append(order, p.Name)
	}
	registry[p.Name] = p
}

func init() {
	Register(Profile{
		Name:  "base",
		Label: "base",
		Description: "Node, git, chromium, ripgrep — enough for support, mail, QA and research agents. " +
			"Everything that does not compile belongs here: on a measured instance five of eight agents " +
			"were carrying a compiler tool-chain to write wiki pages.",
		Image:      "covey-sandbox:latest",
		Build:      "make sandbox-image",
		Dockerfile: "Dockerfile.sandbox",
		Default:    true,
	})
	Register(Profile{
		Name:  "dev",
		Label: "dev",
		Description: "Everything from base plus PHP, JDK, fvm, uv and the node-gyp toolchain — " +
			"the union, for an agent whose field is not settled when it wakes. An agent that " +
			"only ever gets tickets of one kind is better off in a role workplace: it carries " +
			"the same toolchain and not the three it never calls.",
		Image:      "covey-sandbox-dev:latest",
		Build:      "make sandbox-image-dev",
		Dockerfile: "Dockerfile.sandbox.dev",
	})
	// The role workplaces. They do not replace `dev` and they are not a cut
	// along languages — they are the cut that already runs between `base` and
	// `dev`, drawn one step further: the image hangs off the AGENT, and an
	// agent with a settled field carries what that field needs.
	//
	// A role image is worth its own build when it saves more than it costs.
	// `dev-python` is therefore not here: it would differ from `dev-web` by one
	// 40 MB binary whose interpreters live in the home either way. It earns its
	// place when an agent asks for it, not before.
	Register(Profile{
		Name:  "dev-flutter",
		Label: "dev · Flutter",
		Description: "Base plus a Flutter SDK that is already in the image, fvm for projects " +
			"that pin another version, and a JDK for Gradle. The SDK sits here rather than in " +
			"the home on purpose: for a Flutter agent the version question is settled, and the " +
			"home is walked at every wake and written back after every run.",
		Image:      "covey-sandbox-dev-flutter:latest",
		Build:      "make sandbox-image-dev-flutter",
		Dockerfile: "Dockerfile.sandbox.dev-flutter",
	})
	Register(Profile{
		Name:  "dev-php",
		Label: "dev · PHP",
		Description: "Base plus PHP 8.2 with the extensions the projects use, Composer, a " +
			"MariaDB to test against, and the node-gyp toolchain for the asset build — without " +
			"the JDK and the Flutter toolchain a Laravel agent would otherwise carry along.",
		Image:      "covey-sandbox-dev-php:latest",
		Build:      "make sandbox-image-dev-php",
		Dockerfile: "Dockerfile.sandbox.dev-php",
	})
	Register(Profile{
		Name:  "dev-web",
		Label: "dev · Web",
		Description: "Base plus the native build toolchain for npm packages — node, git and " +
			"chromium already come from base. The smallest of the developer workplaces: what a " +
			"Node/TypeScript project needs, and nothing beyond it.",
		Image:      "covey-sandbox-dev-web:latest",
		Build:      "make sandbox-image-dev-web",
		Dockerfile: "Dockerfile.sandbox.dev-web",
	})
}

// All returns the registered profiles in registration order.
func All() []Profile {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Profile, 0, len(order))
	for _, name := range order {
		out = append(out, registry[name])
	}
	return out
}

// Get returns a profile by name.
func Get(name string) (Profile, bool) {
	mu.RLock()
	defer mu.RUnlock()
	p, ok := registry[strings.TrimSpace(name)]
	return p, ok
}

// DefaultName is the profile an agent without a choice works in.
func DefaultName() string {
	for _, p := range All() {
		if p.Default {
			return p.Name
		}
	}
	return ""
}

// Images maps profile name → image, with the instance's overrides applied. The
// map is what the runner pool resolves an agent's workplace with; unknown names
// stay untouched there and are taken as a literal image reference.
func Images(overrides map[string]string) map[string]string {
	out := map[string]string{}
	for _, p := range All() {
		image := p.Image
		if o := strings.TrimSpace(overrides[p.Name]); o != "" {
			image = o
		}
		out[p.Name] = image
	}
	return out
}

// EnvVar is the variable an instance overrides a profile's image with. `base`
// additionally answers to COVEY_SANDBOX_IMAGE — the name from before the split,
// which is documented and set in existing installations.
func EnvVar(name string) string {
	return "COVEY_SANDBOX_IMAGE_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

// BuildHint answers "how do I get this image?" for an image reference. Only a
// profile's image has an answer; anything else is somebody's own image, and
// about that this repository knows nothing — so it says the honest thing rather
// than a make target that would not build it.
func BuildHint(overrides map[string]string, image string) string {
	image = strings.TrimSpace(image)
	for name, ref := range Images(overrides) {
		if ref != image {
			continue
		}
		if p, ok := Get(name); ok {
			return p.Build
		}
	}
	return ""
}

// PublicImage is where this profile's image lies ALREADY BUILT: the package
// the project publishes on every push and every release
// (.github/workflows/sandbox-images.yml). One package, the variants as a tag
// prefix.
//
// It exists so that "how do I get this image?" has an answer that is not a
// multi-gigabyte build on somebody's own machine — and a container
// installation has no repository to run that build from in the first place.
//
// The tag follows the running binary, not the newest state: the image carries
// the coveyd that speaks to THIS control plane. A release pulls its release, an
// untagged build the rolling one, which is exactly what the workflow publishes.
//
// Derived from the source address (buildinfo.SourceRepo) rather than written
// out: a fork that publishes its own images is then already right, and one
// that publishes none says so at the same place it says everything else about
// its origin. Empty when the source is not on GitHub — there is no package then
// and naming one would send somebody to an address that answers nothing.
func PublicImage(profile string) string {
	system, project := buildinfo.SourceRepo()
	if system != "github" || project == "" {
		return ""
	}
	if _, ok := Get(profile); !ok {
		return ""
	}
	tag := profile + "-latest"
	if ref, istTag := buildinfo.Ref(); istTag && ref != "" {
		tag = profile + "-" + ref
	}
	return "ghcr.io/" + strings.ToLower(project) + "-sandbox:" + tag
}

// PublicImageFor answers PublicImage for an image reference instead of a
// profile name — the shape a caller needs who is holding a missing image and
// not a catalogue entry.
func PublicImageFor(overrides map[string]string, image string) string {
	image = strings.TrimSpace(image)
	for name, ref := range Images(overrides) {
		if ref == image {
			return PublicImage(name)
		}
	}
	return ""
}

// EnvVarFor is BuildHint's other half: which variable assigns THIS image's
// profile a different image. An installation without a checkout cannot run a
// make target, and for it that variable is the whole answer.
//
// Empty for an image that belongs to no profile — there is nothing to override
// there, and naming a variable would suggest a knob that does not fit.
func EnvVarFor(overrides map[string]string, image string) string {
	image = strings.TrimSpace(image)
	for name, ref := range Images(overrides) {
		if ref == image {
			return EnvVar(name)
		}
	}
	return ""
}

// KnownImages lists the images of all profiles, sorted — the set the interface
// asks a runner about to say which workplaces are ready here.
func KnownImages(overrides map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, ref := range Images(overrides) {
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		out = append(out, ref)
	}
	sort.Strings(out)
	return out
}
