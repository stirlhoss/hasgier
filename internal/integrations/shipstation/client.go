package shipstation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

type Client struct {
	baseURL    string
	apiKey     string
	apiSecret  string
	httpClient *http.Client
}

func NewClient(apiKey, apiSecret string) *Client {
	return &Client{
		baseURL:    "https://ssapi.shipstation.com",
		apiKey:     apiKey,
		apiSecret:  apiSecret,
		httpClient: &http.Client{},
	}
}

// SetBaseURL overrides the base URL for testing.
func (c *Client) SetBaseURL(url string) {
	c.baseURL = url
}

func (c *Client) GetShipment(ctx context.Context, shipmentID int) (*Shipment, error) {
	req, err := c.newRequest(ctx, http.MethodGet, fmt.Sprintf("/shipments/%d", shipmentID))
	if err != nil {
		return nil, err
	}

	var shipment Shipment
	if err := c.do(req, &shipment); err != nil {
		return nil, err
	}
	return &shipment, nil
}

func (c *Client) ListOrders(ctx context.Context, params url.Values) ([]Order, error) {
	u := c.baseURL + "/orders"
	if params != nil {
		u += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	c.setAuth(req)

	var orders []Order
	if err := c.do(req, &orders); err != nil {
		return nil, err
	}
	return orders, nil
}

func (c *Client) newRequest(ctx context.Context, method, path string) (*http.Request, error) {
	u := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	c.setAuth(req)
	return req, nil
}

func (c *Client) setAuth(req *http.Request) {
	req.SetBasicAuth(c.apiKey, c.apiSecret)
}

func (c *Client) do(req *http.Request, result interface{}) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("shipstation API error %d: %s", resp.StatusCode, string(respBody))
	}

	if err := json.Unmarshal(respBody, result); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	return nil
}

// Shipment represents a ShipStation shipment.
type Shipment struct {
	ShipmentID        int      `json:"shipmentId"`
	OrderID           int      `json:"orderId"`
	OrderNumber       string   `json:"orderNumber"`
	TrackingNumber    string   `json:"trackingNumber"`
	CarrierCode       string   `json:"carrierCode"`
	ServiceCode       string   `json:"serviceCode"`
	ShipDate          string   `json:"shipDate"`
	EstimatedDelivery string   `json:"estimatedDelivery"`
	Cost              float64  `json:"cost"`
	LabelURL          string   `json:"labelUrl"`
	Items             []Item   `json:"shipmentItems"`
}

type Order struct {
	OrderID       int    `json:"orderId"`
	OrderNumber   string `json:"orderNumber"`
	OrderKey      string `json:"orderKey"`
	OrderStatus   string `json:"orderStatus"`
	CustomerEmail string `json:"customerEmail"`
}

type Item struct {
	OrderItemID int    `json:"orderItemId"`
	LineItemKey string `json:"lineItemKey"`
	SKU         string `json:"sku"`
	Name        string `json:"name"`
	Quantity    int    `json:"quantity"`
}
