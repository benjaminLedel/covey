package db

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// advisoryLockKey serializes migrations across instances (HA start-up).
const advisoryLockKey = 74_65_79 // "covey"

type migration struct {
	version int
	name    string
	upSQL   string
	downSQL string
}

// loadMigrations reads NNNN_name.up.sql / NNNN_name.down.sql from the FS.
func loadMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	byVersion := map[int]*migration{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		base, isUp := strings.CutSuffix(name, ".up.sql")
		if !isUp {
			var isDown bool
			base, isDown = strings.CutSuffix(name, ".down.sql")
			if !isDown {
				return nil, fmt.Errorf("migration %s: expected .up.sql or .down.sql", name)
			}
		}
		versionStr, _, ok := strings.Cut(base, "_")
		if !ok {
			return nil, fmt.Errorf("migration %s: expected NNNN_name", name)
		}
		version, err := strconv.Atoi(versionStr)
		if err != nil {
			return nil, fmt.Errorf("migration %s: version: %w", name, err)
		}
		content, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		m := byVersion[version]
		if m == nil {
			m = &migration{version: version, name: base}
			byVersion[version] = m
		}
		// Zwei verschiedene Migrationen unter derselben Nummer — das passiert,
		// sobald zwei Branches parallel dieselbe Nummer vergeben. Bisher gewann
		// still die alphabetisch spätere Datei: ihre SQL überschrieb die der
		// anderen, verbucht wurde aber nur eine Version, und die verlorene
		// Migration lief nie. Der Schaden zeigt sich erst als fehlende Spalte
		// im laufenden Betrieb. Fail-closed: lieber startet die Control Plane
		// gar nicht, als dass sie mit halbem Schema startet.
		if m.name != base {
			a, b := m.name, base
			if a > b {
				a, b = b, a
			}
			return nil, fmt.Errorf("migrations %s and %s share version %d — renumber one of them", a, b, version)
		}
		if isUp {
			m.upSQL = string(content)
		} else {
			m.downSQL = string(content)
		}
	}
	out := make([]migration, 0, len(byVersion))
	for _, m := range byVersion {
		if m.upSQL == "" {
			return nil, fmt.Errorf("migration %d (%s): up.sql missing", m.version, m.name)
		}
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// MigrateUp applies all pending migrations, guarded by pg_advisory_lock.
func MigrateUp(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) (applied int, err error) {
	ms, err := loadMigrations(fsys)
	if err != nil {
		return 0, err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryLockKey); err != nil {
		return 0, fmt.Errorf("advisory lock: %w", err)
	}
	defer conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", advisoryLockKey)

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return 0, err
	}
	// Angewendete Versionen EINZELN führen, nicht als Wasserstandsmarke. Ein
	// `version <= MAX(applied)` überspringt jede Migration, die unterhalb des
	// höchsten bereits angewendeten Standes nachrutscht — und genau das passiert
	// beim Mergen: zwei Branches vergeben ihre Nummern gegen dasselbe main, der
	// zweite merged mit der kleineren. Übersprungen wurde sie still und für
	// immer, denn beim nächsten Start ist die Marke unverändert hoch.
	seen := map[int]bool{}
	rows, err := conn.Query(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return 0, err
		}
		seen[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, m := range ms {
		if seen[m.version] {
			continue
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return applied, err
		}
		if _, err := tx.Exec(ctx, m.upSQL); err != nil {
			tx.Rollback(ctx)
			return applied, fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version, name) VALUES ($1,$2)", m.version, m.name); err != nil {
			tx.Rollback(ctx)
			return applied, err
		}
		if err := tx.Commit(ctx); err != nil {
			return applied, err
		}
		applied++
	}
	return applied, nil
}

// MigrateDown rolls back exactly one migration (emergency; normal operation is forward-only).
func MigrateDown(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) error {
	ms, err := loadMigrations(fsys)
	if err != nil {
		return err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryLockKey); err != nil {
		return err
	}
	defer conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", advisoryLockKey)

	var current int
	if err := conn.QueryRow(ctx, "SELECT COALESCE(MAX(version),0) FROM schema_migrations").Scan(&current); err != nil {
		return err
	}
	if current == 0 {
		return fmt.Errorf("no migration applied")
	}
	for _, m := range ms {
		if m.version != current {
			continue
		}
		if m.downSQL == "" {
			return fmt.Errorf("migration %d (%s): no down.sql", m.version, m.name)
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, m.downSQL); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("down %d (%s): %w", m.version, m.name, err)
		}
		if _, err := tx.Exec(ctx, "DELETE FROM schema_migrations WHERE version=$1", m.version); err != nil {
			tx.Rollback(ctx)
			return err
		}
		return tx.Commit(ctx)
	}
	return fmt.Errorf("migration %d not found in FS", current)
}
