package runner

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/buildinfo"
)

// release is a stand-in for the published artefacts: one archive per platform
// and the checksum file that is their table of contents.
func release(t *testing.T, version, content string) *httptest.Server {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte(content)
	if err := tw.WriteHeader(&tar.Header{Name: "covey-runner", Mode: 0o755, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	archive := buf.Bytes()
	name := fmt.Sprintf("covey-runner_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(archive)
	sums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), name)

	mux := http.NewServeMux()
	mux.HandleFunc("/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sums))
	})
	mux.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

// A host replaces its own binary and says what became of it. The whole point is
// that this does not need an SSH session: runner and control plane are
// delivered separately, so they drift apart, and a fix in the data plane used
// to mean logging into every machine that has one.
func TestARunnerReplacesItsOwnBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no exec that replaces the process")
	}
	dir := t.TempDir()
	// The "binary" this test runs as: updateSelf replaces the file the process
	// was started from, so the test points it at a copy of its own path.
	exe := filepath.Join(dir, "covey-runner")
	if err := os.WriteFile(exe, []byte("the old one"), 0o755); err != nil {
		t.Fatal(err)
	}
	origin := release(t, "v9.9.9", "the new one")

	restarted := 0
	node := NewNode(uuid.New(), uuid.New(), &Docker{DataDir: dir}, quietLog())
	node.Restart = func() error { restarted++; return nil }
	node.executable = func() (string, error) { return exe, nil }

	res := node.updateSelf(context.Background(), Update{Version: "v9.9.9", BaseURL: origin.URL})
	if res.Err != "" {
		t.Fatalf("update: %s", res.Err)
	}
	if !res.Restarting || res.To != "v9.9.9" {
		t.Errorf("the answer has to say what happens next: %+v", res)
	}
	if got, err := os.ReadFile(exe); err != nil || string(got) != "the new one" {
		t.Errorf("the binary was not replaced: %q, %v", got, err)
	}
	// Executable, or the restart runs into a file nobody may start.
	if info, err := os.Stat(exe); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Errorf("the new binary is not executable: %v, %v", info.Mode(), err)
	}
}

// What is downloaded here is a program that is then run. "Over HTTPS" is a
// statement about the transport and not about the file — so the checksum
// decides, and a mismatch leaves the old binary exactly where it was.
func TestAnArchiveThatDoesNotMatchItsChecksumIsNotInstalled(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "covey-runner")
	if err := os.WriteFile(exe, []byte("the old one"), 0o755); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("covey-runner_v9.9.9_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	mux := http.NewServeMux()
	mux.HandleFunc("/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", "00000000000000000000000000000000000000000000000000000000000000ff", name)
	})
	mux.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("something else entirely"))
	})
	origin := httptest.NewServer(mux)
	defer origin.Close()

	node := NewNode(uuid.New(), uuid.New(), &Docker{DataDir: dir}, quietLog())
	node.Restart = func() error { t.Error("a failed update must not restart anything"); return nil }
	node.executable = func() (string, error) { return exe, nil }

	res := node.updateSelf(context.Background(), Update{Version: "v9.9.9", BaseURL: origin.URL})
	if res.Err == "" || res.Restarting {
		t.Fatalf("an archive that does not match must not be installed: %+v", res)
	}
	if got, _ := os.ReadFile(exe); string(got) != "the old one" {
		t.Errorf("the old binary was touched: %q", got)
	}
}

// Not while it is carrying anything. Replacing the process would survive the
// containers — they belong to Docker — but not the watchers, and a sandbox
// nobody watches any more is worse than an update that waits.
func TestAnUpdateWaitsForTheSandboxes(t *testing.T) {
	dir := t.TempDir()
	node := NewNode(uuid.New(), uuid.New(), &Docker{DataDir: dir}, quietLog())
	node.Restart = func() error { t.Error("must not restart while a sandbox is running"); return nil }
	node.mu.Lock()
	node.running[uuid.New()] = &sandboxProc{container: "c1", cancel: func() {}}
	node.mu.Unlock()

	res := node.updateSelf(context.Background(), Update{Version: "v9.9.9", BaseURL: "http://127.0.0.1:1"})
	if res.Err == "" || res.Restarting {
		t.Fatalf("a busy host has to refuse: %+v", res)
	}
	if !bytes.Contains([]byte(res.Err), []byte("sandbox")) {
		t.Errorf("the reason has to name what is in the way: %q", res.Err)
	}
}

// The built-in runner is this process. Offering it an update would download a
// binary nobody would ever start.
func TestTheBuiltInRunnerIsNotUpdatedThroughTheProtocol(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	org := uuid.New()
	p, runnerID := newLocalPool(t, dir, fakeDockerBin(t, dir, "nothing"), org)
	if _, err := p.Update(context.Background(), org, runnerID, "v9.9.9", ""); err == nil {
		t.Fatal("the built-in runner has to refuse the update")
	}
}

