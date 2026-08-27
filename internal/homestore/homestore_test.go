package homestore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func newDir(t *testing.T) *Dir {
	t.Helper()
	d, err := NewDir(filepath.Join(t.TempDir(), "blocks"))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// write builds a small home to sync.
func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The promise of the construction: a home goes in as a whole and comes out as a
// whole, and nobody has to have written down what in it was worth keeping. The
// 48 MB that exist nowhere else lie scattered across an agent's home — every
// list of what to save is a rule that can be wrong, and its error costs work
// that has already been paid for.
func TestHomeSurvivesLosingItsWorkingCopy(t *testing.T) {
	ctx := context.Background()
	blobs := newDir(t)
	org := uuid.New()
	home := t.TempDir()

	write(t, home, "repos/projekt/README.md", "# Projekt")
	write(t, home, "eigenes-werkzeug.ts", "export const x = 1")
	write(t, home, ".claude/transkript.jsonl", strings.Repeat("{\"e\":1}\n", 100))
	if err := os.MkdirAll(filepath.Join(home, "leer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("repos/projekt", filepath.Join(home, "aktuell")); err != nil {
		t.Fatal(err)
	}

	res, err := Sync(ctx, blobs, org, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.ManifestHash == "" || res.Blocks == 0 {
		t.Fatalf("the sync produced nothing: %+v", res)
	}

	// The working copy is gone — a lost runner, a cleared disk.
	if err := os.RemoveAll(home); err != nil {
		t.Fatal(err)
	}

	m, err := Load(ctx, blobs, org, res.ManifestHash)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Materialize(ctx, blobs, org, home, m); err != nil {
		t.Fatal(err)
	}

	for path, want := range map[string]string{
		"repos/projekt/README.md":  "# Projekt",
		"eigenes-werkzeug.ts":      "export const x = 1",
		".claude/transkript.jsonl": strings.Repeat("{\"e\":1}\n", 100),
	} {
		got, err := os.ReadFile(filepath.Join(home, path))
		if err != nil {
			t.Errorf("%s is missing after the restore: %v", path, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s came back changed", path)
		}
	}
	if info, err := os.Stat(filepath.Join(home, "leer")); err != nil || !info.IsDir() {
		t.Errorf("an empty directory belongs to the home too: %v", err)
	}
	// A symlink travels as what it is. Following it would copy into the
	// snapshot what the link describes in forty bytes.
	if target, err := os.Readlink(filepath.Join(home, "aktuell")); err != nil || target != "repos/projekt" {
		t.Errorf("the symlink was not restored: %q, %v", target, err)
	}
}

// The figure that decides whether this is affordable: what actually travels on
// the second sync. A typical run changes megabytes in a 7 GB home — if the
// delta were not real, the sleep path would cost minutes every time.
func TestOnlyNewContentTravels(t *testing.T) {
	ctx := context.Background()
	blobs := newDir(t)
	org := uuid.New()
	home := t.TempDir()

	write(t, home, "cache/gross.bin", strings.Repeat("x", 200_000))
	write(t, home, "notiz.md", "erste Fassung")
	first, err := Sync(ctx, blobs, org, home, nil)
	if err != nil {
		t.Fatal(err)
	}

	write(t, home, "notiz.md", "zweite Fassung")
	second, err := Sync(ctx, blobs, org, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.ManifestHash == first.ManifestHash {
		t.Fatal("a changed home has to produce a new snapshot")
	}
	// Only the changed file and the new manifest — the 200 kB cache stays put.
	if second.BytesUp > 5_000 {
		t.Errorf("the delta travelled too much: %d bytes", second.BytesUp)
	}

	// An unchanged home produces the identical snapshot: the manifest is
	// content-addressed too, so nothing at all travels.
	third, err := Sync(ctx, blobs, org, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if third.ManifestHash != second.ManifestHash {
		t.Error("an unchanged home has to produce the same snapshot")
	}
	if third.Blocks != 0 {
		t.Errorf("nothing may travel for an unchanged home, got %d blocks", third.Blocks)
	}
}

// Deduplication across agents is what makes the whole thing pay: the 4 GB of
// toolchain caches are byte-for-byte the same on every developer home, and
// they are supposed to lie centrally once instead of once per agent.
func TestIdenticalContentIsStoredOnce(t *testing.T) {
	ctx := context.Background()
	blobs := newDir(t)
	org := uuid.New()

	homeA, homeB := t.TempDir(), t.TempDir()
	toolchain := strings.Repeat("SDK", 10_000) // 30 kB, identical on both
	write(t, homeA, ".pub-cache/paket.tar", toolchain)
	write(t, homeB, ".pub-cache/paket.tar", toolchain)
	// Something of its own, so the second home is not simply the same snapshot.
	write(t, homeB, "arbeit/eigenes.md", "nur bei B")

	if _, err := Sync(ctx, blobs, org, homeA, nil); err != nil {
		t.Fatal(err)
	}
	second, err := Sync(ctx, blobs, org, homeB, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Two new blocks: the file only B has, and B's manifest. The 30 kB of
	// toolchain are already there and do not travel a second time.
	if second.Blocks != 2 {
		t.Errorf("only what is new may travel: %d new blocks", second.Blocks)
	}
	if second.BytesUp > 2_000 {
		t.Errorf("the shared toolchain was stored twice: %d bytes", second.BytesUp)
	}
}

// Restoring only writes what differs. On the runner an agent last ran on that
// is the normal case — and if it were not so, every wake would rewrite
// gigabytes for nothing.
func TestMaterializeTouchesOnlyWhatDiffers(t *testing.T) {
	ctx := context.Background()
	blobs := newDir(t)
	org := uuid.New()
	home := t.TempDir()

	write(t, home, "a.txt", "eins")
	write(t, home, "b.txt", "zwei")
	res, _ := Sync(ctx, blobs, org, home, nil)
	m, err := Load(ctx, blobs, org, res.ManifestHash)
	if err != nil {
		t.Fatal(err)
	}

	// Nothing changed: everything is kept.
	got, err := Materialize(ctx, blobs, org, home, m)
	if err != nil {
		t.Fatal(err)
	}
	if got.Written != 0 || got.Kept != 2 {
		t.Errorf("an unchanged working copy must not be rewritten: %+v", got)
	}

	// One file changed, one added that the snapshot does not know.
	write(t, home, "a.txt", "geaendert")
	write(t, home, "uebrig.txt", "aus einem anderen Zustand")
	got, err = Materialize(ctx, blobs, org, home, m)
	if err != nil {
		t.Fatal(err)
	}
	if got.Written != 1 {
		t.Errorf("exactly the changed file has to be written: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(home, "uebrig.txt")); !os.IsNotExist(err) {
		t.Error("what the snapshot does not describe must not stay behind")
	}
}

// Exclusions are a cost question, not a matter of correctness: the default is
// empty, and what is named is left out.
func TestExclusionsLeaveThePathOut(t *testing.T) {
	ctx := context.Background()
	blobs := newDir(t)
	org := uuid.New()
	home := t.TempDir()

	write(t, home, ".dartServer/analyse.bin", strings.Repeat("a", 1000))
	write(t, home, "arbeit/ergebnis.md", "wichtig")

	res, err := Sync(ctx, blobs, org, home, Excludes{".dartServer"})
	if err != nil {
		t.Fatal(err)
	}
	m, err := Load(ctx, blobs, org, res.ManifestHash)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range m.Entries {
		if strings.HasPrefix(e.Path, ".dartServer") {
			t.Errorf("the excluded path is in the snapshot: %s", e.Path)
		}
	}
	var found bool
	for _, e := range m.Entries {
		if e.Path == "arbeit/ergebnis.md" {
			found = true
		}
	}
	if !found {
		t.Error("everything not excluded has to be in there")
	}
}

// The manifest comes back over the protocol from a runner, and a runner is
// trusted infrastructure — but a path that leaves the home would write into
// the control plane's own directories, and that must fail rather than be
// noticed later.
func TestManifestCannotEscapeTheHome(t *testing.T) {
	ctx := context.Background()
	blobs := newDir(t)
	org := uuid.New()
	home := t.TempDir()

	böse := Manifest{Entries: []Entry{{
		Path: "../ausgebrochen.txt", Mode: 0o644, Size: 3, Blocks: []string{Hash([]byte("abc"))},
	}}}
	if err := blobs.Put(ctx, org, Hash([]byte("abc")), bytes.NewReader([]byte("abc"))); err != nil {
		t.Fatal(err)
	}
	if _, err := Materialize(ctx, blobs, org, home, böse); err == nil {
		t.Fatal("a path outside the home has to be refused")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(home), "ausgebrochen.txt")); !os.IsNotExist(err) {
		t.Error("something was written outside the home")
	}
}

// The block namespace is keyed by organisation, and that is a security
// statement, not bookkeeping: a shared one would be an existence oracle over
// hashes — whoever may ask whether a block is there learns whether somebody
// else holds exactly that content.
func TestBlocksOfOneOrganisationAreInvisibleToAnother(t *testing.T) {
	ctx := context.Background()
	blobs := newDir(t)
	a, b := uuid.New(), uuid.New()
	hash := Hash([]byte("geheim"))

	if err := blobs.Put(ctx, a, hash, bytes.NewReader([]byte("geheim"))); err != nil {
		t.Fatal(err)
	}
	has, err := blobs.Has(ctx, b, hash)
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Fatal("a foreign organisation must not see the block")
	}
	if _, err := blobs.Get(ctx, b, hash); err == nil {
		t.Fatal("a foreign organisation must not read the block")
	}
}

// A hash becomes a path, and one that is not a hash would write outside the
// store. Checked at the one place that makes the conversion.
func TestInvalidBlockHashIsRefused(t *testing.T) {
	ctx := context.Background()
	blobs := newDir(t)
	org := uuid.New()
	for _, bad := range []string{"../../etc/passwd", "..", "ZZZZ", ""} {
		if err := blobs.Put(ctx, org, bad, bytes.NewReader([]byte("x"))); err == nil {
			t.Errorf("hash %q had to be refused", bad)
		}
	}
}

// Retention is where deduplication shows its price: a block belongs to no
// single snapshot, so removing one frees only what nothing else references.
// A cleanup that got this wrong would take away another snapshot's content
// while reporting success.
func TestSweepKeepsWhatAnotherSnapshotStillNeeds(t *testing.T) {
	ctx := context.Background()
	blobs := newDir(t)
	org := uuid.New()
	home := t.TempDir()

	write(t, home, "gemeinsam.txt", "beide Snapshots brauchen das")
	write(t, home, "nur-alt.txt", "nur im ersten")
	alt, err := Sync(ctx, blobs, org, home, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(filepath.Join(home, "nur-alt.txt")); err != nil {
		t.Fatal(err)
	}
	write(t, home, "nur-neu.txt", "nur im zweiten")
	neu, err := Sync(ctx, blobs, org, home, nil)
	if err != nil {
		t.Fatal(err)
	}

	// The old snapshot goes; the new one stays.
	res, err := Sweep(ctx, blobs, org, []string{neu.ManifestHash})
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed == 0 {
		t.Error("the cleanup freed nothing at all")
	}

	// The decisive part: the shared content survived, and the new snapshot can
	// still be restored in full.
	m, err := Load(ctx, blobs, org, neu.ManifestHash)
	if err != nil {
		t.Fatalf("the surviving snapshot is unreadable: %v", err)
	}
	restore := t.TempDir()
	if _, err := Materialize(ctx, blobs, org, restore, m); err != nil {
		t.Fatalf("the surviving snapshot is incomplete: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(restore, "gemeinsam.txt"))
	if err != nil || string(got) != "beide Snapshots brauchen das" {
		t.Errorf("the shared block was swept away: %v", err)
	}

	// And the old one is really gone.
	if _, err := Load(ctx, blobs, org, alt.ManifestHash); err == nil {
		t.Error("the removed snapshot is still readable")
	}
}

// Jede Datei eines Homes wird gechunkt — das Manifest selbst ging bisher als
// EIN Objekt weg, egal wie groß. Bei einem gewachsenen Home sind das
// hunderttausende Einträge mit Pfad und 64-Zeichen-Hash, also zweistellige
// Megabytes in einem PUT. Über einen entfernten Runner ist das eine
// HTTP-Anfrage, und sie starb an der Größenbegrenzung dessen, was vor der
// Steuerebene steht: das Home war dauerhaft nicht sicherbar, während jedes
// kleine Home funktionierte und die Installation gesund aussehen ließ.
func TestEinGrossesManifestReistInStuecken(t *testing.T) {
	ctx := context.Background()
	org := uuid.New()
	home := t.TempDir()

	// Viele kleine Dateien: der Inhalt ist winzig, das Manifest wird groß —
	// genau die Form, die ein Entwickler-Home hat (node_modules, Caches).
	for i := 0; i < 20000; i++ {
		write(t, home, fmt.Sprintf("caches/paket-%05d/ein-recht-langer-dateiname.js", i), "x")
	}

	limit := &limitedBlobs{Dir: newDir(t), max: chunkSize}
	res, err := Sync(ctx, limit, org, home, nil)
	if err != nil {
		t.Fatalf("Sync scheitert an der Größe des Manifests: %v", err)
	}
	if limit.largest > chunkSize {
		t.Errorf("ein Objekt von %d Bytes ging weg — mehr als ein Block tragen darf", limit.largest)
	}

	// Und es kommt vollständig zurück.
	m, err := Load(ctx, limit, org, res.ManifestHash)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(m.Entries) < 20000 {
		t.Errorf("das Manifest kam unvollständig zurück: %d Einträge", len(m.Entries))
	}
}

// Ein Schnappschuss aus der Zeit vor der Stückelung liegt als ganzes Manifest
// im Store. Er muss weiter laden — sonst kostet die Änderung genau das, was sie
// verhindern soll.
func TestEinAltesManifestLaedtWeiterhin(t *testing.T) {
	ctx := context.Background()
	blobs := newDir(t)
	org := uuid.New()
	home := t.TempDir()
	write(t, home, "klein.txt", "inhalt")

	res, err := Sync(ctx, blobs, org, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Klein genug: es liegt unverändert als Manifest da, kein Index davor.
	raw, err := fetch(ctx, blobs, org, res.ManifestHash)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "covey_manifest_chunks") {
		t.Error("ein kleines Manifest soll unverändert liegen, damit jeder Leser es versteht")
	}
	if _, err := Load(ctx, blobs, org, res.ManifestHash); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

// limitedBlobs ist der Store mit der Grenze, die in der Wirklichkeit vor ihm
// steht: ein Proxy bzw. die Steuerebene nehmen keine beliebig großen Objekte an.
type limitedBlobs struct {
	*Dir
	max     int
	largest int
}

func (l *limitedBlobs) Put(ctx context.Context, orgID uuid.UUID, hash string, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if len(data) > l.largest {
		l.largest = len(data)
	}
	if len(data) > l.max {
		return fmt.Errorf("413 Request Entity Too Large (%d Bytes)", len(data))
	}
	return l.Dir.Put(ctx, orgID, hash, bytes.NewReader(data))
}

// Ein zweiter Weckruf auf demselben Host darf nichts kosten — das ist die
// Zusage, auf der die Runner-Affinität steht. Sie galt nur für kleine Dateien:
// alles über wholeFileLimit galt als verändert und wurde JEDES Mal neu geholt.
// Auf einer Produktivinstanz waren das 8,3 GB und elf Minuten pro Weckruf,
// bevor der Agent seinen ersten Turn machte.
func TestEineGrosseDateiWirdNichtBeiJedemWeckrufNeuGeholt(t *testing.T) {
	ctx := context.Background()
	blobs := newDir(t)
	org := uuid.New()
	home := t.TempDir()

	// Eine Datei über wholeFileLimit (also gechunkt) und eine knapp darunter.
	write(t, home, "sdk/flutter.tar", strings.Repeat("SDK", 4*1024*1024))
	write(t, home, "mittel.bin", strings.Repeat("m", 6*1024*1024))
	write(t, home, "klein.txt", "kurz")

	res, err := Sync(ctx, blobs, org, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := Load(ctx, blobs, org, res.ManifestHash)
	if err != nil {
		t.Fatal(err)
	}

	// Dieselbe Arbeitskopie noch einmal auf denselben Stand bringen: es ist
	// nichts zu tun, und es darf nichts über die Leitung gehen.
	second, err := Materialize(ctx, blobs, org, home, m)
	if err != nil {
		t.Fatal(err)
	}
	if second.BytesIn != 0 {
		t.Errorf("%d Bytes wurden erneut geholt, obwohl sich nichts geändert hat", second.BytesIn)
	}
	if second.Written != 0 {
		t.Errorf("%d Dateien wurden erneut geschrieben", second.Written)
	}
	if second.Kept != 3 {
		t.Errorf("erwartet 3 unveränderte Dateien, gezählt %d", second.Kept)
	}
}

// Und die Gegenrichtung: eine große Datei, die sich WIRKLICH geändert hat, muss
// erkannt werden — sonst wäre die Ersparnis mit falschen Daten bezahlt.
func TestEineVeraenderteGrosseDateiWirdErkannt(t *testing.T) {
	ctx := context.Background()
	blobs := newDir(t)
	org := uuid.New()
	home := t.TempDir()
	write(t, home, "gross.bin", strings.Repeat("a", 10*1024*1024))

	res, err := Sync(ctx, blobs, org, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := Load(ctx, blobs, org, res.ManifestHash)
	if err != nil {
		t.Fatal(err)
	}

	// Gleiche Länge, anderer Inhalt — die Größe allein verrät es nicht.
	write(t, home, "gross.bin", strings.Repeat("a", 5*1024*1024)+strings.Repeat("b", 5*1024*1024))
	back, err := Materialize(ctx, blobs, org, home, m)
	if err != nil {
		t.Fatal(err)
	}
	if back.Written != 1 {
		t.Errorf("die veränderte Datei wurde nicht zurückgeholt (Written %d)", back.Written)
	}
	raw, err := os.ReadFile(filepath.Join(home, "gross.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "b") {
		t.Error("der Stand des Schnappschusses hat sich nicht durchgesetzt")
	}
}

// Ein gewachsenes Transkript ist der Fall, für den feste Blöcke überhaupt
// gewählt wurden: ein Anhängen lässt jeden vorherigen Chunk byteweise gleich,
// an derselben Stelle. Eingelöst wurde das nicht — writeFile holte jeden Block
// aus dem Store, auch die, die lokal schon danebenlagen.
func TestBeimAnhaengenReistNurDasNeueStueck(t *testing.T) {
	ctx := context.Background()
	blobs := newDir(t)
	org := uuid.New()
	home := t.TempDir()

	// 12 MiB: gechunkt in 3 Stücke.
	write(t, home, ".claude/transkript.jsonl", strings.Repeat("z", 12*1024*1024))
	if _, err := Sync(ctx, blobs, org, home, nil); err != nil {
		t.Fatal(err)
	}

	// Angehängt: die ersten drei Chunks bleiben, ein vierter kommt dazu.
	write(t, home, ".claude/transkript.jsonl", strings.Repeat("z", 12*1024*1024)+strings.Repeat("neu", 100))
	res, err := Sync(ctx, blobs, org, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := Load(ctx, blobs, org, res.ManifestHash)
	if err != nil {
		t.Fatal(err)
	}

	// Die Arbeitskopie steht auf dem ALTEN Stand — wie auf einem Runner, der
	// den Agenten zuletzt vor dem Anhängen getragen hat.
	write(t, home, ".claude/transkript.jsonl", strings.Repeat("z", 12*1024*1024))
	back, err := Materialize(ctx, blobs, org, home, m)
	if err != nil {
		t.Fatal(err)
	}
	// Nur das letzte, kurze Stück darf über die Leitung — nicht die 12 MiB davor.
	if back.BytesIn > chunkSize {
		t.Errorf("%d Bytes geholt, obwohl nur ein Stück angehängt wurde", back.BytesIn)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".claude/transkript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 12*1024*1024+300 || !strings.HasSuffix(string(raw), "neu") {
		t.Errorf("die Datei ist nicht der Stand des Schnappschusses (%d Bytes)", len(raw))
	}
}
