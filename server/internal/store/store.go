// Package store is the data layer: repository interfaces backed by SQLite.
// Handlers and services never touch SQL directly — they depend on the
// interfaces defined here, so a PostgreSQL implementation can be added later.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	dsn := path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping database %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Migrate applies embedded migrations in filename order, tracking applied
// versions in schema_migrations. Migration files are append-only. Migrations
// run on a dedicated connection with foreign keys disabled so table rebuilds
// (SQLite's only way to change constraints) don't cascade into child tables.
func (s *Store) Migrate(ctx context.Context) (retErr error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migration connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("disable foreign keys: %w", err)
	}
	// The connection goes back to the pool afterwards, so failing to turn
	// foreign keys back on would leave a connection that silently ignores
	// them: that fails the migration rather than passing unnoticed.
	defer func() {
		if _, err := conn.ExecContext(context.WithoutCancel(ctx), `PRAGMA foreign_keys = ON`); err != nil && retErr == nil {
			retErr = fmt.Errorf("re-enable foreign keys: %w", err)
		}
	}()

	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	names, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(names)

	for _, name := range names {
		base := strings.TrimPrefix(name, "migrations/")
		numStr, _, _ := strings.Cut(base, "_")
		version, err := strconv.Atoi(numStr)
		if err != nil {
			return fmt.Errorf("migration %s: name must start with a version number: %w", base, err)
		}

		var applied int
		if err := conn.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %d: %w", version, err)
		}
		if applied > 0 {
			continue
		}

		ddl, err := fs.ReadFile(migrationsFS, name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", base, err)
		}
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, string(ddl)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", base, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version) VALUES (?)`, version); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %d: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", version, err)
		}
	}
	return nil
}
