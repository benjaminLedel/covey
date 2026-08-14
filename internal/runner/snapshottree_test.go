package runner

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"covey/internal/homestore"
	"covey/internal/sandboxfs"
)

// Reading a home out of its last snapshot is the path nobody takes until the
// moment they need it: the runner is down and somebody wants to see what the
// agent produced. A fault in here is therefore silent right up to the point
// where it costs the most, which is why it is worth its own tests rather than
// its share of an integration run.

// snapshotOf builds a small home, syncs it and returns the tree over it.
func snapshotOf(t *testing.T, files map[string]string) (sandboxfs.Tree, homestore.BlobStore, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	blobs, err := homestore.NewDir(filepath.Join(t.TempDir(), "blocks"))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	org := uuid.New()
	res, err := homestore.Sync(ctx, blobs, org, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := homestore.Load(ctx, blobs, org, res.ManifestHash)
	if err != nil {
		t.Fatal(err)
	}
	return newSnapshotTree(blobs, org, m), blobs, org
}

func TestSnapshotTreeReadsWhatTheRunnerNoLongerCan(t *testing.T) {
	// A file large enough to be several blocks: assembling it out of them is
	// where a download from a snapshot differs from one off a disk.
	big := strings.Repeat("Zeile mit Inhalt\n", 700_000) // ~11 MB, chunked
	tree, _, _ := snapshotOf(t, map[string]string{
		"notizen/plan.md":    "# Plan",
		"notizen/alt.md":     "verworfen",
		"protokoll/lauf.log": big,
	})

	listing, err := tree.List("notizen")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listing.Entries) != 2 {
		t.Fatalf("the listing is wrong: %+v", listing.Entries)
	}
	// The state is named in the listing itself and not only when a write
	// fails: whoever is about to upload should learn it beforehand.
	if !listing.ReadOnly || listing.ReadOnlyReason == "" {
		t.Error("a snapshot listing has to say that it is read-only, and why")
	}

	file, err := tree.Read("notizen/plan.md")
	if err != nil || !strings.Contains(file.Content, "# Plan") {
		t.Fatalf("Read: %q, %v", file.Content, err)
	}

	// The download: assembled out of its blocks, one at a time — an 11 MB file
	// must not have to fit into memory twice to be readable.
	rc, info, err := tree.Open("protokoll/lauf.log")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()
	if info.Size != int64(len(big)) || info.Name != "lauf.log" {
		t.Errorf("the file's particulars are wrong: %+v", info)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if string(got) != big {
		t.Errorf("the file came back changed (%d of %d bytes)", len(got), len(big))
	}

	// A missing path stays a 404 across this route too.
	if _, err := tree.Read("gibtesnicht.md"); err == nil {
		t.Error("a missing path has to fail")
	}
	if _, _, err := tree.Open("notizen"); err == nil {
		t.Error("opening a directory has to fail")
	}
}

func TestSnapshotTreeZipsAndMeasures(t *testing.T) {
	tree, _, _ := snapshotOf(t, map[string]string{
		"arbeit/a.md":      "eins",
		"arbeit/tief/b.md": "zwei",
		"draussen.md":      "nicht im Archiv",
	})

	// Measured before the first byte is out: "too large" has to be a status,
	// not an archive that breaks off mid-download.
	plan, err := tree.PlanZip([]string{"arbeit"})
	if err != nil {
		t.Fatalf("PlanZip: %v", err)
	}
	if plan.Files != 2 || plan.Bytes != int64(len("eins")+len("zwei")) {
		t.Errorf("the plan is wrong: %+v", plan)
	}
	if plan.Name == "" {
		t.Error("the archive needs a suggested name")
	}

	var buf bytes.Buffer
	if err := tree.WriteZip(&buf, plan); err != nil {
		t.Fatalf("WriteZip: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("the archive is not readable: %v", err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if len(zr.File) != 2 || !names["arbeit/a.md"] || !names["arbeit/tief/b.md"] {
		t.Errorf("the archive holds the wrong files: %v", names)
	}
	// What was not selected stays out.
	if names["draussen.md"] {
		t.Error("an unselected file must not be in the archive")
	}

	if _, err := tree.PlanZip([]string{"gibtesnicht"}); err == nil {
		t.Error("planning over a missing path has to fail")
	}
}

// Usage over a snapshot answers what it can and stays quiet about what it
// cannot: free space is a property of a disk, and a snapshot lies on none.
// Reporting the control plane's own disk there would be a figure about the
// wrong machine.
func TestSnapshotTreeUsageAnswersOnlyWhatItKnows(t *testing.T) {
	tree, _, _ := snapshotOf(t, map[string]string{
		"repos/projekt-a/datei.txt": strings.Repeat("a", 3000),
		"repos/projekt-b/datei.txt": strings.Repeat("b", 1000),
		"sonstwo.txt":               "x",
	})
	usage := tree.Usage()
	if !usage.Exists {
		t.Fatal("the home exists in the snapshot")
	}
	if usage.TotalBytes != 0 || usage.FreeBytes != 0 {
		t.Error("a snapshot lies on no disk and must not report one")
	}
	if len(usage.Checkouts) != 2 || usage.Checkouts[0].Name != "projekt-a" {
		t.Fatalf("the checkouts are wrong or unsorted: %+v", usage.Checkouts)
	}
	if usage.CheckoutBytes != 4000 {
		t.Errorf("checkout bytes: %d", usage.CheckoutBytes)
	}
}

// Every writing operation is refused, with a reason. A change to a snapshot
// would be a second state beside the working copy that is coming back, and
// nothing could then say which of the two is the home.
func TestSnapshotTreeRefusesEveryWrite(t *testing.T) {
	tree, _, _ := snapshotOf(t, map[string]string{"da.md": "inhalt"})

	var readOnly *sandboxfs.ReadOnlyError
	check := func(what string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s had to be refused", what)
		}
		if !errorsAs(err, &readOnly) {
			t.Errorf("%s: %v — expected a ReadOnlyError", what, err)
		}
	}
	_, err := tree.Write("neu.md", strings.NewReader("x"))
	check("Write", err)
	_, err = tree.Mkdir("ordner")
	check("Mkdir", err)
	check("Remove", tree.Remove("da.md"))
	_, err = tree.Move("da.md", "anders.md")
	check("Move", err)
	if !strings.Contains(readOnly.Reason, "runner") {
		t.Errorf("the refusal has to say why: %q", readOnly.Reason)
	}
}

func errorsAs(err error, target **sandboxfs.ReadOnlyError) bool {
	for err != nil {
		if e, ok := err.(*sandboxfs.ReadOnlyError); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
