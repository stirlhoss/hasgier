package workers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"hasgier/internal/db/queries"
	"hasgier/internal/integrations/quickbooks"
	"hasgier/internal/integrations/woocommerce"
)

type WooOrderJob struct {
	Payload map[string]interface{} `river:"woo_order,unique"`
}

func (WooOrderJob) Kind() string { return "woo_order" }

func (j WooOrderJob) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3}
}

type WooOrderWorker struct {
	river.WorkerDefaults[WooOrderJob]
	Pool     *pgxpool.Pool
	Woo      *woocommerce.Client
	QB       *quickbooks.Client
	QBRealmID string
}

func (w *WooOrderWorker) Work(ctx context.Context, job *river.Job[WooOrderJob]) error {
	topic, _ := job.Args.Payload["topic"].(string)
	bodyRaw, _ := job.Args.Payload["body"].(json.RawMessage)

	var order woocommerce.Order
	if err := json.Unmarshal(bodyRaw, &order); err != nil {
		return fmt.Errorf("parse order: %w", err)
	}

	switch topic {
	case "order.created", "order.updated":
		return w.syncOrder(ctx, &order)
	default:
		return nil // ignore other topics
	}
}

func (w *WooOrderWorker) syncOrder(ctx context.Context, order *woocommerce.Order) error {
	// 1. Find or create QuickBooks customer by email
	customer, err := w.QB.FindCustomer(ctx, order.Billing.Email)
	if err != nil {
		return fmt.Errorf("find customer: %w", err)
	}

	if customer == nil {
		customer = &quickbooks.Customer{
			DisplayName: order.Billing.FirstName + " " + order.Billing.LastName,
			PrimaryEmailAddr: &quickbooks.Email{
				Address: order.Billing.Email,
			},
			BillAddr: &quickbooks.Addr{
				Line1:                  order.Billing.Address1,
				City:                   order.Billing.City,
				CountrySubDivisionCode: order.Billing.State,
				PostalCode:             order.Billing.Postcode,
				Country:                order.Billing.Country,
			},
		}
		customer, err = w.QB.CreateCustomer(ctx, customer)
		if err != nil {
			return fmt.Errorf("create customer: %w", err)
		}
	}

	// 2. Build QB invoice lines from WooCommerce line items
	var lines []quickbooks.InvoiceLine
	for _, item := range order.LineItems {
		itemID, err := queries.GetMapping(ctx, w.Pool, "woo_product", fmt.Sprintf("%d", item.ProductID), "qb_item")
		if err != nil {
			return fmt.Errorf("get mapping for product %d: %w", item.ProductID, err)
		}

		price := 0.0
		fmt.Sscanf(item.Price, "%f", &price)

		lines = append(lines, quickbooks.InvoiceLine{
			Amount:     price * float64(item.Quantity),
			DetailType: "SalesItemLineDetail",
			SalesItemLineDetail: &quickbooks.SalesItemLineDetail{
				ItemRef: &quickbooks.Ref{Value: itemID},
				Qty:     item.Quantity,
				UnitPrice: price,
			},
		})
	}

	// 3. Build invoice
	invoice := &quickbooks.Invoice{
		CustomerRef: &quickbooks.Ref{Value: customer.ID},
		Line:        lines,
		BillAddr: &quickbooks.Addr{
			Line1:                  order.Billing.Address1,
			City:                   order.Billing.City,
			CountrySubDivisionCode: order.Billing.State,
			PostalCode:             order.Billing.Postcode,
			Country:                order.Billing.Country,
		},
	}

	created, err := w.QB.CreateInvoice(ctx, invoice)
	if err != nil {
		queries.UpsertSyncState(ctx, w.Pool, int64(order.ID), "error", "", "", "", err.Error())
		return fmt.Errorf("create invoice: %w", err)
	}

	// 4. Update sync state
	queries.UpsertSyncState(ctx, w.Pool, int64(order.ID), "synced", created.ID, "", "", "")

	return nil
}

func NewWorkers() *river.Workers {
	workers := river.NewWorkers()
	river.AddWorker(workers, &WooOrderWorker{})
	river.AddWorker(workers, &ShipStationWorker{})
	return workers
}

// ShipStationJob is a River job for processing ShipStation webhook events.
type ShipStationJob struct {
	Payload map[string]interface{} `river:"shipstation,unique"`
}

func (ShipStationJob) Kind() string { return "shipstation" }

func (j ShipStationJob) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3}
}

type ShipStationWorker struct {
	river.WorkerDefaults[ShipStationJob]
	Pool *pgxpool.Pool
	QB   *quickbooks.Client
}

func (w *ShipStationWorker) Work(ctx context.Context, job *river.Job[ShipStationJob]) error {
	event, _ := job.Args.Payload["event"].(string)
	bodyRaw, _ := job.Args.Payload["body"].(json.RawMessage)

	switch event {
	case "SHIP_NOTIFY":
		return w.handleShipNotify(ctx, bodyRaw)
	default:
		return nil
	}
}

func (w *ShipStationWorker) handleShipNotify(ctx context.Context, body json.RawMessage) error {
	var notification struct {
		ResourceURL string `json:"resource_url"`
	}
	if err := json.Unmarshal(body, &notification); err != nil {
		return fmt.Errorf("parse notification: %w", err)
	}

	// Extract shipment ID from resource URL and fetch shipment details
	// This would typically involve calling the ShipStation API
	// For now, this is a placeholder that logs the event
	_ = notification

	return nil
}
