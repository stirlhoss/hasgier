package webhook

import (
	"bytes"
	"compress/gzip"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"hasgier/internal/config"
	"hasgier/internal/testutil"
	"hasgier/internal/workers"
)

var testDB *testutil.TestDB

func TestMain(m *testing.M) {
	testDB = testutil.SetupSharedDB()
	code := m.Run()
	testDB.Cleanup()
	os.Exit(code)
}

func setupTestContext(t *testing.T) *Context {
	t.Helper()
	db := testutil.GetTestDB(t)

	cfg := &config.Config{
		Port:                 8080,
		DatabaseURL:          db.URL,
		WooWebhookSecret:     "test-webhook-secret",
		ShipStationAPIKey:    "ss_key",
		ShipStationAPISecret: "ss_secret",
		QBClientID:           "qb_id",
		QBClientSecret:       "qb_secret",
		QBRedirectURI:        "https://example.com/callback",
		QBEnvironment:        "sandbox",
	}

	riverClient, err := river.NewClient(riverpgxv5.New(db.Pool), &river.Config{
		Workers: workers.NewWorkers(),
	})
	if err != nil {
		t.Fatalf("create river client: %v", err)
	}

	return &Context{
		Config: cfg,
		Pool:   db.Pool,
		River:  riverClient,
	}
}

func TestWooWebhook_ValidSignature(t *testing.T) {
	ctx := setupTestContext(t)

	payload := []byte(`{"id":123,"status":"processing"}`)
	secret := "test-webhook-secret"

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	signature := fmt.Sprintf("%x", mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/webhooks/woocommerce", bytes.NewReader(payload))
	req.Header.Set("X-WC-Webhook-Signature", signature)
	req.Header.Set("X-WC-Webhook-Topic", "order.created")
	req.Header.Set("X-WC-Webhook-Delivery-ID", "delivery-123")

	w := httptest.NewRecorder()
	ctx.handleWooWebhook(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("status = %d, want %d", w.Code, http.StatusAccepted)
	}

	// Verify idempotency record was created
	var exists bool
	err := testDB.Pool.QueryRow(req.Context(),
		`SELECT EXISTS(SELECT 1 FROM webhook_events WHERE idem_key = $1)`,
		"woo_order.created_delivery-123").Scan(&exists)
	if err != nil {
		t.Fatalf("check idempotency: %v", err)
	}
	if !exists {
		t.Error("expected idempotency record to exist")
	}
}

func TestWooWebhook_InvalidSignature(t *testing.T) {
	ctx := setupTestContext(t)

	payload := []byte(`{"id":123}`)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/woocommerce", bytes.NewReader(payload))
	req.Header.Set("X-WC-Webhook-Signature", "invalid")
	req.Header.Set("X-WC-Webhook-Topic", "order.created")

	w := httptest.NewRecorder()
	ctx.handleWooWebhook(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestWooWebhook_DuplicateDelivery(t *testing.T) {
	ctx := setupTestContext(t)

	payload := []byte(`{"id":123}`)
	secret := "test-webhook-secret"

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	signature := fmt.Sprintf("%x", mac.Sum(nil))

	// First request
	req := httptest.NewRequest(http.MethodPost, "/webhooks/woocommerce", bytes.NewReader(payload))
	req.Header.Set("X-WC-Webhook-Signature", signature)
	req.Header.Set("X-WC-Webhook-Topic", "order.created")
	req.Header.Set("X-WC-Webhook-Delivery-ID", "delivery-dup")

	w := httptest.NewRecorder()
	ctx.handleWooWebhook(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("first request: status = %d", w.Code)
	}

	// Second request with same delivery ID should be deduped
	req2 := httptest.NewRequest(http.MethodPost, "/webhooks/woocommerce", bytes.NewReader(payload))
	req2.Header.Set("X-WC-Webhook-Signature", signature)
	req2.Header.Set("X-WC-Webhook-Topic", "order.created")
	req2.Header.Set("X-WC-Webhook-Delivery-ID", "delivery-dup")

	w2 := httptest.NewRecorder()
	ctx.handleWooWebhook(w2, req2)
	if w2.Code != http.StatusAccepted {
		t.Errorf("duplicate request: status = %d, want %d", w2.Code, http.StatusAccepted)
	}

	// Should only have one record
	var count int
	err := testDB.Pool.QueryRow(req.Context(),
		`SELECT COUNT(*) FROM webhook_events WHERE idem_key = $1`,
		"woo_order.created_delivery-dup").Scan(&count)
	if err != nil {
		t.Fatalf("count records: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestSSWebhook_ValidAuth(t *testing.T) {
	ctx := setupTestContext(t)

	payload := map[string]string{"order_id": "123"}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/shipstation", bytes.NewReader(body))
	req.SetBasicAuth("ss_key", "ss_secret")
	req.Header.Set("X-ShipStation-Event", "SHIP_NOTIFY")

	w := httptest.NewRecorder()
	ctx.handleSSWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	// Verify idempotency record was created
	var exists bool
	err := testDB.Pool.QueryRow(req.Context(),
		`SELECT EXISTS(SELECT 1 FROM webhook_events WHERE idem_key = $1)`,
		"ss_SHIP_NOTIFY").Scan(&exists)
	if err != nil {
		t.Fatalf("check idempotency: %v", err)
	}
	if !exists {
		t.Error("expected idempotency record to exist")
	}
}

func TestSSWebhook_InvalidAuth(t *testing.T) {
	ctx := setupTestContext(t)

	payload := map[string]string{"order_id": "123"}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/shipstation", bytes.NewReader(body))
	req.SetBasicAuth("wrong", "creds")
	req.Header.Set("X-ShipStation-Event", "SHIP_NOTIFY")

	w := httptest.NewRecorder()
	ctx.handleSSWebhook(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestSSWebhook_GzipDecompression(t *testing.T) {
	ctx := setupTestContext(t)

	payload := map[string]string{"order_id": "123"}
	jsonBody, _ := json.Marshal(payload)

	// Gzip compress the body
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.Write(jsonBody)
	gz.Close()

	req := httptest.NewRequest(http.MethodPost, "/webhooks/shipstation", &buf)
	req.SetBasicAuth("ss_key", "ss_secret")
	req.Header.Set("X-ShipStation-Event", "SHIP_NOTIFY")
	req.Header.Set("Content-Encoding", "gzip")

	w := httptest.NewRecorder()
	ctx.handleSSWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}
