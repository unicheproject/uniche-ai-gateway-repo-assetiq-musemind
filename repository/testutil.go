package repository

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"

	"uniche/config"
)

// SetupTestDB opens a real, migrated Postgres pool for integration tests. It
// loads the nearest .env found walking up from the working directory, and
// skips the test when Postgres is not configured or not reachable. Use
// RunWithRollback to keep test data out of the database.
func SetupTestDB(t *testing.T) (*DB, func()) {
	t.Helper()
	loadDotEnv()

	url, err := config.Get().PostgresURL()
	if err != nil {
		t.Skipf("skipping: %v", err)
	}
	ctx := context.Background()
	db, err := NewDB(ctx, url)
	if err != nil {
		t.Skipf("skipping: cannot connect to test DB: %v", err)
	}
	if err := db.RunMigrations(ctx); err != nil {
		db.Close()
		t.Fatalf("test DB migration failed: %v", err)
	}
	return db, db.Close
}

// RunWithRollback runs fn inside a transaction that is always rolled back, so
// test data never persists. fn receives the transaction as a DBTX.
func RunWithRollback(t *testing.T, db *DB, fn func(ctx context.Context, tx pgx.Tx)) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Logf("warning: rollback failed: %v", err)
		}
	}()
	fn(ctx, tx)
}

func loadDotEnv() {
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".env")); err == nil {
			_ = godotenv.Load(filepath.Join(dir, ".env"))
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}
