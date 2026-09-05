package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/engines"
	"covey/internal/homestore"
)

/* Zwei Vorgänge machen den Großteil der Wartezeit vor dem ersten Zug eines
   Agenten aus: das Bild holen und das Home herstellen. Ein dritter, das
   Zurückschreiben, hängt hinten dran. Alle drei haben früher nichts von sich
   gesagt, solange sie liefen — gemeldet wurde erst das Ergebnis, und bei einem
   Runner, der mittendrin neu startete, gar nichts. Hier steht, dass sie sich
   melden. */

// fortschrittsDocker: ein Docker, das kein Bild hat und beim Holen die Zeilen
// schreibt, die ein echtes docker pull schreibt.
func fortschrittsDocker(t *testing.T, dir string) string {
	t.Helper()
	pfad := filepath.Join(dir, "docker")
	skript := `#!/bin/sh
if [ "$1" = "image" ]; then exit 1; fi
if [ "$1" = "pull" ]; then
  echo "latest: Pulling from covey/sandbox"
  echo "aaaa: Downloading [==>        ]  400MB/2GB"
  echo "aaaa: Pull complete"
  exit 0
fi
if [ "$1" = "wait" ]; then sleep 30; exit 0; fi
echo ok
`
	if err := os.WriteFile(pfad, []byte(skript), 0o755); err != nil {
		t.Fatal(err)
	}
	return pfad
}

func TestDerBildAbrufMeldetSichVorherUndNachher(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("das Fake-Binary ist ein Shell-Skript")
	}
	dir := t.TempDir()
	orgID, runnerID, agentID := uuid.New(), uuid.New(), uuid.New()
	node := NewNode(runnerID, orgID, &Docker{
		RunnerID: runnerID, Image: "covey-sandbox:test", DataDir: dir,
		DockerBin: fortschrittsDocker(t, dir),
	}, quietLog())
	t.Cleanup(node.Close)

	control, nodeEnd := NewInProc()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = node.Run(ctx, nodeEnd) }()

	if _, err := control.Receive(ctx); err != nil { // registriert
		t.Fatalf("Registrierung: %v", err)
	}
	start, err := encode(TypeStartSandbox, "1", StartSandbox{
		AgentID: agentID, OrgID: orgID, Image: "covey/sandbox:latest",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := control.Send(ctx, start); err != nil {
		t.Fatal(err)
	}

	// Zwei Meldungen: „ich hole jetzt" (bevor es passiert — hinterher braucht
	// es niemand mehr) und „ich habe geholt", mit der Größe.
	anfang := warteAufFortschritt(t, ctx, control, PhaseImage, false)
	if anfang.Detail != "covey/sandbox:latest" {
		t.Fatalf("die Anfangsmeldung nennt das Bild nicht: %+v", anfang)
	}
	ende := warteAufFortschritt(t, ctx, control, PhaseImage, true)
	if ende.BytesTotal != 2_000_000_000 {
		t.Fatalf("die Schlussmeldung trägt %d Bytes, erwartet 2 GB aus den Fortschrittszeilen", ende.BytesTotal)
	}
}

func TestDasZurueckschreibenMeldetSichVorherUndNachher(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("das Fake-Binary ist ein Shell-Skript")
	}
	dir := t.TempDir()
	orgID, runnerID, agentID := uuid.New(), uuid.New(), uuid.New()
	blobs, err := homestore.NewDir(filepath.Join(dir, "blocks"))
	if err != nil {
		t.Fatal(err)
	}
	docker := &Docker{
		RunnerID: runnerID, Image: "covey-sandbox:test", DataDir: dir,
		DockerBin: fakeDockerBin(t, dir, "nothing"),
	}
	node := NewNode(runnerID, orgID, docker, quietLog())
	node.Blobs = blobs
	t.Cleanup(node.Close)

	// Ein Home mit Inhalt, sonst hat der Sync nichts zu tun.
	home, _, _ := docker.AgentHome(agentID)
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "NOTIZ.md"), []byte("etwas Arbeit"), 0o644); err != nil {
		t.Fatal(err)
	}

	control, nodeEnd := NewInProc()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = node.Run(ctx, nodeEnd) }()

	if _, err := control.Receive(ctx); err != nil {
		t.Fatalf("Registrierung: %v", err)
	}
	req, err := encode(TypeSyncHome, "1", SyncHome{AgentID: agentID, OrgID: orgID})
	if err != nil {
		t.Fatal(err)
	}
	if err := control.Send(ctx, req); err != nil {
		t.Fatal(err)
	}

	warteAufFortschritt(t, ctx, control, PhaseHomeSync, false)
	ende := warteAufFortschritt(t, ctx, control, PhaseHomeSync, true)
	if ende.Bytes == 0 {
		t.Fatal("die Schlussmeldung trägt keine Bytes — genau die Zahl macht sie lesbar")
	}
}

