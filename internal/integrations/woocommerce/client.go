package woocommerce

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type Client struct {
	baseURL        string
	consumerKey    string
	consumerSecret string
	httpClient     *http.Client
}

func NewClient(baseURL, consumerKey, consumerSecret string) *Client {
	return &Client{
		baseURL:        strings.TrimSuffix(baseURL, "/") + "/wp-json/wc/v3",
		consumerKey:    consumerKey,
		consumerSecret: consumerSecret,
		httpClient:     &http.Client{},
	}
}

func (c *Client) GetOrder(ctx context.Context, id int) (*Order, error) {
	req, err := c.newRequest(ctx, http.MethodGet, fmt.Sprintf("/orders/%d", id), nil)
	if err != nil {
		return nil, err
	}

	var order Order
	if err := c.do(req, &order); err != nil {
		return nil, err
	}
	return &order, nil
}

func (c *Client) UpdateOrder(ctx context.Context, id int, order *Order) (*Order, error) {
	body, err := json.Marshal(order)
	if err != nil {
		return nil, fmt.Errorf("marshal order: %w", err)
	}

	req, err := c.newRequest(ctx, http.MethodPut, fmt.Sprintf("/orders/%d", id), body)
	if err != nil {
		return nil, err
	}

	var updated Order
	if err := c.do(req, &updated); err != nil {
		return nil, err
	}
	return &updated, nil
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

func (c *Client) newRequest(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	u := c.baseURL + path
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
	}

	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	c.setAuth(req)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *Client) setAuth(req *http.Request) {
	req.SetBasicAuth(c.consumerKey, c.consumerSecret)
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
		return fmt.Errorf("woocommerce API error %d: %s", resp.StatusCode, string(respBody))
	}

	if err := json.Unmarshal(respBody, result); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	return nil
}

// Order represents a WooCommerce order.
type Order struct {
	ID            int         `json:"id"`
	Number        string      `json:"number"`
	Status        string      `json:"status"`
	Currency      string      `json:"currency"`
	DateCreated   string      `json:"date_created"`
	DateModified  string      `json:"date_modified"`
	Total         string      `json:"total"`
	TotalTax      string      `json:"total_tax"`
	ShippingTotal string      `json:"shipping_total"`
	DiscountTotal string      `json:"discount_total"`
	PaymentMethod string      `json:"payment_method"`
	Billing       Address     `json:"billing"`
	Shipping      Address     `json:"shipping"`
	LineItems     []LineItem  `json:"line_items"`
	ShippingLines []ShippingLine `json:"shipping_lines"`
}

type Address struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Company   string `json:"company"`
	Address1  string `json:"address_1"`
	Address2  string `json:"address_2"`
	City      string `json:"city"`
	State     string `json:"state"`
	Postcode  string `json:"postcode"`
	Country   string `json:"country"`
	Email     string `json:"email"`
	Phone     string `json:"phone"`
}

type LineItem struct {
	ID        int     `json:"id"`
	ProductID int     `json:"product_id"`
	Name      string  `json:"name"`
	Quantity  int     `json:"quantity"`
	Price     string  `json:"price"`
	Total     string  `json:"total"`
	TotalTax  string  `json:"total_tax"`
	SKU       string  `json:"sku"`
}

type ShippingLine struct {
	ID          int    `json:"id"`
	MethodTitle string `json:"method_title"`
	MethodID    string `json:"method_id"`
	Total       string `json:"total"`
	TotalTax    string `json:"total_tax"`
}
