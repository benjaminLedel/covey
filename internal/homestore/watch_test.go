package homestore

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

// Ein Sync, der nichts hochzuladen hat, ist trotzdem Arbeit: jeder Block wird
// gelesen und gehasht. Genau dieser Fall — ein gewachsenes Home, das sich kaum
// geändert hat — hat auf einer produktiven Instanz eine Viertelstunde gedauert
// und dabei nichts von sich gesagt. Das Lebenszeichen darf also nicht daran
// hängen, dass etwas über die Leitung geht.
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

	// Zweiter Durchlauf: alles liegt schon im Store, es geht kein Byte hoch.
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
