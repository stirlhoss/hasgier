package config

import (
	"os"
	"testing"
)

func TestLoad_Success(t *testing.T) {
	env := map[string]string{
		"DATABASE_URL":           "postgres://localhost/test",
		"WOO_URL":                "https://store.example.com",
		"WOO_CONSUMER_KEY":       "ck_test",
		"WOO_CONSUMER_SECRET":    "cs_test",
		"WOO_WEBHOOK_SECRET":     "secret",
		"SHIPSTATION_API_KEY":    "ss_key",
		"SHIPSTATION_API_SECRET": "ss_secret",
		"QB_CLIENT_ID":           "qb_id",
		"QB_CLIENT_SECRET":       "qb_secret",
		"QB_REDIRECT_URI":        "https://example.com/callback",
		"QB_ENVIRONMENT":         "sandbox",
	}
	for k, v := range env {
		t.Setenv(k, v)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.WooURL != "https://store.example.com" {
		t.Errorf("WooURL = %q", cfg.WooURL)
	}
	if cfg.QBEnvironment != "sandbox" {
		t.Errorf("QBEnvironment = %q", cfg.QBEnvironment)
	}
}

func TestLoad_MissingRequired(t *testing.T) {
	// Clear all env vars
	os.Clearenv()
	t.Setenv("PORT", "8080")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for missing env vars")
	}
}

func TestLoad_CustomPort(t *testing.T) {
	env := map[string]string{
		"PORT":                   "9090",
		"DATABASE_URL":           "postgres://localhost/test",
		"WOO_URL":                "https://store.example.com",
		"WOO_CONSUMER_KEY":       "ck_test",
		"WOO_CONSUMER_SECRET":    "cs_test",
		"WOO_WEBHOOK_SECRET":     "secret",
		"SHIPSTATION_API_KEY":    "ss_key",
		"SHIPSTATION_API_SECRET": "ss_secret",
		"QB_CLIENT_ID":           "qb_id",
		"QB_CLIENT_SECRET":       "qb_secret",
		"QB_REDIRECT_URI":        "https://example.com/callback",
	}
	for k, v := range env {
		t.Setenv(k, v)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Port != 9090 {
		t.Errorf("Port = %d, want 9090", cfg.Port)
	}
}
