package sandbox

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

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

// Das fertige Image. Es ist die Antwort auf „woher nehme ich das?", die keine
// Maschine mit mehreren Gigabyte Bauzeit voraussetzt — und die einzige, die
// eine Container-Installation ueberhaupt ausfuehren kann.
//
// Die Adresse muss zu dem passen, was der Workflow veroeffentlicht
// (.github/workflows/sandbox-images.yml): ein Package, Varianten als
// Tag-Praefix. Zwei Stellen, eine Namensregel — deshalb steht sie hier fest.
func TestPublicImage(t *testing.T) {
	if got := PublicImage("dev"); got != "ghcr.io/benjaminledel/covey-sandbox:dev-latest" {
		t.Fatalf("PublicImage(dev) = %q", got)
	}
	if got := PublicImage("base"); got != "ghcr.io/benjaminledel/covey-sandbox:base-latest" {
		t.Fatalf("PublicImage(base) = %q", got)
	}
	// Kein Profil, keine Adresse — eine erfundene waere schlimmer als keine.
	if got := PublicImage("gibt-es-nicht"); got != "" {
		t.Fatalf("unbekanntes Profil: %q", got)
	}
	// Ueber die Image-Referenz gefragt, auch wenn die Instanz sie umbenannt hat.
	if got := PublicImageFor(map[string]string{"dev": "our-dev:1"}, "our-dev:1"); got == "" {
		t.Fatal("PublicImageFor findet das umbenannte Profil nicht")
	}
	if got := PublicImageFor(nil, "registry.example.com/team/sandbox:2026-08"); got != "" {
		t.Fatalf("fremdes Image darf keine Adresse bekommen: %q", got)
	}
}

