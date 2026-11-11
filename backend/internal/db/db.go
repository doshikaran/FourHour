// Package db opens the SQLite database and applies the plain SQL migrations
// in migrations/.
package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Open opens the SQLite file at path with foreign keys on, a busy timeout and
// WAL journaling on every connection, and checks it with a ping.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	if path == "" || strings.ContainsRune(path, '?') {
		return nil, fmt.Errorf("db: invalid SQLite path %q", path)
	}
	dsn := path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("db: open %s: %w", path, err)
	}
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := d.PingContext(pctx); err != nil {
		return nil, errors.Join(fmt.Errorf("db: ping %s: %w", path, err), d.Close())
	}
	return d, nil
}

// Migrate applies every migration not yet recorded in schema_migrations, in
// file-name order, each in its own transaction. It returns the versions applied.
func Migrate(ctx context.Context, d *sql.DB) ([]string, error) {
	if _, err := d.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return nil, fmt.Errorf("db: create schema_migrations: %w", err)
	}
	names, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return nil, fmt.Errorf("db: list migrations: %w", err)
	}
	sort.Strings(names)
	var applied []string
	for _, name := range names {
		version := strings.TrimSuffix(strings.TrimPrefix(name, "migrations/"), ".sql")
		done, err := isApplied(ctx, d, version)
		if err != nil {
			return applied, err
		}
		if done {
			continue
		}
		body, err := migrationFS.ReadFile(name)
		if err != nil {
			return applied, fmt.Errorf("db: read %s: %w", name, err)
		}
		if err := applyOne(ctx, d, version, string(body)); err != nil {
			return applied, err
		}
		applied = append(applied, version)
	}
	return applied, nil
}

func isApplied(ctx context.Context, d *sql.DB, version string) (bool, error) {
	var n int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version = ?`, version).Scan(&n); err != nil {
		return false, fmt.Errorf("db: check migration %s: %w", version, err)
	}
	return n > 0, nil
}

func applyOne(ctx context.Context, d *sql.DB, version, body string) (err error) {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("db: begin migration %s: %w", version, err)
	}
	defer func() {
		if err != nil {
			if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
				err = errors.Join(err, fmt.Errorf("db: rollback migration %s: %w", version, rbErr))
			}
		}
	}()
	if _, err = tx.ExecContext(ctx, body); err != nil {
		return fmt.Errorf("db: apply migration %s: %w", version, err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, version, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("db: record migration %s: %w", version, err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("db: commit migration %s: %w", version, err)
	}
	return nil
}