// Ein Vorgang, der jede Datei meldet, füllt die Aufzeichnung mit sich selbst.
// Gedrosselt heißt: höchstens eine Meldung je progressEvery, egal wie oft der
// Vorgang etwas zu sagen hätte.
func TestFortschrittWirdGedrosselt(t *testing.T) {
	orgID, runnerID := uuid.New(), uuid.New()
	node := NewNode(runnerID, orgID, &Docker{RunnerID: runnerID, DataDir: t.TempDir()}, quietLog())
	t.Cleanup(node.Close)

	control, nodeEnd := NewInProc()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	melden := node.gate(ctx, nodeEnd)
	for i := 0; i < 100; i++ {
		melden(Progress{AgentID: uuid.New(), Phase: PhaseHome, Count: int64(i)})
	}
	// Der InProc-Transport puffert; was durchkam, steht sofort bereit.
	kurz, abbrechen := context.WithTimeout(ctx, 200*time.Millisecond)
	defer abbrechen()
	var durch int
	for {
		if _, err := control.Receive(kurz); err != nil {
			break
		}
		durch++
	}
	if durch != 0 {
		t.Fatalf("%d Meldungen kamen durch, erwartet 0 innerhalb von %s", durch, progressEvery)
	}
}

func warteAufFortschritt(t *testing.T, ctx context.Context, control Transport, phase string, fertig bool) Progress {
	t.Helper()
	frist, abbrechen := context.WithTimeout(ctx, 15*time.Second)
	defer abbrechen()
	for {
		msg, err := control.Receive(frist)
		if err != nil {
			t.Fatalf("keine Meldung für Phase %q (fertig=%v): %v", phase, fertig, err)
		}
		if msg.Type != TypeProgress {
			continue
		}
		p, err := decode[Progress](msg)
		if err != nil {
			t.Fatal(err)
		}
		if p.Phase == phase && p.Done == fertig {
			return p
		}
	}
}

