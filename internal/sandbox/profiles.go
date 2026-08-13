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
		Name:        "base",
		Label:       "base",
		Description: "Node, git, chromium, ripgrep — enough for support, mail, QA and research agents.",
		Image:       "covey-sandbox:latest",
		Build:       "make sandbox-image",
		Dockerfile:  "Dockerfile.sandbox",
		Default:     true,
	})
	Register(Profile{
		Name:  "dev",
		Label: "dev",
		Description: "Everything from base plus PHP, JDK, fvm, uv and the node-gyp toolchain — " +
			"for agents that build software. A profile is a union of toolchains and not a cut " +
			"along languages: a developer agent works on PHP and Flutter projects, a mail agent " +
			"on neither.",
		Image:      "covey-sandbox-dev:latest",
		Build:      "make sandbox-image-dev",
		Dockerfile: "Dockerfile.sandbox.dev",
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
