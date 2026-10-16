package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

var adminURL = getEnvOrDefault("TEST_DATABASE_ADMIN_URL", "postgres://stirl:postgres@localhost:5432/postgres?sslmode=disable")

// TestDB wraps a pgx pool for a shared test database.
type TestDB struct {
	Pool   *pgxpool.Pool
	URL    string
	DBName string
}

var (
	sharedDB     *TestDB
	sharedDBOnce sync.Once
	sharedDBErr  error
)

// SetupSharedDB creates (or returns) a shared test database for the entire package.
// Call this from TestMain. The database is created once and dropped when the suite ends.
//
// Example:
//
//	func TestMain(m *testing.M) {
//	    db := testutil.SetupSharedDB()
//	    code := m.Run()
//	    db.Cleanup()
//	    os.Exit(code)
//	}
func SetupSharedDB() *TestDB {
	sharedDBOnce.Do(func() {
		sharedDB, sharedDBErr = createSharedDB()
	})
	if sharedDBErr != nil {
		panic(fmt.Sprintf("setup shared test db: %v", sharedDBErr))
	}
	return sharedDB
}

// GetTestDB returns a clean database for a single test.
// Truncates all tables so each test starts with a clean slate.
func GetTestDB(t *testing.T) *TestDB {
	t.Helper()
	db := SetupSharedDB()
	db.TruncateTables(t)
	return db
}

func createSharedDB() (*TestDB, error) {
	ctx := context.Background()

	// Generate unique database name
	suffix := make([]byte, 4)
	rand.Read(suffix)
	dbName := fmt.Sprintf("hasgier_test_%s", hex.EncodeToString(suffix))

	// Connect to admin database to create test database
	adminPool, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		return nil, fmt.Errorf("connect to admin database: %w", err)
	}
	defer adminPool.Close()

	_, err = adminPool.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s", dbName))
	if err != nil {
		return nil, fmt.Errorf("create test database: %w", err)
	}

	dbURL := fmt.Sprintf("postgres://stirl:postgres@localhost:5432/%s?sslmode=disable", dbName)

	// Run app migrations
	migrationsPath, err := findMigrationsPath()
	if err != nil {
		return nil, fmt.Errorf("find migrations: %w", err)
	}

	m, err := migrate.New("file://"+migrationsPath, dbURL)
	if err != nil {
		return nil, fmt.Errorf("create migrator: %w", err)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return nil, fmt.Errorf("run migrations: %w", err)
	}
	m.Close()

	// Create pgx pool
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return nil, fmt.Errorf("create pgx pool: %w", err)
	}

	// Run river migrations
	riverMigrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return nil, fmt.Errorf("create river migrator: %w", err)
	}
	if _, err := riverMigrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return nil, fmt.Errorf("run river migrations: %w", err)
	}

	return &TestDB{
		Pool:   pool,
		URL:    dbURL,
		DBName: dbName,
	}, nil
}

// Cleanup drops the test database. Call this at the end of TestMain.
func (db *TestDB) Cleanup() {
	ctx := context.Background()

	db.Pool.Close()

	adminPool, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cleanup: connect to admin: %v\n", err)
		return
	}
	defer adminPool.Close()

	// Terminate connections before dropping
	adminPool.Exec(ctx, fmt.Sprintf(`
		SELECT pg_terminate_backend(pid)
		FROM pg_stat_activity
		WHERE datname = '%s' AND pid <> pg_backend_pid()
	`, db.DBName))

	_, err = adminPool.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", db.DBName))
	if err != nil {
		fmt.Fprintf(os.Stderr, "cleanup: drop database: %v\n", err)
	}
}

// findMigrationsPath locates the migrations directory relative to the test file.
func findMigrationsPath() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		candidate := filepath.Join(dir, "internal", "db", "migrations")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("migrations directory not found")
		}
		dir = parent
	}
}

// Seed inserts test data into the database.
func (db *TestDB) Seed(t *testing.T, queries []string) {
	t.Helper()
	for _, q := range queries {
		if _, err := db.Pool.Exec(context.Background(), q); err != nil {
			t.Fatalf("seed query failed: %v\nquery: %s", err, q)
		}
	}
}

// TruncateTables truncates all tables for a clean slate.
func (db *TestDB) TruncateTables(t *testing.T) {
	t.Helper()
	tables := []string{"webhook_events", "qb_tokens", "entity_mappings", "sync_state"}
	for _, table := range tables {
		if _, err := db.Pool.Exec(context.Background(), fmt.Sprintf("TRUNCATE TABLE %s CASCADE", table)); err != nil {
			t.Fatalf("truncate %s: %v", table, err)
		}
	}
}

func getEnvOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}
