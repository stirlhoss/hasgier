package shipstation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetShipment(t *testing.T) {
	shipment := Shipment{
		ShipmentID:     456,
		OrderID:        123,
		OrderNumber:    "1001",
		TrackingNumber: "9400111899223344556677",
		CarrierCode:    "usps",
		ServiceCode:    "usps_priority_mail",
		ShipDate:       "2026-01-15",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/shipments/456" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		user, pass, ok := r.BasicAuth()
		if !ok || user != "api_key" || pass != "api_secret" {
			t.Errorf("unexpected auth: %s / %s", user, pass)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(shipment)
	}))
	defer server.Close()

	client := NewClient("api_key", "api_secret")
	client.SetBaseURL(server.URL)
	result, err := client.GetShipment(context.Background(), 456)
	if err != nil {
		t.Fatalf("GetShipment() error = %v", err)
	}

	if result.ShipmentID != 456 {
		t.Errorf("ShipmentID = %d, want 456", result.ShipmentID)
	}
	if result.TrackingNumber != "9400111899223344556677" {
		t.Errorf("TrackingNumber = %q", result.TrackingNumber)
	}
}

func TestGetShipment_Unauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"Unauthorized"}`))
	}))
	defer server.Close()

	client := NewClient("bad_key", "bad_secret")
	client.SetBaseURL(server.URL)
	_, err := client.GetShipment(context.Background(), 456)
	if err == nil {
		t.Fatal("GetShipment() expected error")
	}
}