// Die andere Haelfte der Antwort. Eine Installation als Container hat kein
// Repository und kann kein `make` ausfuehren — fuer sie ist die Variable der
// ganze Weg, und ohne sie las die Meldung wie eine Anweisung ins Leere.
func TestEnvVarForImage(t *testing.T) {
	dev, _ := Get("dev")
	if got := EnvVarFor(nil, dev.Image); got != "COVEY_SANDBOX_IMAGE_DEV" {
		t.Errorf("EnvVarFor(%q) = %q", dev.Image, got)
	}
	// Auch fuer ein umbenanntes Image: gemeldet wird das umbenannte, und die
	// Variable ist trotzdem die des Profils.
	if got := EnvVarFor(map[string]string{"dev": "our-dev:1"}, "our-dev:1"); got != "COVEY_SANDBOX_IMAGE_DEV" {
		t.Errorf("EnvVarFor(umbenannt) = %q", got)
	}
	// Ein fremdes Image gehoert zu keinem Profil — dort gibt es nichts zu
	// ueberschreiben, und eine Variable zu nennen behauptete einen Regler, der
	// nicht passt.
	if got := EnvVarFor(nil, "registry.example.com/team/sandbox:2026-08"); got != "" {
		t.Errorf("fremdes Image darf keine Variable nennen: %q", got)
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
	voll := KnownImages(nil)
	images := KnownImages(map[string]string{"dev": Images(nil)[DefaultName()]})
	if len(images) != len(voll)-1 {
		t.Errorf("identical images have to collapse into one: %v", images)
	}
	if !sort.StringsAreSorted(images) {
		t.Errorf("the list is not sorted: %v", images)
	}
}

// Every profile is one image and one build. The catalogue exists so that the
// answer to "how do I get this?" is not guessed — a build hint naming a
// Dockerfile that is not in the repository is exactly the guess it replaced.
func TestEveryProfileNamesAFileThatExists(t *testing.T) {
	seen := map[string]string{}
	for _, p := range All() {
		if p.Dockerfile == "" {
			t.Errorf("profile %q names no Dockerfile", p.Name)
			continue
		}
		if _, err := os.Stat(filepath.Join("..", "..", p.Dockerfile)); err != nil {
			t.Errorf("profile %q names %s, which is not there: %v", p.Name, p.Dockerfile, err)
		}
		if other, doppelt := seen[p.Image]; doppelt {
			t.Errorf("profiles %q and %q ship the same image %q", other, p.Name, p.Image)
		}
		seen[p.Image] = p.Name

		// And it copies ITS OWN self-description into the image. A role image
		// derived from another one by copy is one line away from telling its
		// agent about a toolchain that is not in it — and that line is the one
		// the agent believes (#112).
		roh, err := os.ReadFile(filepath.Join("..", "..", p.Dockerfile))
		if err != nil {
			continue
		}
		zeile := "COPY internal/sandbox/workplaces/" + p.Name + ".json /etc/covey/workplace.json"
		if !strings.Contains(string(roh), zeile) {
			t.Errorf("%s does not copy the description of %q into the image (%q)", p.Dockerfile, p.Name, zeile)
		}
	}
}

// The workflow that BUILDS the images names the profiles four times over — in
// the two build matrices, in the shell loop that collects the digests, and in
// the catalogue entry it writes. It has to: a GitHub matrix cannot ask a Go
// registry. What it must not do is drift, and the failure is quiet in both
// directions — a profile missing there is a workplace every instance offers and
// no host can pull; a profile only there is an image nobody knows.
//
// Which matrix a profile belongs in is not written down twice either: it
// follows from its Dockerfile. One that starts on `base` is built in the second
// stage beside the others; one that starts on `dev` has to wait until `dev` is
// published, and that is what the third stage is for.
func TestTheWorkflowBuildsExactlyTheseProfiles(t *testing.T) {
	roh, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "sandbox-images.yml"))
	if err != nil {
		t.Skipf("no workflow to compare against: %v", err)
	}
	workflow := string(roh)

	var alle, aufBase, aufDev []string
	for _, p := range All() {
		alle = append(alle, p.Name)
		if p.Name == "base" {
			// `base` is built by its own stage — everything else runs through
			// a matrix on top of something.
			continue
		}
		df, err := os.ReadFile(filepath.Join("..", "..", p.Dockerfile))
		if err != nil {
			t.Errorf("%s: %v", p.Dockerfile, err)
			continue
		}
		switch {
		case strings.Contains(string(df), "ARG BASE_IMAGE=covey-sandbox:latest"):
			aufBase = append(aufBase, p.Name)
		case strings.Contains(string(df), "ARG BASE_IMAGE=covey-sandbox-dev:latest"):
			aufDev = append(aufDev, p.Name)
		default:
			t.Errorf("%s names no base image this workflow knows how to stage", p.Dockerfile)
		}
	}

	fundstellen := []struct {
		was     string
		muster  string
		trenner string
		erwarte []string
	}{
		{"the second stage", `profile: \[(dev, [^\]]*)\]`, ",", aufBase},
		{"the third stage", `profile: \[(dev-full[^\]]*)\]`, ",", aufDev},
		{"the digest loop", `profiles="([^"]*)"`, " ", alle},
		{"the catalogue order", `reihenfolge = \[n for n in \(([^)]*)\)`, ",", alle},
	}
	for _, f := range fundstellen {
		treffer := regexp.MustCompile(f.muster).FindAllStringSubmatch(workflow, -1)
		if len(treffer) == 0 {
			t.Errorf("%s was not found in the workflow — has it been rewritten?", f.was)
			continue
		}
		for _, tr := range treffer {
			var namen []string
			for _, teil := range strings.Split(tr[1], f.trenner) {
				name := strings.Trim(strings.TrimSpace(teil), `"'`)
				if name != "" {
					namen = append(namen, name)
				}
			}
			if strings.Join(namen, " ") != strings.Join(f.erwarte, " ") {
				t.Errorf("%s says %v, the catalogue says %v", f.was, namen, f.erwarte)
			}
		}
	}
}

// The Flutter images share one install script, and they have to: the version
// would otherwise stand in two Dockerfiles and drift apart at the first bump.
// The workplace descriptions name it a third and fourth time, because that is
// the line an agent reads before deciding whether it has to fetch an SDK.
func TestTheFlutterImagesAgreeOnTheirVersion(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "internal", "sandbox", "install-flutter.sh"))
	if err != nil {
		t.Fatalf("the shared install script is gone: %v", err)
	}
	m := regexp.MustCompile(`FLUTTER_VERSION="\$\{FLUTTER_VERSION:-([^}"]+)\}"`).FindStringSubmatch(string(script))
	if m == nil {
		t.Fatal("the script names no default Flutter version")
	}
	version := m[1]

	for _, profil := range []string{"dev-flutter", "dev-full"} {
		doc, ok := Workplace(profil)
		if !ok {
			t.Errorf("%s has no description", profil)
			continue
		}
		var genannt string
		for _, tool := range doc.Tools {
			if tool.Name == "flutter" {
				genannt = tool.Version
			}
		}
		if genannt != version {
			t.Errorf("%s tells the agent about Flutter %q, the image installs %q",
				profil, genannt, version)
		}
		df, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile.sandbox."+profil))
		if err != nil {
			t.Errorf("%s: %v", profil, err)
			continue
		}
		if !strings.Contains(string(df), "install-flutter.sh") {
			t.Errorf("Dockerfile.sandbox.%s installs Flutter past the shared script", profil)
		}
	}
}
