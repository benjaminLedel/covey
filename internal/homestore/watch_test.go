package homestore

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

// A sync that has nothing to upload is still work: every block is read and
// hashed. Exactly this case — a grown home that changed hardly at all —
// took a quarter of an hour on a production instance and said nothing of
// itself while it ran. The heartbeat may therefore not hang on something
// going over the wire.
func TestDasLebenszeichenHaengtNichtAmHochladen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	blobs, err := NewDir(filepath.Join(dir, "blocks"))
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(home, name), []byte("Inhalt "+name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	org := uuid.New()
	if _, err := Sync(ctx, blobs, org, home, Excludes{}); err != nil {
		t.Fatal(err)
	}

	// Second run: everything is already in the store, not a byte goes up.
	var meldungen int
	res, err := SyncWatched(ctx, blobs, org, home, Excludes{}, func(gesehen int, bytesUp int64) {
		meldungen++
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.BytesUp != 0 {
		t.Fatalf("%d Bytes gingen hoch — der Fall ist damit nicht der gemeinte", res.BytesUp)
	}
	if meldungen == 0 {
		t.Fatal("kein Lebenszeichen, obwohl der Sync jede Datei gelesen hat")
	}
}
