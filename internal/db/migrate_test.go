package db

import (
	"strings"
	"testing"
	"testing/fstest"

	"covey/migrations"
)

// TestLoadMigrationsRejectsDuplicateVersion: two branches hand out the same
// number in parallel. Until now loadMigrations ate that silently — both files
// on the same migration struct, the later one overwriting the earlier's SQL,
// one version booked — and the lost migration never ran. That must not load.
func TestLoadMigrationsRejectsDuplicateVersion(t *testing.T) {
	fsys := fstest.MapFS{
		"0051_a.up.sql":                 {Data: []byte("SELECT 1")},
		"0052_agent_effort.up.sql":      {Data: []byte("ALTER TABLE agents ADD COLUMN effort TEXT")},
		"0052_improvement_items.up.sql": {Data: []byte("CREATE TABLE improvement_items ()")},
	}
	_, err := loadMigrations(fsys)
	if err == nil {
		t.Fatal("zwei Migrationen mit Version 52 wurden klaglos geladen")
	}
	for _, want := range []string{"0052_agent_effort", "0052_improvement_items", "52"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Fehlermeldung nennt %q nicht: %v", want, err)
		}
	}
}

// TestLoadMigrationsPairsAndSorts keeps the normal case: up/down of the same
// name pair up, and the order is that of the numbers, not of the directory.
func TestLoadMigrationsPairsAndSorts(t *testing.T) {
	fsys := fstest.MapFS{
		"0002_b.up.sql":   {Data: []byte("up 2")},
		"0002_b.down.sql": {Data: []byte("down 2")},
		"0001_a.up.sql":   {Data: []byte("up 1")},
	}
	ms, err := loadMigrations(fsys)
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if len(ms) != 2 {
		t.Fatalf("erwartet 2 Migrationen, bekommen %d", len(ms))
	}
	if ms[0].version != 1 || ms[1].version != 2 {
		t.Fatalf("nicht nach Version sortiert: %d, %d", ms[0].version, ms[1].version)
	}
	if ms[1].upSQL != "up 2" || ms[1].downSQL != "down 2" {
		t.Errorf("up/down nicht gepaart: %q / %q", ms[1].upSQL, ms[1].downSQL)
	}
}

// TestEmbeddedMigrationsLoad checks the repo's own embedded migrations: no
// duplicate number, each with an up.sql. The test that lets a number collision
// in a merge surface before it hits an instance at startup.
func TestEmbeddedMigrationsLoad(t *testing.T) {
	ms, err := loadMigrations(migrations.FS)
	if err != nil {
		t.Fatalf("eingebettete Migrationen: %v", err)
	}
	if len(ms) == 0 {
		t.Fatal("keine eingebetteten Migrationen gefunden")
	}
}