// A build from before the self-update existed ignores the message — it does not
// answer it, because it does not know it. Waiting that out would take the whole
// update timeout and end in "does not answer", which is the sentence this
// platform uses for a broken host. So the registration says what a build can do,
// and the answer is immediate and names the way out.
func TestARunnerTooOldToUpdateItselfSaysSoAtOnce(t *testing.T) {
	orgID := uuid.New()
	p := NewPool(quietLog())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	control, node := NewInProc()
	defer control.Close()
	go func() {
		// No Features: the shape of every runner in the field today.
		hello, _ := encode(TypeRegistered, "", Registered{RunnerID: uuid.New(), OrgID: orgID, Protocol: Protocol})
		_ = node.Send(ctx, hello)
		<-ctx.Done()
	}()
	go func() { _ = p.Attach(ctx, control, false) }()
	waitUntil(t, 3*time.Second, func() bool { return len(p.LiveFor(orgID)) == 1 })

	var id uuid.UUID
	for k := range p.LiveFor(orgID) {
		id = k
	}
	done := make(chan error, 1)
	go func() { _, err := p.Update(ctx, orgID, id, "v9.9.9", ""); done <- err }()
	select {
	case err := <-done:
		if err == nil || !errors.Is(err, ErrNotSupported) {
			t.Fatalf("an old runner has to be named as such: %v", err)
		}
		if !strings.Contains(err.Error(), "install.sh") {
			t.Errorf("the message has to say what to do instead: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the answer waited on a message the runner does not know")
	}
}

// Die Absage bekommt ein Feld und nicht nur einen Satz: die Steuerebene macht
// aus ihr einen Plan, und etwas an einer Zeichenkette festzumachen, die jemand
// umformulieren darf, hört irgendwann still auf zu funktionieren.
func TestEineBeschaeftigteAbsageSagtDasAuchImFeld(t *testing.T) {
	dir := t.TempDir()
	node := NewNode(uuid.New(), uuid.New(), &Docker{DataDir: dir}, quietLog())
	node.mu.Lock()
	node.running[uuid.New()] = &sandboxProc{container: "c1", cancel: func() {}}
	node.mu.Unlock()

	res := node.updateSelf(context.Background(), Update{Version: "v9.9.9", BaseURL: "http://127.0.0.1:1"})
	if !res.Busy {
		t.Errorf("die Absage wegen laufender Sandboxen muss als solche erkennbar sein: %+v", res)
	}
}

// Ein vorgemerktes Update wartet auf die Lücke — und der Kapazitätsbericht ist
// die Stelle, an der die Lücke sichtbar wird. Ohne das blieb das Warten am
// Menschen hängen: drücken, abgelehnt, später nochmal.
func TestEinVorgemerktesUpdateLaeuftInDerLuecke(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("das Docker-Double ist ein Shell-Skript")
	}
	orgID, runnerID := uuid.New(), uuid.New()
	p := NewPool(quietLog())
	var gefragt, erledigt int
	var erledigtMit string
	p.PlannedUpdate = func(context.Context, uuid.UUID) (string, error) {
		gefragt++
		return "v9.9.9", nil
	}
	p.PlannedUpdateDone = func(_ context.Context, _ uuid.UUID, version string) {
		erledigt++
		erledigtMit = version
	}

	// Eine Verbindung, die sich für die gewünschte Fassung hält: dann ist der
	// Wunsch erfüllt, ohne dass irgendetwas ersetzt werden muss.
	c := &conn{pool: p, runnerID: runnerID, orgID: orgID, version: "v9.9.9"}
	c.runPlannedUpdate(context.Background())
	if gefragt != 1 {
		t.Errorf("der Plan wurde nicht abgefragt (%d)", gefragt)
	}
	if erledigt != 1 || erledigtMit != "v9.9.9" {
		t.Errorf("ein Host, der schon dort ist, erfüllt den Plan: %d/%q", erledigt, erledigtMit)
	}
}

// Nach einem Versuch wird nicht sofort wieder losgelaufen: sonst liefe ein
// Update, das an einem kaputten Download scheitert, alle dreißig Sekunden neu.
func TestNachEinemVersuchWirdNichtSofortWiederGefragt(t *testing.T) {
	p := NewPool(quietLog())
	var gefragt int
	p.PlannedUpdate = func(context.Context, uuid.UUID) (string, error) {
		gefragt++
		return "v9.9.9", nil
	}
	c := &conn{pool: p, runnerID: uuid.New(), orgID: uuid.New(), version: "v9.9.9"}
	c.runPlannedUpdate(context.Background())
	c.runPlannedUpdate(context.Background())
	if gefragt != 1 {
		t.Errorf("%d Abfragen kurz hintereinander — die Bremse greift nicht", gefragt)
	}
}