// The store's own account of an install, beside the node's — the two ends of one
// reporting path, so a change to either is read in one place. What it holds to:
// an install that takes a minute says what it is doing while it does it (the byte
// figures are why — "starting" was the whole answer for the image pull too, until
// that learned to count), it ends (a phase that never ends is worse than none,
// because it claims work that is already finished), and a cache hit ends too, in
// the same millisecond it started.
//
// It reaches the store through a catalogue rather than a hand-built Release,
// because the store's engine name lives on an unexported field: what a caller
// outside the package can hold is what a catalogue hands it, and that is the
// honest thing to test with.
func TestEnsureWatchedTellsWhereItGotTo(t *testing.T) {
	dir := t.TempDir()
	art := engineTarball(t)
	artPath := filepath.Join(dir, "engine.tgz")
	if err := os.WriteFile(artPath, art, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(art)
	catPath := filepath.Join(dir, "catalogue.json")
	body := []byte(`{"schema":1,"engines":[{"name":"sevencode","versions":[` +
		`{"version":"1.0.8","kind":"tarball","url":"file://` + artPath +
		`","integrity":"sha256:` + hex.EncodeToString(sum[:]) + `"}]}]}`)
	if err := os.WriteFile(catPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	r, ok := engines.NewSource("file://"+catPath, nil, nil).For(context.Background(), "sevencode", "")
	if !ok {
		t.Fatal("the catalogue names this engine")
	}
	store := &engines.Store{Dir: filepath.Join(dir, "engines")}

	var seen []engines.Progress
	if _, err := store.EnsureWatched(context.Background(), r,
		func(p engines.Progress) { seen = append(seen, p) }); err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(seen) < 2 {
		t.Fatalf("an install of %d bytes reported %d times: %+v", len(art), len(seen), seen)
	}
	if !strings.Contains(seen[0].Detail, "sevencode") || !strings.Contains(seen[0].Detail, "1.0.8") {
		t.Fatalf("the first report names what is being installed, got %q", seen[0].Detail)
	}
	if !seen[len(seen)-1].Done {
		t.Fatalf("the last report has to end the phase: %+v", seen[len(seen)-1])
	}
	var bekannt bool
	for _, p := range seen {
		if p.Bytes == int64(len(art)) && p.BytesTotal == int64(len(art)) {
			bekannt = true
		}
	}
	if !bekannt {
		t.Fatalf("no report carries the artefact's size on both sides (%d bytes): %+v", len(art), seen)
	}

	// The layer stands now. One report, ending at once — a step the display shows
	// running for a second after it finished is a step nobody believes the next
	// time.
	seen = nil
	if _, err := store.EnsureWatched(context.Background(), r,
		func(p engines.Progress) { seen = append(seen, p) }); err != nil {
		t.Fatalf("second install: %v", err)
	}
	if len(seen) != 1 || !seen[0].Done {
		t.Fatalf("a layer already standing is one ending report, got %+v", seen)
	}
}

// The fourth phase of a start: an engine the catalogue names is not on this host
// yet, and fetching it is the same kind of wait as pulling an image and the same
// order of magnitude — a self-contained engine is a hundred and fifty megabytes.
// It reports itself the way the pull does: once before it begins, because
// afterwards nobody needs it, and once at the end with the figures.
func TestEngineInstallReportsItsPhase(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	orgID, runnerID, agentID := uuid.New(), uuid.New(), uuid.New()

	art := engineTarball(t)
	artPath := filepath.Join(dir, "sevencode.tgz")
	if err := os.WriteFile(artPath, art, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(art)
	catPath := filepath.Join(dir, "engines.json")
	body := []byte(`{"schema":1,"engines":[{"name":"sevencode","versions":[` +
		`{"version":"1.0.8","kind":"tarball","url":"file://` + artPath +
		`","integrity":"sha256:` + hex.EncodeToString(sum[:]) + `"}]}]}`)
	if err := os.WriteFile(catPath, body, 0o644); err != nil {
		t.Fatal(err)
	}

	node := NewNode(runnerID, orgID, &Docker{
		RunnerID: runnerID, Image: "covey-sandbox:test", DataDir: dir,
		DockerBin:   fortschrittsDocker(t, dir),
		Engines:     engines.NewSource("file://"+catPath, nil, nil),
		EngineStore: &engines.Store{Dir: filepath.Join(dir, "engines")},
	}, quietLog())
	t.Cleanup(node.Close)

	control, nodeEnd := NewInProc()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = node.Run(ctx, nodeEnd) }()
	if _, err := control.Receive(ctx); err != nil {
		t.Fatalf("registration: %v", err)
	}

	start, err := encode(TypeStartSandbox, "1", StartSandbox{
		AgentID: agentID, OrgID: orgID, Image: "covey/sandbox:latest", Engine: "sevencode",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := control.Send(ctx, start); err != nil {
		t.Fatal(err)
	}

	begin := warteAufFortschritt(t, ctx, control, PhaseEngine, false)
	if !strings.Contains(begin.Detail, "sevencode") || !strings.Contains(begin.Detail, "1.0.8") {
		t.Fatalf("the opening report names the engine and version: %+v", begin)
	}
	end := warteAufFortschritt(t, ctx, control, PhaseEngine, true)
	if end.BytesTotal != int64(len(art)) {
		t.Fatalf("the closing report carries the artefact's %d bytes, got %d", len(art), end.BytesTotal)
	}
	if _, err := os.Stat(filepath.Join(dir, "engines", "sevencode", "1.0.8", "bin", "sevencode")); err != nil {
		t.Fatalf("the phase ended before the layer was there: %v", err)
	}
}
