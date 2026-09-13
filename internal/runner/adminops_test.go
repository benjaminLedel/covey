package runner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"

	"covey/internal/sandbox"
)

// Asking the hosts which images they hold is what the workplace page shows.
// The interesting part is not the answer but the question: an image named
// twice is asked about once, and blanks are not asked about at all — a host
// answering "is ” present?" would be a question nobody can answer.
func TestWorkplaceImagesAsksEachImageOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	orgID := uuid.New()
	p, _ := newLocalPool(t, dir, fakeDockerBin(t, dir, "nothing"), orgID)

	got := p.WorkplaceImages(context.Background(), orgID,
		[]string{"covey-sandbox:test", "covey-sandbox:test", "", "   "})
	// The local Docker is a shell script that reports nothing as present, so
	// the answer is about the SHAPE: one entry, not four, and no empty key.
	if _, blank := got[""]; blank {
		t.Errorf("an empty image was asked about: %v", got)
	}

	// A foreign organisation's hosts are not asked at all — a runner of
	// another tenant is not a worse source, it is none.
	other := p.WorkplaceImages(context.Background(), uuid.New(), []string{"covey-sandbox:test"})
	if len(other) != 0 {
		t.Errorf("a foreign organisation got an answer: %v", other)
	}
}

// Pulling onto ONE host is the deliberate version of what the first wake would
// do anyway — so that a private registry without credentials answers here and
// not in the middle of a run.
func TestPullOnRefusesWhatItCannotAddress(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	orgID := uuid.New()
	p, runnerID := newLocalPool(t, dir, fakeDockerBin(t, dir, "nothing"), orgID)
	ctx := context.Background()

	// A name that is not a known workplace is NOT an error: it is taken as an
	// image reference, the same way an agent's workplace resolves — which is
	// what lets somebody pull an image the catalogue does not carry. And an
	// empty name means the default profile, not "nothing".
	image, _ := p.PullOn(ctx, orgID, runnerID, "registry.example/eigenes:1")
	if image != "registry.example/eigenes:1" {
		t.Errorf("an image reference resolved to %q", image)
	}
	image, _ = p.PullOn(ctx, orgID, runnerID, "")
	if image != "covey-sandbox:test" {
		t.Errorf("the empty name resolved to %q, expected the default workplace", image)
	}

	// A host of another organisation is not reachable from here.
	if _, err := p.PullOn(ctx, uuid.New(), runnerID, "base"); err == nil {
		t.Error("a host of a foreign organisation was addressed")
	}
	// And one that is not connected at all.
	if _, err := p.PullOn(ctx, orgID, uuid.New(), "base"); err == nil {
		t.Error("an unconnected host was addressed")
	}
}

// The release address is derived from one constant, and the same string stands
// in installer/install.sh. A fork changes both.
func TestReleaseBase(t *testing.T) {
	got := releaseBase("v0.8.0")
	if !strings.HasPrefix(got, "https://github.com/"+Repo+"/releases/download/") {
		t.Errorf("releaseBase = %q", got)
	}
	if !strings.HasSuffix(got, "/v0.8.0") {
		t.Errorf("the version is not in the address: %q", got)
	}
}

// The checksum is read out of the file sha256sum writes — hash, blanks, name.
// Reading the wrong line would mean verifying a binary against another one's
// hash, which fails safely but says nothing useful.
func TestChecksumFor(t *testing.T) {
	const sums = `abc123  covey-runner_linux_amd64.tar.gz
def456  covey-runner_darwin_arm64.tar.gz

# a comment line
`
	got, ok := checksumFor(sums, "covey-runner_darwin_arm64.tar.gz")
	if !ok || got != "def456" {
		t.Errorf("checksumFor = %q, %v", got, ok)
	}
	if _, ok := checksumFor(sums, "covey-runner_windows_amd64.zip"); ok {
		t.Error("a file that is not in the list got a checksum")
	}
	if _, ok := checksumFor("", "irgendwas"); ok {
		t.Error("an empty SHA256SUMS produced a checksum")
	}
}

