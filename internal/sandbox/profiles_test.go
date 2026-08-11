package sandbox

import "testing"

// The catalogue exists so that one list does not become four. What that costs
// if it slips is not a compile error but a wrong sentence in an error message —
// hence the tests on exactly those answers.

func TestCatalogueHasADefault(t *testing.T) {
	name := DefaultName()
	if name == "" {
		t.Fatal("without a default profile an agent that chooses nothing has no workplace")
	}
	p, ok := Get(name)
	if !ok || p.Image == "" {
		t.Fatalf("the default profile %q has no image", name)
	}
	var defaults int
	for _, p := range All() {
		if p.Default {
			defaults++
		}
		if p.Build == "" || p.Image == "" || p.Description == "" {
			t.Errorf("profile %q is incomplete: %+v", p.Name, p)
		}
	}
	if defaults != 1 {
		t.Errorf("exactly one profile may be the default, found %d", defaults)
	}
}

func TestImagesTakeTheOverride(t *testing.T) {
	images := Images(map[string]string{"dev": "registry.example.com/team/dev:2026-08"})
	if images["dev"] != "registry.example.com/team/dev:2026-08" {
		t.Errorf("override was not applied: %q", images["dev"])
	}
	base, _ := Get(DefaultName())
	if images[base.Name] != base.Image {
		t.Errorf("a profile without an override keeps its image: %q", images[base.Name])
	}
	// The catalogue must not carry the override away with it — the next caller
	// would get a foreign instance's image.
	if p, _ := Get("dev"); p.Image == "registry.example.com/team/dev:2026-08" {
		t.Error("Images() wrote back into the catalogue")
	}
}

func TestBuildHintOnlyForOwnImages(t *testing.T) {
	dev, _ := Get("dev")
	if got := BuildHint(nil, dev.Image); got != dev.Build {
		t.Errorf("hint for %q: %q, expected %q", dev.Image, got, dev.Build)
	}
	// A foreign image gets none: no make target of this repository produces it,
	// and naming one anyway is the mistake the string comparison on the image
	// name used to make.
	if got := BuildHint(nil, "registry.example.com/team/sandbox:2026-08"); got != "" {
		t.Errorf("a foreign image must not carry a build hint: %q", got)
	}
	// With an override the hint has to follow the renamed image, because that
	// is the one the runner reports as missing.
	over := map[string]string{"dev": "our-dev:1"}
	if got := BuildHint(over, "our-dev:1"); got != dev.Build {
		t.Errorf("hint for the overridden image: %q", got)
	}
}

func TestEnvVarNames(t *testing.T) {
	if got := EnvVar("dev"); got != "COVEY_SANDBOX_IMAGE_DEV" {
		t.Errorf("EnvVar(dev) = %q", got)
	}
	// A profile with a hyphen would otherwise produce a variable no shell can
	// set.
	if got := EnvVar("ml-gpu"); got != "COVEY_SANDBOX_IMAGE_ML_GPU" {
		t.Errorf("EnvVar(ml-gpu) = %q", got)
	}
}

func TestKnownImagesAreUniqueAndSorted(t *testing.T) {
	// Two profiles on one image is a legitimate configuration (whoever points
	// dev at base has no dev toolchain, and knows it) — asking about it twice
	// is not.
	images := KnownImages(map[string]string{"dev": Images(nil)[DefaultName()]})
	if len(images) != 1 {
		t.Errorf("identical images have to collapse into one: %v", images)
	}
}
