package db

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Versioned migrations live in db/migrations as NNN_name.sql and are embedded
// in the binary. They are applied manually with "secretary migrate apply";
// only the 001 baseline (the tables the app always created at boot) runs
// automatically, so a deploy never changes the schema by surprise.

//go:embed migrations/*.sql
var migrationFiles embed.FS

// BaselineVersion is the only migration applied automatically at boot.
const BaselineVersion = "001"

// migrationLockID serializes concurrent "migrate apply" runs.
const migrationLockID = 727_001

type Migration struct {
	Version  string
	Name     string
	SQL      string
	Checksum string
}

type MigrationState struct {
	Migration
	Applied   bool
	AppliedAt time.Time
	// Modified is true when the file changed after it was applied.
	Modified bool
}

func LoadMigrations() ([]Migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, err
	}
	var out []Migration
	seen := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		version, rest, ok := strings.Cut(strings.TrimSuffix(name, ".sql"), "_")
		if !ok || version == "" {
			return nil, fmt.Errorf("migration %q must be named NNN_description.sql", name)
		}
		if other, dup := seen[version]; dup {
			return nil, fmt.Errorf("migrations %q and %q share version %s", other, name, version)
		}
		seen[version] = name
		body, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(body)
		out = append(out, Migration{Version: version, Name: rest, SQL: string(body), Checksum: hex.EncodeToString(sum[:])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

const createMigrationsTable = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    checksum TEXT NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`

func MigrationStatus(ctx context.Context, pool *pgxpool.Pool) ([]MigrationState, error) {
	migrations, err := LoadMigrations()
	if err != nil {
		return nil, err
	}
	if _, err := pool.Exec(ctx, createMigrationsTable); err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx, "SELECT version, checksum, applied_at FROM schema_migrations")
	if err != nil {
		return nil, err
	}
	type applied struct {
		checksum string
		at       time.Time
	}
	done := map[string]applied{}
	for rows.Next() {
		var v, c string
		var at time.Time
		if err := rows.Scan(&v, &c, &at); err != nil {
			rows.Close()
			return nil, err
		}
		done[v] = applied{c, at}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]MigrationState, 0, len(migrations))
	for _, m := range migrations {
		st := MigrationState{Migration: m}
		if a, ok := done[m.Version]; ok {
			st.Applied = true
			st.AppliedAt = a.at
			st.Modified = a.checksum != m.Checksum
		}
		out = append(out, st)
	}
	return out, nil
}

// PendingMigrations lists versions not applied yet.
func PendingMigrations(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	states, err := MigrationStatus(ctx, pool)
	if err != nil {
		return nil, err
	}
	var pending []string
	for _, s := range states {
		if !s.Applied {
			pending = append(pending, s.Version)
		}
	}
	return pending, nil
}

// ApplyMigrations applies every pending migration up to and including
// upTo ("" = all), each in its own transaction. It refuses to run when an
// applied migration was edited afterwards.
func ApplyMigrations(ctx context.Context, pool *pgxpool.Pool, upTo string) ([]string, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockID); err != nil {
		return nil, err
	}
	defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", migrationLockID)

	states, err := MigrationStatus(ctx, pool)
	if err != nil {
		return nil, err
	}
	for _, s := range states {
		if s.Modified {
			return nil, fmt.Errorf("migration %s_%s changed after it was applied; create a new migration instead", s.Version, s.Name)
		}
	}

	var applied []string
	for _, s := range states {
		if upTo != "" && s.Version > upTo {
			break
		}
		if s.Applied {
			continue
		}
		err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, s.SQL); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)", s.Version, s.Name, s.Checksum)
			return err
		})
		if err != nil {
			return applied, fmt.Errorf("migration %s_%s failed: %w", s.Version, s.Name, err)
		}
		applied = append(applied, s.Version)
	}
	return applied, nil
}

// EnsureBaseline applies the 001 baseline at boot. It only ever creates the
// tables the Secretária has always created on startup.
func EnsureBaseline(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := ApplyMigrations(ctx, pool, BaselineVersion)
	return err
}

// IsApplied reports whether a migration version is recorded.
func IsApplied(ctx context.Context, pool *pgxpool.Pool, version string) (bool, error) {
	var ok bool
	err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)", version).Scan(&ok)
	return ok, err
}