// A failed start comes back with its output, and what says how it ended is the
// END of that output — a head would carry the pull progress and lose the line
// that names the fault.
func TestTailOfKeepsTheEnd(t *testing.T) {
	long := strings.Repeat("a\n", 500) + "letzte zeile"
	got := tailOf(long)
	if !strings.HasSuffix(got, "letzte zeile") {
		t.Errorf("the tail lost the end: %q", got[len(got)-30:])
	}
	if !strings.HasPrefix(got, "…") {
		t.Errorf("a cut output does not say it was cut: %q", got[:10])
	}
	if len(got) >= len(long) {
		t.Errorf("nothing was cut: %d vs %d", len(got), len(long))
	}
	if got := tailOf("kurz"); got != "kurz" {
		t.Errorf("a short text was cut: %q", got)
	}
}

// cut names examples rather than printing a list nobody reads: a home has six
// figures of files, and five of them say what kind of thing is missing.
func TestCutNamesExamples(t *testing.T) {
	long := []string{"a", "b", "c", "d", "e", "f", "g"}
	got := cut(long, 3)
	if len(got) != 4 || got[3] != "…" {
		t.Errorf("cut = %v", got)
	}
	if got[0] != "a" || got[2] != "c" {
		t.Errorf("cut took the wrong ones: %v", got)
	}
	// Shorter than the bound: handed back whole, without an ellipsis that
	// would claim something was left out.
	short := []string{"a", "b"}
	if got := cut(short, 3); len(got) != 2 {
		t.Errorf("a short list was cut: %v", got)
	}
	// And the original is not disturbed — the caller still holds the full list.
	if len(long) != 7 {
		t.Errorf("cut changed the list it was given: %v", long)
	}
}

// The working directory a runner keeps its homes in has to exist before the
// first one is written into it.
func TestDockerDataDirIsUsable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tief", "drin")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	d := &Docker{RunnerID: uuid.New(), Image: "covey-sandbox:test", DataDir: dir}
	if d.DataDir != dir {
		t.Errorf("DataDir = %q", d.DataDir)
	}
}

// Which image a workplace name means comes from three layers: what was wired
// in, what the environment overrides, and what the published catalogue says.
// The order is deliberate and it is what keeps "which image does `dev` mean"
// from depending on who looks first.
func TestProfilesLayerTheEnvironmentOverTheDefaults(t *testing.T) {
	ctx := context.Background()

	// Nothing to layer: what was wired in stands. That is also what the tests
	// build, and they should not need a catalogue.
	bare := NewPool(quietLog())
	bare.Profiles = map[string]string{"base": "wired:1"}
	if got := bare.profiles(ctx)["base"]; got != "wired:1" {
		t.Errorf("without a catalogue the wired image is %q", got)
	}

	// With an override, the compiled defaults are layered under it: the named
	// profile takes the operator's image, and the ones they said nothing about
	// keep theirs.
	p := NewPool(quietLog())
	p.EnvImages = map[string]string{sandbox.DefaultName(): "von-hand:1"}
	eff := p.profiles(ctx)
	if eff[sandbox.DefaultName()] != "von-hand:1" {
		t.Errorf("the override did not win: %q", eff[sandbox.DefaultName()])
	}
	if len(eff) < len(sandbox.All()) {
		t.Errorf("an override dropped the profiles it did not name: %d of %d", len(eff), len(sandbox.All()))
	}
	for name, image := range eff {
		if image == "" {
			t.Errorf("profile %q resolves to nothing", name)
		}
	}

	// And the answer is cached for a minute: the catalogue sits behind a URL,
	// and asking it on every wake would put a fetch in front of every start.
	p.EnvImages = map[string]string{sandbox.DefaultName(): "spaeter:2"}
	if again := p.profiles(ctx)[sandbox.DefaultName()]; again != "von-hand:1" {
		t.Errorf("the cache was bypassed: %q", again)
	}
}
