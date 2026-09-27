// Package dbtest gives SQL tests an isolated, fully migrated schema in the
// database pointed to by TEST_DATABASE_URL (the docker compose Postgres).
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"secretary/db"

	"github.com/jackc/pgx/v5/pgxpool"
)

// New creates a throwaway schema, applies every migration inside it and
// returns a pool whose search_path points there. Tests skip when
// TEST_DATABASE_URL is unset; the database name must end in "_test" so a
// misconfigured URL can never touch real data.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing to run: TEST_DATABASE_URL database %q must end in _test", cfg.ConnConfig.Database)
	}

	ctx := context.Background()
	suffix := make([]byte, 6)
	rand.Read(suffix)
	schema := "t_" + hex.EncodeToString(suffix)

	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}

	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.ConnConfig.RuntimeParams["timezone"] = "America/Sao_Paulo"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect schema pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})

	if _, err := db.ApplyMigrations(ctx, pool, ""); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return pool
}
