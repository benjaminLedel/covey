package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/homestore"
)

/* Ein Home wächst nur, und nichts hat einen Agenten je gebeten, seinen eigenen
   Schreibtisch aufzuräumen. Gemessen: 19,1 GB, davon 18,8 GB, die kein anderer
   teilt — zwei selbst installierte JDKs, ein von Hand entpackter Datenbank-
   server, Kratzverzeichnisse aus Tickets vom August. Der Preis steht in den
   Phasen: 34 s Prüfen bei jedem Weckruf, 140 s Zurückschreiben nach jedem Lauf
   (#103).

   Gebeten wird der Agent, nicht gekehrt wird für ihn: `239-fix-backup` ist eine
   Kopie aus einem Ticket, und das weiß nur er. */

func TestEinGewachsenesHomeBekommtEineAufraeumAufgabe(t *testing.T) {
	ctx := context.Background()
	s := newStackWith(t, stackOpts{})
	agent := s.newSupportAgent("gewachsen")

	// Ein Schnappschuss über der Schwelle (5 GB), wie ihn ein Sync schreibt.
	if _, err := s.pool.Exec(ctx, `INSERT INTO home_snapshots
		(id, org_id, agent_id, manifest_hash, total_size, blocks_up, bytes_up, duration_ms, reason)
		VALUES ($1,$2,$3,'abc',$4,293,425796824,139964,'job')`,
		uuid.New(), s.orgID, agent.ID, int64(19)<<30); err != nil {
		t.Fatal(err)
	}

	s.orch.AskForTidying(ctx)

	aufgaben, err := s.backlog.ListByAgent(ctx, agent.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	var gefunden bool
	for _, a := range aufgaben {
		if !strings.Contains(a.Title, "aufräumen") {
			continue
		}
		gefunden = true
		// Die Zahlen gehören hinein: ohne sie räumt er auf, was leicht zu
		// finden ist, statt was groß ist.
		if !strings.Contains(a.Body, "19.0 GB") {
			t.Fatalf("die Größe fehlt im Auftrag:\n%s", a.Body)
		}
		// Und die Grenze: ein Home ist ein Gedächtnis, kein Zwischenspeicher.
		if !strings.Contains(a.Body, "Gedächtnis") {
			t.Fatalf("der Auftrag nennt die Grenze nicht:\n%s", a.Body)
		}
	}
	if !gefunden {
		t.Fatalf("keine Aufräum-Aufgabe angelegt (%d Aufgaben)", len(aufgaben))
	}

	// Zweimal fragen heißt nicht zwei Aufgaben: solange eine offen steht,
	// kommt keine weitere.
	s.orch.AskForTidying(ctx)
	aufgaben, _ = s.backlog.ListByAgent(ctx, agent.ID, false)
	var n int
	for _, a := range aufgaben {
		if strings.Contains(a.Title, "aufräumen") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d Aufräum-Aufgaben — die Entdopplung greift nicht", n)
	}
}

// A home can be a heap without being large: 806 entries directly in one
// developer home, and the size never pointed at them (#272). The count comes
// from the manifest the store already holds.
func TestAScatteredHomeIsAskedEvenWhenSmall(t *testing.T) {
	ctx := context.Background()
	s := newStackWith(t, stackOpts{})
	blobs, err := homestore.NewDir(filepath.Join(t.TempDir(), "blocks"))
	if err != nil {
		t.Fatal(err)
	}
	s.orch.Blobs = blobs

	snapshot := func(agentID uuid.UUID, fill func(home string)) {
		t.Helper()
		home := t.TempDir()
		fill(home)
		res, err := homestore.Sync(ctx, blobs, s.orgID, home, homestore.Excludes{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.pool.Exec(ctx, `INSERT INTO home_snapshots
			(id, org_id, agent_id, manifest_hash, total_size, blocks_up, bytes_up, duration_ms, reason)
			VALUES ($1,$2,$3,$4,$5,1,1,900,'job')`,
			uuid.New(), s.orgID, agentID, res.ManifestHash, res.TotalSize); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(path), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	heap := s.newSupportAgent("haufen")
	snapshot(heap.ID, func(home string) {
		for i := 0; i < 220; i++ {
			write(filepath.Join(home, fmt.Sprintf("shot-%d.png", i)))
		}
		write(filepath.Join(home, "repos", "p40", "README.md"))
	})
	// The counter-check: the same number of files one level down is a
	// checkout, not a heap.
	tidy := s.newSupportAgent("ordentlich")
	snapshot(tidy.ID, func(home string) {
		for i := 0; i < 220; i++ {
			write(filepath.Join(home, "repos", "p40", fmt.Sprintf("shot-%d.png", i)))
		}
	})

	s.orch.AskForTidying(ctx)

	body := ""
	tasks, _ := s.backlog.ListByAgent(ctx, heap.ID, false)
	for _, a := range tasks {
		if strings.Contains(a.Title, "aufräumen") {
			body = a.Body
		}
	}
	if body == "" {
		t.Fatalf("a home with 221 entries in its root was not asked (%d tasks)", len(tasks))
	}
	for _, want := range []string{"Einträge direkt in ~: 221", "`shot*`: 220", "~/scratch/"} {
		if !strings.Contains(body, want) {
			t.Fatalf("%q missing from the assignment:\n%s", want, body)
		}
	}

	tasks, _ = s.backlog.ListByAgent(ctx, tidy.ID, false)
	for _, a := range tasks {
		if strings.Contains(a.Title, "aufräumen") {
			t.Fatalf("a home with one entry in its root was asked:\n%s", a.Body)
		}
	}
}

// Die Gegenprobe: ein kleines Home wird in Ruhe gelassen. Eine Schwelle, die
// alle trifft, wird zur Gewohnheit und dann ignoriert.
func TestEinKleinesHomeWirdInRuheGelassen(t *testing.T) {
	ctx := context.Background()
	s := newStackWith(t, stackOpts{})
	agent := s.newSupportAgent("schlank")
	if _, err := s.pool.Exec(ctx, `INSERT INTO home_snapshots
		(id, org_id, agent_id, manifest_hash, total_size, blocks_up, bytes_up, duration_ms, reason)
		VALUES ($1,$2,$3,'abc',$4,5,1000,900,'job')`,
		uuid.New(), s.orgID, agent.ID, int64(400)<<20); err != nil {
		t.Fatal(err)
	}

	s.orch.AskForTidying(ctx)
	time.Sleep(100 * time.Millisecond)

	aufgaben, _ := s.backlog.ListByAgent(ctx, agent.ID, false)
	for _, a := range aufgaben {
		if strings.Contains(a.Title, "aufräumen") {
			t.Fatalf("ein 400-MB-Home wurde zum Aufräumen gebeten")
		}
	}
}
