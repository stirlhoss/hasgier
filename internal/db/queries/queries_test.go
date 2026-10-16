package queries

import (
	"context"
	"os"
	"testing"
	"time"

	"hasgier/internal/testutil"
)

var testDB *testutil.TestDB

func TestMain(m *testing.M) {
	testDB = testutil.SetupSharedDB()
	code := m.Run()
	testDB.Cleanup()
	os.Exit(code)
}

func TestIdempotency(t *testing.T) {
	db := testutil.GetTestDB(t)
	ctx := context.Background()

	// Insert a key
	err := InsertIdempotencyKey(ctx, db.Pool, "test_key_1", "woocommerce")
	if err != nil {
		t.Fatalf("InsertIdempotencyKey() error = %v", err)
	}

	// Check it exists
	exists, err := CheckIdempotencyKey(ctx, db.Pool, "test_key_1")
	if err != nil {
		t.Fatalf("CheckIdempotencyKey() error = %v", err)
	}
	if !exists {
		t.Error("expected key to exist")
	}

	// Duplicate insert should not error (ON CONFLICT DO NOTHING)
	err = InsertIdempotencyKey(ctx, db.Pool, "test_key_1", "woocommerce")
	if err != nil {
		t.Fatalf("duplicate InsertIdempotencyKey() error = %v", err)
	}

	// Non-existent key
	exists, err = CheckIdempotencyKey(ctx, db.Pool, "nonexistent")
	if err != nil {
		t.Fatalf("CheckIdempotencyKey() error = %v", err)
	}
	if exists {
		t.Error("expected key to not exist")
	}

	// Mark done
	err = MarkIdempotencyDone(ctx, db.Pool, "test_key_1")
	if err != nil {
		t.Fatalf("MarkIdempotencyDone() error = %v", err)
	}
}

func TestQBTokens(t *testing.T) {
	db := testutil.GetTestDB(t)
	ctx := context.Background()

	token := &QBToken{
		RealmID:      "1234567890",
		AccessToken:  "test_access_token",
		RefreshToken: "test_refresh_token",
		ExpiresAt:    time.Now().Add(time.Hour),
	}

	// Insert
	err := UpsertQBToken(ctx, db.Pool, token)
	if err != nil {
		t.Fatalf("UpsertQBToken() error = %v", err)
	}

	// Read back
	got, err := GetQBToken(ctx, db.Pool, "1234567890")
	if err != nil {
		t.Fatalf("GetQBToken() error = %v", err)
	}
	if got.AccessToken != "test_access_token" {
		t.Errorf("AccessToken = %q, want %q", got.AccessToken, "test_access_token")
	}

	// Update
	token.AccessToken = "updated_access_token"
	err = UpsertQBToken(ctx, db.Pool, token)
	if err != nil {
		t.Fatalf("UpsertQBToken() update error = %v", err)
	}

	got, err = GetQBToken(ctx, db.Pool, "1234567890")
	if err != nil {
		t.Fatalf("GetQBToken() after update error = %v", err)
	}
	if got.AccessToken != "updated_access_token" {
		t.Errorf("AccessToken = %q, want %q", got.AccessToken, "updated_access_token")
	}
}

func TestMappings(t *testing.T) {
	db := testutil.GetTestDB(t)
	ctx := context.Background()

	// Insert a mapping
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO entity_mappings (source_type, source_id, target_type, target_id)
		 VALUES ('woo_product', '42', 'qb_item', '100')`)
	if err != nil {
		t.Fatalf("insert mapping: %v", err)
	}

	// Look it up
	targetID, err := GetMapping(ctx, db.Pool, "woo_product", "42", "qb_item")
	if err != nil {
		t.Fatalf("GetMapping() error = %v", err)
	}
	if targetID != "100" {
		t.Errorf("targetID = %q, want %q", targetID, "100")
	}
}

func TestSyncState(t *testing.T) {
	db := testutil.GetTestDB(t)
	ctx := context.Background()

	// Insert
	err := UpsertSyncState(ctx, db.Pool, 123, "received", "", "", "", "")
	if err != nil {
		t.Fatalf("UpsertSyncState() error = %v", err)
	}

	// Read back
	state, err := GetSyncState(ctx, db.Pool, 123)
	if err != nil {
		t.Fatalf("GetSyncState() error = %v", err)
	}
	if state.Status != "received" {
		t.Errorf("Status = %q, want %q", state.Status, "received")
	}

	// Update
	err = UpsertSyncState(ctx, db.Pool, 123, "synced", "inv_100", "", "", "")
	if err != nil {
		t.Fatalf("UpsertSyncState() update error = %v", err)
	}

	state, err = GetSyncState(ctx, db.Pool, 123)
	if err != nil {
		t.Fatalf("GetSyncState() after update error = %v", err)
	}
	if state.Status != "synced" {
		t.Errorf("Status = %q, want %q", state.Status, "synced")
	}
	if state.QBInvoiceID == nil || *state.QBInvoiceID != "inv_100" {
		t.Errorf("QBInvoiceID = %v, want inv_100", state.QBInvoiceID)
	}
}

func TestSeed(t *testing.T) {
	db := testutil.GetTestDB(t)

	// Test seeding
	db.Seed(t, []string{
		`INSERT INTO entity_mappings (source_type, source_id, target_type, target_id)
		 VALUES ('woo_product', '1', 'qb_item', '200')`,
		`INSERT INTO entity_mappings (source_type, source_id, target_type, target_id)
		 VALUES ('woo_product', '2', 'qb_item', '201')`,
	})

	ctx := context.Background()
	targetID, err := GetMapping(ctx, db.Pool, "woo_product", "1", "qb_item")
	if err != nil {
		t.Fatalf("GetMapping() error = %v", err)
	}
	if targetID != "200" {
		t.Errorf("targetID = %q, want %q", targetID, "200")
	}
}
