package quickbooks

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFindCustomer(t *testing.T) {
	customer := Customer{
		ID:          "42",
		DisplayName: "John Doe",
		PrimaryEmailAddr: &Email{
			Address: "john@example.com",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}

		resp := map[string]interface{}{
			"QueryResponse": map[string]interface{}{
				"Customer": []Customer{customer},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := &OAuthConfig{Environment: "sandbox"}
	client := NewClient(cfg, func(ctx context.Context) (string, error) {
		return "test-token", nil
	}, "1234567890")
	client.SetBaseURL(server.URL)

	result, err := client.FindCustomer(context.Background(), "john@example.com")
	if err != nil {
		t.Fatalf("FindCustomer() error = %v", err)
	}

	if result == nil {
		t.Fatal("FindCustomer() returned nil")
	}
	if result.ID != "42" {
		t.Errorf("ID = %q, want 42", result.ID)
	}
}

func TestFindCustomer_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"QueryResponse": map[string]interface{}{},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := &OAuthConfig{Environment: "sandbox"}
	client := NewClient(cfg, func(ctx context.Context) (string, error) {
		return "test-token", nil
	}, "1234567890")
	client.SetBaseURL(server.URL)

	result, err := client.FindCustomer(context.Background(), "nobody@example.com")
	if err != nil {
		t.Fatalf("FindCustomer() error = %v", err)
	}
	if result != nil {
		t.Errorf("FindCustomer() = %v, want nil", result)
	}
}

func TestCreateInvoice(t *testing.T) {
	invoice := Invoice{
		ID:         "100",
		DocNumber:  "INV-001",
		CustomerRef: &Ref{Value: "42"},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected content-type: %s", r.Header.Get("Content-Type"))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(invoice)
	}))
	defer server.Close()

	cfg := &OAuthConfig{Environment: "sandbox"}
	client := NewClient(cfg, func(ctx context.Context) (string, error) {
		return "test-token", nil
	}, "1234567890")
	client.SetBaseURL(server.URL)

	newInvoice := &Invoice{
		CustomerRef: &Ref{Value: "42"},
		Line: []InvoiceLine{
			{
				Amount:     99.99,
				DetailType: "SalesItemLineDetail",
				SalesItemLineDetail: &SalesItemLineDetail{
					ItemRef: &Ref{Value: "1"},
					Qty:     1,
					UnitPrice: 99.99,
				},
			},
		},
	}

	result, err := client.CreateInvoice(context.Background(), newInvoice)
	if err != nil {
		t.Fatalf("CreateInvoice() error = %v", err)
	}
	if result.ID != "100" {
		t.Errorf("ID = %q, want 100", result.ID)
	}
}

func TestOAuthConfig_BaseURL(t *testing.T) {
	tests := []struct {
		env  string
		want string
	}{
		{"sandbox", qbSandboxBaseURL},
		{"production", qbProductionBaseURL},
		{"", qbSandboxBaseURL},
	}

	for _, tt := range tests {
		t.Run(tt.env, func(t *testing.T) {
			cfg := &OAuthConfig{Environment: tt.env}
			if got := cfg.BaseURL(); got != tt.want {
				t.Errorf("BaseURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetAuthorizationURL(t *testing.T) {
	cfg := &OAuthConfig{
		ClientID:    "test-client-id",
		RedirectURI: "https://example.com/callback",
	}

	url := cfg.GetAuthorizationURL("test-state")

	if url == "" {
		t.Error("GetAuthorizationURL() returned empty string")
	}
	// Should contain the client ID and redirect URI
	if !contains(url, "test-client-id") {
		t.Error("URL missing client_id")
	}
	if !contains(url, "example.com") {
		t.Error("URL missing redirect_uri")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