// Ohne Plan passiert nichts, und der eingebaute Runner wird gar nicht erst
// gefragt: er wird mit der Steuerebene aktualisiert.
func TestOhnePlanUndBeimEingebautenPassiertNichts(t *testing.T) {
	p := NewPool(quietLog())
	var gefragt int
	p.PlannedUpdate = func(context.Context, uuid.UUID) (string, error) {
		gefragt++
		return "", nil
	}
	c := &conn{pool: p, runnerID: uuid.New(), orgID: uuid.New()}
	c.runPlannedUpdate(context.Background())
	if gefragt != 1 {
		t.Fatalf("ohne Plan darf gefragt werden, aber genau einmal (%d)", gefragt)
	}
	eingebaut := &conn{pool: p, runnerID: uuid.New(), orgID: uuid.New(), builtin: true}
	eingebaut.runPlannedUpdate(context.Background())
	if gefragt != 1 {
		t.Errorf("der eingebaute Runner wird nach keinem Plan gefragt (%d)", gefragt)
	}
}

/* Ein Host, der sein eigenes Binary gebaut hat, trägt den Namen des Tags, auf
   dem sein Baum steht — und ist trotzdem etwas anderes. Auf covey.work lief
   „v0.7.2 (45c9c48-dirty)", während v0.7.2 die neueste Veröffentlichung war:
   Der Vergleich sagte „schon aktuell", der Knopf meldete Erfolg, ersetzt wurde
   nichts. Tagelang, und niemand sah, warum der Host zurückblieb. */

func TestEinSchmutzigerBauGiltNichtAlsDieVeroeffentlichung(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("kein exec, das den Prozess ersetzt")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "covey-runner")
	if err := os.WriteFile(exe, []byte("selbst gebaut"), 0o755); err != nil {
		t.Fatal(err)
	}
	origin := release(t, "v0.7.2", "die Veröffentlichung")

	// Derselbe Name, anderer Baum.
	alt := buildinfo.Get
	buildinfo.Get = func() buildinfo.Info {
		return buildinfo.Info{Version: "v0.7.2", Commit: "45c9c48", Dirty: true}
	}
	defer func() { buildinfo.Get = alt }()

	node := NewNode(uuid.New(), uuid.New(), &Docker{DataDir: dir}, quietLog())
	node.Restart = func() error { return nil }
	node.executable = func() (string, error) { return exe, nil }

	res := node.updateSelf(context.Background(), Update{Version: "v0.7.2", BaseURL: origin.URL})
	if res.Err != "" {
		t.Fatalf("update: %s", res.Err)
	}
	if !res.Restarting {
		t.Fatal("nichts ersetzt — genau die stille Absage aus #81")
	}
	if got, _ := os.ReadFile(exe); string(got) != "die Veröffentlichung" {
		t.Fatalf("das Binary blieb das alte: %q", got)
	}
}

// Die Gegenprobe, und sie ist die wichtigere: ein sauberer Bau auf derselben
// Fassung wird NICHT ersetzt. Sonst lüde jeder Knopfdruck dieselben Megabyte
// noch einmal und startete den Host ohne Grund neu.
func TestEinSauberesBinaryAufDerselbenFassungBleibtLiegen(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "covey-runner")
	if err := os.WriteFile(exe, []byte("die Veröffentlichung"), 0o755); err != nil {
		t.Fatal(err)
	}

	alt := buildinfo.Get
	buildinfo.Get = func() buildinfo.Info {
		return buildinfo.Info{Version: "v0.7.2", Commit: "abc1234"}
	}
	defer func() { buildinfo.Get = alt }()

	node := NewNode(uuid.New(), uuid.New(), &Docker{DataDir: dir}, quietLog())
	node.Restart = func() error { t.Fatal("es wurde neu gestartet"); return nil }
	node.executable = func() (string, error) { return exe, nil }

	// Ohne BaseURL: Würde er etwas holen wollen, liefe er ins Netz und
	// scheiterte — hier soll er gar nicht erst losgehen.
	res := node.updateSelf(context.Background(), Update{Version: "v0.7.2"})
	if res.Restarting || res.Err != "" {
		t.Fatalf("er hat angefasst, was schon stimmte: %+v", res)
	}
	if res.From != "v0.7.2" || res.To != "v0.7.2" {
		t.Fatalf("die Antwort nennt die Fassung nicht zweimal: %+v", res)
	}
	if got, _ := os.ReadFile(exe); string(got) != "die Veröffentlichung" {
		t.Fatalf("das Binary wurde angefasst: %q", got)
	}
}

