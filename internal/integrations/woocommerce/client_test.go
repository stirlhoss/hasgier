package woocommerce

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetOrder(t *testing.T) {
	order := Order{
		ID:       123,
		Number:   "123",
		Status:   "processing",
		Currency: "USD",
		Total:    "99.99",
		Billing: Address{
			FirstName: "John",
			LastName:  "Doe",
			Email:     "john@example.com",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wp-json/wc/v3/orders/123" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		user, pass, ok := r.BasicAuth()
		if !ok || user != "ck_test" || pass != "cs_test" {
			t.Errorf("unexpected auth: %s / %s", user, pass)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(order)
	}))
	defer server.Close()

	client := NewClient(server.URL, "ck_test", "cs_test")
	result, err := client.GetOrder(context.Background(), 123)
	if err != nil {
		t.Fatalf("GetOrder() error = %v", err)
	}

	if result.ID != 123 {
		t.Errorf("ID = %d, want 123", result.ID)
	}
	if result.Billing.Email != "john@example.com" {
		t.Errorf("Billing.Email = %q", result.Billing.Email)
	}
}

func TestGetOrder_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"code":"woocommerce_rest_shop_order_invalid_id","message":"Invalid ID.","data":{"status":404}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "ck_test", "cs_test")
	_, err := client.GetOrder(context.Background(), 999)
	if err == nil {
		t.Fatal("GetOrder() expected error")
	}
}

func TestListOrders(t *testing.T) {
	orders := []Order{
		{ID: 1, Number: "1", Status: "processing"},
		{ID: 2, Number: "2", Status: "completed"},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(orders)
	}))
	defer server.Close()

	client := NewClient(server.URL, "ck_test", "cs_test")
	result, err := client.ListOrders(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListOrders() error = %v", err)
	}

	if len(result) != 2 {
		t.Errorf("len(result) = %d, want 2", len(result))
	}
}
