package db

import (
	"strings"
	"testing"
	"testing/fstest"

	"covey/migrations"
)

// Zwei Branches vergeben parallel dieselbe Nummer. Bisher fraß loadMigrations
// das still: beide Dateien landeten auf demselben migration-Struct, die
// alphabetisch spätere überschrieb die SQL der früheren, verbucht wurde eine
// Version — und die verlorene Migration lief nie. Das darf nicht laden.
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

// Der Normalfall bleibt: up/down desselben Namens gehören zusammen, und die
// Reihenfolge ist die der Nummern, nicht die des Verzeichnisses.
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

// Die eingebetteten Migrationen des Repos selbst: keine doppelte Nummer, jede
// mit up.sql. Der Test, der eine Nummernkollision im Merge auffliegen lässt,
// bevor sie eine Instanz beim Start trifft.
func TestEmbeddedMigrationsLoad(t *testing.T) {
	ms, err := loadMigrations(migrations.FS)
	if err != nil {
		t.Fatalf("eingebettete Migrationen: %v", err)
	}
	if len(ms) == 0 {
		t.Fatal("keine eingebetteten Migrationen gefunden")
	}
}