// The busy check runs twice: before the download and again after it, since a
// download has ten minutes to run and a sync may begin in that time. A host
// that became busy while downloading keeps its old binary and says so (#161).
func TestAHostThatBecomesBusyDuringTheDownloadIsNotReplaced(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no exec that replaces the process")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "covey-runner")
	if err := os.WriteFile(exe, []byte("the old one"), 0o755); err != nil {
		t.Fatal(err)
	}
	node := NewNode(uuid.New(), uuid.New(), &Docker{DataDir: dir}, quietLog())
	node.executable = func() (string, error) { return exe, nil }

	// The release server is where the download happens — and where the host
	// becomes busy in the middle of it: a working copy enters the queue.
	origin := release(t, "v9.9.9", "the new one")
	busyDuring := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/SHA256SUMS" {
			node.mu.Lock()
			node.turn[uuid.New()] = make(chan struct{})
			node.mu.Unlock()
		}
		resp, err := http.Get(origin.URL + r.URL.Path)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		defer resp.Body.Close()
		_, _ = io.Copy(w, resp.Body)
	}))
	t.Cleanup(busyDuring.Close)

	res := node.updateSelf(context.Background(), Update{Version: "v9.9.9", BaseURL: busyDuring.URL})
	if !res.Busy || res.Restarting {
		t.Fatalf("a host that became busy has to refuse and say so: %+v", res)
	}
	if got, _ := os.ReadFile(exe); string(got) != "the old one" {
		t.Fatalf("the binary was replaced under a busy host: %q", got)
	}
}

// While the binary is being fetched the host takes no sandbox: the process is
// about to be replaced, and a sandbox accepted now would be watched by nobody
// in a minute. The pool leaves such a host out of the candidates for the same
// reason (#161).
func TestAnUpdatingHostRefusesStartsAndIsNoCandidate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	orgID, runnerID := uuid.New(), uuid.New()
	node := NewNode(runnerID, orgID, &Docker{
		RunnerID: runnerID, Image: "covey-sandbox:test", DataDir: dir, DockerBin: fakeDockerBin(t, dir, "nothing"),
	}, quietLog())
	t.Cleanup(node.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	control, _ := startNodeOn(t, ctx, node)

	node.setUpdating(true)
	start, _ := encode(TypeStartSandbox, "1", StartSandbox{AgentID: uuid.New(), OrgID: orgID})
	if err := control.Send(ctx, start); err != nil {
		t.Fatal(err)
	}
	res, _ := decode[SandboxResult](warteAufTyp(t, control, TypeSandboxFailed))
	if !strings.Contains(res.Err, "installing an update") {
		t.Fatalf("the refusal has to name the update: %q", res.Err)
	}

	p := NewPool(quietLog())
	_, id, _ := registriereFalschenRunner(t, p, orgID)
	c := p.connFor(orgID, id)
	c.setUpdating(true)
	if _, err := p.candidates(need{orgID: orgID}); err == nil || !strings.Contains(err.Error(), "installing an update") {
		t.Fatalf("an updating host is no candidate, and the message says why: %v", err)
	}
}

// The host reports "v0.7.2 (abc1234, 2026-…, go1.26)" and the plan says
// "v0.7.2": the same version, compared on the tag — and a dirty build is never
// the release, whatever its tag says (#161).
func TestAPlanIsFulfilledByTheTagNotTheWholeBuildString(t *testing.T) {
	p := NewPool(quietLog())
	p.PlannedUpdate = func(context.Context, uuid.UUID) (string, error) { return "v9.9.9", nil }
	done := 0
	p.PlannedUpdateDone = func(context.Context, uuid.UUID, string) { done++ }

	clean := &conn{pool: p, runnerID: uuid.New(), orgID: uuid.New(), version: "v9.9.9 (abc1234, 2026-09-02T00:00:00Z, go1.26)"}
	clean.runPlannedUpdate(context.Background())
	if done != 1 {
		t.Fatalf("a clean build on the planned tag fulfils the plan (%d)", done)
	}
	dirty := &conn{pool: p, runnerID: uuid.New(), orgID: uuid.New(), version: "v9.9.9 (abc1234-dirty, 2026-09-02T00:00:00Z, go1.26)"}
	// No connection to update over: the attempt fails, and that is the point —
	// it was attempted rather than declared done.
	dirty.runPlannedUpdate(context.Background())
	if done != 1 {
		t.Fatalf("a dirty build must not count as the release (%d)", done)
	}
}
