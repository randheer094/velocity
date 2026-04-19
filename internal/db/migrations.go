package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

const migrationsTable = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INT         PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`

type migration struct {
	version int
	name    string
	sql     string
}

// migrate applies every embedded `NNNN_*.sql` whose version is not yet
// recorded in schema_migrations. Each migration runs inside a single
// transaction; Postgres supports transactional DDL so a failed file
// leaves no partial state behind.
func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, migrationsTable); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied, err := appliedVersions(ctx, pool)
	if err != nil {
		return err
	}

	migs, err := loadMigrations()
	if err != nil {
		return err
	}

	for _, m := range migs {
		if applied[m.version] {
			continue
		}
		if err := applyMigration(ctx, pool, m); err != nil {
			return err
		}
		slog.Info("applied migration", "version", m.version, "name", m.name)
	}
	return nil
}

func appliedVersions(ctx context.Context, pool *pgxpool.Pool) (map[int]bool, error) {
	rows, err := pool.Query(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = true
	}
	return out, rows.Err()
}

func applyMigration(ctx context.Context, pool *pgxpool.Pool, m migration) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin %s: %w", m.name, err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, m.sql); err != nil {
		return fmt.Errorf("apply %s: %w", m.name, err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", m.version); err != nil {
		return fmt.Errorf("record %s: %w", m.name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit %s: %w", m.name, err)
	}
	return nil
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}
	var out []migration
	seen := map[int]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		// Filenames must be "NNNN_description.sql". Anything else is a
		// packaging mistake and should fail loudly at start, not silently.
		parts := strings.SplitN(name, "_", 2)
		if len(parts) < 2 {
			return nil, fmt.Errorf("migration %q: expected NNNN_description.sql", name)
		}
		v, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("migration %q: bad version prefix: %w", name, err)
		}
		if prev, dup := seen[v]; dup {
			return nil, fmt.Errorf("migration version %d used by both %s and %s", v, prev, name)
		}
		seen[v] = name
		body, err := fs.ReadFile(migrationsFS, "migrations/"+name)
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: v, name: name, sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}
