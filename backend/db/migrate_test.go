package db_test

import (
	"context"
	"testing"

	"secretary/db"
	"secretary/db/dbtest"
)

func TestMigrationsLoadInOrder(t *testing.T) {
	ms, err := db.LoadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 || ms[0].Version != db.BaselineVersion {
		t.Fatalf("first migration = %+v, want baseline %s", ms, db.BaselineVersion)
	}
	for i := 1; i < len(ms); i++ {
		if ms[i-1].Version >= ms[i].Version {
			t.Fatalf("migrations out of order: %s then %s", ms[i-1].Version, ms[i].Version)
		}
	}
}

func TestApplyIsIdempotentAndTracksStatus(t *testing.T) {
	pool := dbtest.New(t) // applies everything once
	ctx := context.Background()

	again, err := db.ApplyMigrations(ctx, pool, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("second apply ran %v", again)
	}
	pending, err := db.PendingMigrations(ctx, pool)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending = %v, err %v", pending, err)
	}
}

func TestEditedMigrationBlocksApply(t *testing.T) {
	pool := dbtest.New(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "UPDATE schema_migrations SET checksum = 'tampered' WHERE version = $1", db.BaselineVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ApplyMigrations(ctx, pool, ""); err == nil {
		t.Fatal("apply accepted a migration whose file changed after being applied")
	}
}

func TestBaselineOnlyAppliesVersion001(t *testing.T) {
	pool := dbtest.New(t)
	ctx := context.Background()
	// Start from an empty history, like a database created by the old boot code.
	if _, err := pool.Exec(ctx, "DELETE FROM schema_migrations"); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureBaseline(ctx, pool); err != nil {
		t.Fatal(err)
	}
	pending, err := db.PendingMigrations(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	all, _ := db.LoadMigrations()
	if len(pending) != len(all)-1 {
		t.Fatalf("baseline applied more than 001: pending %v of %d", pending, len(all))
	}
}
