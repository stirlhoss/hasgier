package quickbooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

type Client struct {
	oauthCfg   *OAuthConfig
	tokenFunc  TokenProvider
	httpClient *http.Client
	realmID    string
	baseURL    string // override for testing
}

// TokenProvider returns a valid access token, refreshing if needed.
type TokenProvider func(ctx context.Context) (string, error)

func NewClient(oauthCfg *OAuthConfig, tokenFunc TokenProvider, realmID string) *Client {
	return &Client{
		oauthCfg:   oauthCfg,
		tokenFunc:  tokenFunc,
		httpClient: &http.Client{},
		realmID:    realmID,
	}
}

// SetBaseURL overrides the base URL for testing.
func (c *Client) SetBaseURL(url string) {
	c.baseURL = url
}

func (c *Client) getBaseURL() string {
	if c.baseURL != "" {
		return c.baseURL
	}
	return c.oauthCfg.BaseURL()
}

func (c *Client) CreateInvoice(ctx context.Context, invoice *Invoice) (*Invoice, error) {
	var result Invoice
	if err := c.post(ctx, "/v3/company/"+c.realmID+"/invoice", invoice, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) CreatePayment(ctx context.Context, payment *Payment) (*Payment, error) {
	var result Payment
	if err := c.post(ctx, "/v3/company/"+c.realmID+"/payment", payment, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) Query(ctx context.Context, query string) ([]byte, error) {
	params := url.Values{"query": {query}}
	path := "/v3/company/" + c.realmID + "/query?" + params.Encode()
	return c.get(ctx, path)
}

func (c *Client) FindCustomer(ctx context.Context, email string) (*Customer, error) {
	query := fmt.Sprintf("SELECT * FROM Customer WHERE PrimaryEmailAddr.Address = '%s'", email)
	data, err := c.Query(ctx, query)
	if err != nil {
		return nil, err
	}

	var resp struct {
		QueryResponse struct {
			Customers []Customer `json:"Customer"`
		} `json:"QueryResponse"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse query response: %w", err)
	}

	if len(resp.QueryResponse.Customers) == 0 {
		return nil, nil // not found
	}
	return &resp.QueryResponse.Customers[0], nil
}

func (c *Client) CreateCustomer(ctx context.Context, customer *Customer) (*Customer, error) {
	var result Customer
	if err := c.post(ctx, "/v3/company/"+c.realmID+"/customer", customer, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	token, err := c.tokenFunc(ctx)
	if err != nil {
		return nil, fmt.Errorf("get token: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.getBaseURL()+path, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("quickbooks API error %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

func (c *Client) post(ctx context.Context, path string, payload, result interface{}) error {
	token, err := c.tokenFunc(ctx)
	if err != nil {
		return fmt.Errorf("get token: %w", err)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.getBaseURL()+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

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
		return fmt.Errorf("quickbooks API error %d: %s", resp.StatusCode, string(respBody))
	}

	if err := json.Unmarshal(respBody, result); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	return nil
}

// QB types

type Customer struct {
	ID               string  `json:"Id"`
	DisplayName      string  `json:"DisplayName"`
	PrimaryEmailAddr *Email  `json:"PrimaryEmailAddr,omitempty"`
	PrimaryPhone     *Phone  `json:"PrimaryPhone,omitempty"`
	BillAddr         *Addr   `json:"BillAddr,omitempty"`
	ShipAddr         *Addr   `json:"ShipAddr,omitempty"`
}

type Email struct {
	Address string `json:"Address"`
}

type Phone struct {
	FreeFormNumber string `json:"FreeFormNumber"`
}

type Addr struct {
	Line1                  string `json:"Line1,omitempty"`
	City                   string `json:"City,omitempty"`
	CountrySubDivisionCode string `json:"CountrySubDivisionCode,omitempty"`
	PostalCode             string `json:"PostalCode,omitempty"`
	Country                string `json:"Country,omitempty"`
}

type Invoice struct {
	ID          string        `json:"Id"`
	DocNumber   string        `json:"DocNumber,omitempty"`
	TxnDate     string        `json:"TxnDate,omitempty"`
	CustomerRef *Ref          `json:"CustomerRef"`
	Line        []InvoiceLine `json:"Line"`
	TotalAmt    float64       `json:"TotalAmt,omitempty"`
	BillAddr    *Addr         `json:"BillAddr,omitempty"`
	ShipAddr    *Addr         `json:"ShipAddr,omitempty"`
	TrackingNum string        `json:"TrackingNum,omitempty"`
}

type InvoiceLine struct {
	Amount     float64 `json:"Amount"`
	DetailType string  `json:"DetailType"`
	Description string `json:"Description,omitempty"`
	SalesItemLineDetail *SalesItemLineDetail `json:"SalesItemLineDetail,omitempty"`
	SubTotalLineDetail  *SubTotalLineDetail  `json:"SubTotalLineDetail,omitempty"`
}

type SalesItemLineDetail struct {
	ItemRef   *Ref    `json:"ItemRef"`
	Qty       int     `json:"Qty"`
	UnitPrice float64 `json:"UnitPrice"`
}

type SubTotalLineDetail struct {
	TotalAmt float64 `json:"TotalAmt"`
}

type Ref struct {
	Value string `json:"value"`
	Name  string `json:"name,omitempty"`
}

type Payment struct {
	ID          string       `json:"Id"`
	TotalAmt    float64      `json:"TotalAmt"`
	CustomerRef *Ref         `json:"CustomerRef"`
	Line        []PaymentLine `json:"Line,omitempty"`
}

type PaymentLine struct {
	Amount    float64      `json:"Amount"`
	LinkedTxn []LinkedTxn  `json:"LinkedTxn"`
}

type LinkedTxn struct {
	TxnID   string `json:"TxnId"`
	TxnType string `json:"TxnType"`
}
