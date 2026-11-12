package orders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/joyaljoy/waypoint/internal/platform/audit"
	"github.com/joyaljoy/waypoint/internal/platform/database"
	"github.com/joyaljoy/waypoint/internal/platform/idempotency"
	"github.com/joyaljoy/waypoint/internal/platform/outbox"
)

type Store struct {
	db *database.DB
}

func NewStore(db *database.DB) *Store {
	return &Store{db: db}
}

type productSnapshot struct {
	ID         uuid.UUID
	SKU        string
	Name       string
	PriceCents int64
	Currency   string
	Active     bool
}

func (s *Store) Create(ctx context.Context, tenantID, userID uuid.UUID, p CreateParams, orderNumber, key string) (Order, bool, error) {
	var out Order
	replayed := false

	err := s.db.InTenantTx(ctx, tenantID, userID, func(tx pgx.Tx) error {
		claim, err := idempotency.Claim(ctx, tx, tenantID, userID, key, p.RequestHash())
		if err != nil {
			return err
		}
		if claim.Replayed {
			replayed = true
			return json.Unmarshal(claim.Body, &out)
		}

		var warehouseName string
		var warehouseActive bool
		err = tx.QueryRow(ctx,
			`SELECT name, is_active FROM warehouses WHERE tenant_id = $1 AND id = $2`,
			tenantID, p.WarehouseID,
		).Scan(&warehouseName, &warehouseActive)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: warehouse not found", ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("load warehouse: %w", err)
		}
		if !warehouseActive {
			return fmt.Errorf("%w: warehouse is inactive", ErrConflict)
		}

		ids := make([]string, len(p.Items))
		for i, it := range p.Items {
			ids[i] = it.ProductID.String()
		}
		rows, err := tx.Query(ctx,
			`SELECT id, sku, name, unit_price_cents, currency, is_active
			 FROM products WHERE tenant_id = $1 AND id = ANY($2::uuid[])`,
			tenantID, ids,
		)
		if err != nil {
			return fmt.Errorf("load products: %w", err)
		}
		defer rows.Close()

		products := make(map[uuid.UUID]productSnapshot, len(ids))
		for rows.Next() {
			var p productSnapshot
			if err := rows.Scan(&p.ID, &p.SKU, &p.Name, &p.PriceCents, &p.Currency, &p.Active); err != nil {
				return err
			}
			products[p.ID] = p
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(products) != len(p.Items) {
			return fmt.Errorf("%w: one or more products were not found", ErrNotFound)
		}

		items := make([]Item, 0, len(p.Items))
		var subtotal int64
		currency := ""
		for _, requested := range p.Items {
			product := products[requested.ProductID]
			if !product.Active {
				return fmt.Errorf("%w: product %s is inactive", ErrConflict, product.SKU)
			}
			if currency == "" {
				currency = product.Currency
			} else if currency != product.Currency {
				return fmt.Errorf("%w: all order items must use the same currency", ErrValidation)
			}
			if product.PriceCents > 0 && int64(requested.Quantity) > (1<<62)/product.PriceCents {
				return fmt.Errorf("%w: line total is too large", ErrValidation)
			}
			lineTotal := product.PriceCents * int64(requested.Quantity)
			subtotal += lineTotal
			items = append(items, Item{
				ProductID:      product.ID,
				SKU:            product.SKU,
				Name:           product.Name,
				Quantity:       requested.Quantity,
				UnitPriceCents: product.PriceCents,
				TotalCents:     lineTotal,
			})
		}

		address, err := json.Marshal(p.ShippingAddress)
		if err != nil {
			return fmt.Errorf("marshal shipping address: %w", err)
		}

		var status string
		err = tx.QueryRow(ctx,
			`INSERT INTO orders
			    (tenant_id, order_number, warehouse_id, customer_name, customer_email,
			     shipping_address, currency, subtotal_cents, total_cents, notes, created_by)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8, $9, $10)
			 RETURNING id, status, version, created_at, updated_at`,
			tenantID, orderNumber, p.WarehouseID, p.CustomerName, p.CustomerEmail,
			address, currency, subtotal, p.Notes, userID,
		).Scan(&out.ID, &status, &out.Version, &out.CreatedAt, &out.UpdatedAt)
		if err != nil {
			return fmt.Errorf("insert order: %w", err)
		}

		for i := range items {
			err := tx.QueryRow(ctx,
				`INSERT INTO order_items
				    (tenant_id, order_id, product_id, sku, name, quantity, unit_price_cents, total_cents)
				 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
				 RETURNING id`,
				tenantID, out.ID, items[i].ProductID, items[i].SKU, items[i].Name,
				items[i].Quantity, items[i].UnitPriceCents, items[i].TotalCents,
			).Scan(&items[i].ID)
			if err != nil {
				return fmt.Errorf("insert order item: %w", err)
			}
		}

		var eventID int64
		var eventAt time.Time
		err = tx.QueryRow(ctx,
			`INSERT INTO order_events (tenant_id, order_id, from_status, to_status, actor_id, note)
			 VALUES ($1, $2, NULL, 'pending', $3, 'order created')
			 RETURNING id, created_at`,
			tenantID, out.ID, userID,
		).Scan(&eventID, &eventAt)
		if err != nil {
			return fmt.Errorf("insert order event: %w", err)
		}

		out = Order{
			ID:              out.ID,
			OrderNumber:     orderNumber,
			Status:          StatusPending,
			WarehouseID:     p.WarehouseID,
			WarehouseName:   warehouseName,
			CustomerName:    p.CustomerName,
			CustomerEmail:   p.CustomerEmail,
			ShippingAddress: p.ShippingAddress,
			Currency:        currency,
			SubtotalCents:   subtotal,
			TotalCents:      subtotal,
			Notes:           p.Notes,
			Version:         out.Version,
			Items:           items,
			Events: []Event{{
				ID: eventID, FromStatus: nil, ToStatus: StatusPending,
				ActorID: &userID, Note: "order created", CreatedAt: eventAt,
			}},
			CreatedAt: out.CreatedAt,
			UpdatedAt: out.UpdatedAt,
		}
		if err := outbox.Insert(ctx, tx, outbox.Event{
			TenantID: tenantID, AggregateType: "order", AggregateID: out.ID,
			EventType: "order.created", Payload: orderEventPayload(out),
		}); err != nil {
			return err
		}
		if err := audit.Write(ctx, tx, tenantID, userID, "orders.created", "order", out.ID, map[string]any{
			"order_number": out.OrderNumber, "total_cents": out.TotalCents, "currency": out.Currency,
		}); err != nil {
			return err
		}

		if key != "" {
			body, err := json.Marshal(out)
			if err != nil {
				return fmt.Errorf("marshal idempotent response: %w", err)
			}
			if err := idempotency.Complete(ctx, tx, tenantID, key, 201, body); err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "orders_tenant_id_order_number_key" {
			return Order{}, replayed, ErrOrderNumberConflict
		}
	}
	return out, replayed, err
}

func (s *Store) Get(ctx context.Context, tenantID, userID, id uuid.UUID) (Order, error) {
	var out Order
	err := s.db.InTenantTx(ctx, tenantID, userID, func(tx pgx.Tx) error {
		found, err := loadOrder(ctx, tx, tenantID, id, false)
		if err != nil {
			return err
		}
		out = found
		return nil
	})
	return out, err
}

func (s *Store) List(ctx context.Context, tenantID, userID uuid.UUID, status, query string, limit, offset int) ([]Summary, int64, error) {
	var out []Summary
	var total int64

	err := s.db.InTenantTx(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM orders o
			 WHERE o.tenant_id = $1
			   AND ($2 = '' OR o.status::text = $2)
			   AND ($3 = '' OR o.order_number ILIKE '%' || $3 || '%'
			        OR o.customer_name ILIKE '%' || $3 || '%'
			        OR o.customer_email ILIKE '%' || $3 || '%')`,
			tenantID, status, query,
		).Scan(&total); err != nil {
			return err
		}

		rows, err := tx.Query(ctx,
			`SELECT o.id, o.order_number, o.status, o.customer_name, o.customer_email,
			        o.currency, o.total_cents,
			        (SELECT count(*) FROM order_items oi WHERE oi.tenant_id = o.tenant_id AND oi.order_id = o.id),
			        o.created_at
			 FROM orders o
			 WHERE o.tenant_id = $1
			   AND ($2 = '' OR o.status::text = $2)
			   AND ($3 = '' OR o.order_number ILIKE '%' || $3 || '%'
			        OR o.customer_name ILIKE '%' || $3 || '%'
			        OR o.customer_email ILIKE '%' || $3 || '%')
			 ORDER BY o.created_at DESC, o.id
			 LIMIT $4 OFFSET $5`,
			tenantID, status, query, limit, offset,
		)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var s Summary
			var st string
			if err := rows.Scan(&s.ID, &s.OrderNumber, &st, &s.CustomerName, &s.CustomerEmail,
				&s.Currency, &s.TotalCents, &s.ItemCount, &s.CreatedAt); err != nil {
				return err
			}
			s.Status = Status(st)
			out = append(out, s)
		}
		return rows.Err()
	})

	return out, total, err
}

func (s *Store) Confirm(ctx context.Context, tenantID, userID, id uuid.UUID) (Order, error) {
	var out Order
	err := s.db.InTenantTx(ctx, tenantID, userID, func(tx pgx.Tx) error {
		order, err := loadOrder(ctx, tx, tenantID, id, true)
		if err != nil {
			return err
		}
		switch order.Status {
		case StatusConfirmed, StatusProcessing, StatusCompleted:
			out = order
			return nil
		case StatusCancelled:
			return fmt.Errorf("%w: a cancelled order cannot be confirmed", ErrConflict)
		}

		items := append([]Item(nil), order.Items...)
		sort.Slice(items, func(i, j int) bool {
			return items[i].ProductID.String() < items[j].ProductID.String()
		})

		for _, item := range items {
			var onHand, reserved int
			err := tx.QueryRow(ctx,
				`SELECT on_hand, reserved FROM stock_levels
				 WHERE tenant_id = $1 AND warehouse_id = $2 AND product_id = $3
				 FOR UPDATE`,
				tenantID, order.WarehouseID, item.ProductID,
			).Scan(&onHand, &reserved)
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: no stock record for %s at this warehouse", ErrInsufficientStock, item.SKU)
			}
			if err != nil {
				return fmt.Errorf("lock stock for %s: %w", item.SKU, err)
			}
			if onHand-reserved < item.Quantity {
				return fmt.Errorf("%w: %s needs %d but only %d is available", ErrInsufficientStock, item.SKU, item.Quantity, onHand-reserved)
			}

			tag, err := tx.Exec(ctx,
				`UPDATE stock_levels
				 SET reserved = reserved + $4, version = version + 1
				 WHERE tenant_id = $1 AND warehouse_id = $2 AND product_id = $3
				   AND reserved + $4 <= on_hand`,
				tenantID, order.WarehouseID, item.ProductID, item.Quantity,
			)
			if err != nil {
				return fmt.Errorf("reserve stock for %s: %w", item.SKU, err)
			}
			if tag.RowsAffected() != 1 {
				return fmt.Errorf("%w: %s could not be reserved", ErrInsufficientStock, item.SKU)
			}

			if _, err := tx.Exec(ctx,
				`INSERT INTO stock_movements
				    (tenant_id, warehouse_id, product_id, kind, quantity, reason, order_id, actor_id)
				 VALUES ($1, $2, $3, 'reservation', $4, 'order confirmed', $5, $6)`,
				tenantID, order.WarehouseID, item.ProductID, item.Quantity, order.ID, userID,
			); err != nil {
				return fmt.Errorf("insert reservation movement: %w", err)
			}

			if _, err := tx.Exec(ctx,
				`INSERT INTO stock_reservations
				    (tenant_id, warehouse_id, product_id, order_id, order_item_id, quantity)
				 VALUES ($1, $2, $3, $4, $5, $6)`,
				tenantID, order.WarehouseID, item.ProductID, order.ID, item.ID, item.Quantity,
			); err != nil {
				return fmt.Errorf("insert stock reservation: %w", err)
			}
		}

		if _, err := tx.Exec(ctx,
			`UPDATE orders
			 SET status = 'confirmed', confirmed_at = now(), version = version + 1
			 WHERE tenant_id = $1 AND id = $2 AND status = 'pending'`,
			tenantID, order.ID,
		); err != nil {
			return fmt.Errorf("confirm order: %w", err)
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO order_events (tenant_id, order_id, from_status, to_status, actor_id, note)
			 VALUES ($1, $2, 'pending', 'confirmed', $3, 'stock reserved and order confirmed')`,
			tenantID, order.ID, userID,
		); err != nil {
			return fmt.Errorf("insert confirm event: %w", err)
		}

		out, err = loadOrder(ctx, tx, tenantID, order.ID, false)
		if err != nil {
			return err
		}

		if err := outbox.Insert(ctx, tx, outbox.Event{
			TenantID: tenantID, AggregateType: "order", AggregateID: out.ID,
			EventType: "order.confirmed", Payload: orderEventPayload(out),
		}); err != nil {
			return err
		}
		return audit.Write(ctx, tx, tenantID, userID, "orders.confirmed", "order", out.ID, map[string]any{
			"order_number": out.OrderNumber,
		})
	})
	return out, err
}

func (s *Store) Cancel(ctx context.Context, tenantID, userID, id uuid.UUID, reason string) (Order, error) {
	var out Order

	err := s.db.InTenantTx(ctx, tenantID, userID, func(tx pgx.Tx) error {
		order, err := loadOrder(ctx, tx, tenantID, id, true)
		if err != nil {
			return err
		}
		if order.Status == StatusCancelled {
			out = order
			return nil
		}
		if order.Status != StatusPending && order.Status != StatusConfirmed {
			return fmt.Errorf("%w: orders in %s status cannot be cancelled", ErrConflict, order.Status)
		}

		// A shipment may already exist. Before dispatch we can cancel it with the
		// order; once stock has physically left the warehouse the order cannot be
		// cancelled. This check lives in the same transaction as the order update.
		var shipmentID uuid.UUID
		var shipmentStatus, trackingNumber string
		err = tx.QueryRow(ctx,
			`SELECT id, status::text, tracking_number FROM shipments
			 WHERE tenant_id = $1 AND order_id = $2
			 FOR UPDATE`,
			tenantID, order.ID,
		).Scan(&shipmentID, &shipmentStatus, &trackingNumber)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("lock shipment: %w", err)
		}
		if err == nil {
			switch shipmentStatus {
			case "pending", "assigned":
				if _, err := tx.Exec(ctx,
					`UPDATE shipments
					 SET status = 'cancelled', cancelled_at = now(), version = version + 1
					 WHERE tenant_id = $1 AND id = $2`,
					tenantID, shipmentID,
				); err != nil {
					return fmt.Errorf("cancel shipment: %w", err)
				}
				if _, err := tx.Exec(ctx,
					`INSERT INTO shipment_events (tenant_id, shipment_id, status, description, actor_id)
					 VALUES ($1, $2, 'cancelled', 'order cancelled', $3)`,
					tenantID, shipmentID, userID,
				); err != nil {
					return fmt.Errorf("insert shipment cancel event: %w", err)
				}
				if err := outbox.Insert(ctx, tx, outbox.Event{
					TenantID: tenantID, AggregateType: "shipment", AggregateID: shipmentID,
					EventType: "shipment.status_changed", Payload: map[string]any{
						"shipment_id": shipmentID, "tracking_number": trackingNumber,
						"order_id": order.ID, "status": "cancelled",
						"description": "order cancelled", "occurred_at": time.Now().UTC(),
					},
				}); err != nil {
					return err
				}
				if err := audit.Write(ctx, tx, tenantID, userID, "shipments.cancelled", "shipment", shipmentID, map[string]any{
					"order_id": order.ID, "reason": "order cancelled",
				}); err != nil {
					return err
				}
			default:
				return fmt.Errorf("%w: order has a shipment already in progress (%s)", ErrConflict, shipmentStatus)
			}
		}

		if order.Status == StatusConfirmed {
			rows, err := tx.Query(ctx,
				`SELECT id, product_id, quantity FROM stock_reservations
				 WHERE tenant_id = $1 AND order_id = $2 AND status = 'held'
				 ORDER BY product_id
				 FOR UPDATE`,
				tenantID, order.ID,
			)
			if err != nil {
				return fmt.Errorf("load reservations: %w", err)
			}

			type reservation struct {
				ID        uuid.UUID
				ProductID uuid.UUID
				Quantity  int
			}
			var reservations []reservation
			for rows.Next() {
				var r reservation
				if err := rows.Scan(&r.ID, &r.ProductID, &r.Quantity); err != nil {
					rows.Close()
					return err
				}
				reservations = append(reservations, r)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()

			for _, r := range reservations {
				var onHand, reserved int
				err := tx.QueryRow(ctx,
					`SELECT on_hand, reserved FROM stock_levels
					 WHERE tenant_id = $1 AND warehouse_id = $2 AND product_id = $3
					 FOR UPDATE`,
					tenantID, order.WarehouseID, r.ProductID,
				).Scan(&onHand, &reserved)
				if err != nil {
					return fmt.Errorf("lock stock for release: %w", err)
				}
				if reserved < r.Quantity {
					return fmt.Errorf("%w: reservation ledger is inconsistent", ErrConflict)
				}

				if _, err := tx.Exec(ctx,
					`UPDATE stock_levels
					 SET reserved = reserved - $4, version = version + 1
					 WHERE tenant_id = $1 AND warehouse_id = $2 AND product_id = $3`,
					tenantID, order.WarehouseID, r.ProductID, r.Quantity,
				); err != nil {
					return fmt.Errorf("release stock: %w", err)
				}

				if _, err := tx.Exec(ctx,
					`INSERT INTO stock_movements
					    (tenant_id, warehouse_id, product_id, kind, quantity, reason, order_id, reservation_id, actor_id)
					 VALUES ($1, $2, $3, 'release', $4, 'order cancelled', $5, $6, $7)`,
					tenantID, order.WarehouseID, r.ProductID, -r.Quantity, order.ID, r.ID, userID,
				); err != nil {
					return fmt.Errorf("insert release movement: %w", err)
				}

				if _, err := tx.Exec(ctx,
					`UPDATE stock_reservations
					 SET status = 'released', released_at = now()
					 WHERE tenant_id = $1 AND id = $2`,
					tenantID, r.ID,
				); err != nil {
					return fmt.Errorf("mark reservation released: %w", err)
				}
			}
		}

		if _, err := tx.Exec(ctx,
			`UPDATE orders
			 SET status = 'cancelled', cancelled_at = now(), version = version + 1
			 WHERE tenant_id = $1 AND id = $2`,
			tenantID, order.ID,
		); err != nil {
			return fmt.Errorf("cancel order: %w", err)
		}

		note := reason
		if note == "" {
			note = "order cancelled"
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO order_events (tenant_id, order_id, from_status, to_status, actor_id, note)
			 VALUES ($1, $2, $3, 'cancelled', $4, $5)`,
			tenantID, order.ID, string(order.Status), userID, note,
		); err != nil {
			return fmt.Errorf("insert cancel event: %w", err)
		}

		out, err = loadOrder(ctx, tx, tenantID, order.ID, false)
		if err != nil {
			return err
		}
		if err := outbox.Insert(ctx, tx, outbox.Event{
			TenantID: tenantID, AggregateType: "order", AggregateID: out.ID,
			EventType: "order.cancelled", Payload: orderEventPayload(out),
		}); err != nil {
			return err
		}
		return audit.Write(ctx, tx, tenantID, userID, "orders.cancelled", "order", out.ID, map[string]any{
			"order_number": out.OrderNumber, "reason": note,
		})
	})

	return out, err
}

func loadOrder(ctx context.Context, tx pgx.Tx, tenantID, id uuid.UUID, forUpdate bool) (Order, error) {
	query := `SELECT o.id, o.order_number, o.status, o.warehouse_id, w.name,
	                 o.customer_name, o.customer_email, o.shipping_address,
	                 o.currency, o.subtotal_cents, o.total_cents, o.notes, o.version,
	                 o.confirmed_at, o.cancelled_at, o.completed_at, o.created_at, o.updated_at
	          FROM orders o
	          JOIN warehouses w ON w.tenant_id = o.tenant_id AND w.id = o.warehouse_id
	          WHERE o.tenant_id = $1 AND o.id = $2`
	if forUpdate {
		query += " FOR UPDATE OF o"
	}

	var o Order
	var status string
	var address []byte
	err := tx.QueryRow(ctx, query, tenantID, id).Scan(
		&o.ID, &o.OrderNumber, &status, &o.WarehouseID, &o.WarehouseName,
		&o.CustomerName, &o.CustomerEmail, &address, &o.Currency,
		&o.SubtotalCents, &o.TotalCents, &o.Notes, &o.Version,
		&o.ConfirmedAt, &o.CancelledAt, &o.CompletedAt, &o.CreatedAt, &o.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, fmt.Errorf("%w: order not found", ErrNotFound)
	}
	if err != nil {
		return Order{}, fmt.Errorf("load order: %w", err)
	}
	o.Status = Status(status)
	_ = json.Unmarshal(address, &o.ShippingAddress)

	rows, err := tx.Query(ctx,
		`SELECT id, product_id, sku, name, quantity, unit_price_cents, total_cents
		 FROM order_items WHERE tenant_id = $1 AND order_id = $2 ORDER BY sku, id`,
		tenantID, id,
	)
	if err != nil {
		return Order{}, fmt.Errorf("load order items: %w", err)
	}
	for rows.Next() {
		var item Item
		if err := rows.Scan(&item.ID, &item.ProductID, &item.SKU, &item.Name,
			&item.Quantity, &item.UnitPriceCents, &item.TotalCents); err != nil {
			rows.Close()
			return Order{}, err
		}
		o.Items = append(o.Items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Order{}, err
	}
	rows.Close()

	eventRows, err := tx.Query(ctx,
		`SELECT id, from_status, to_status, actor_id, note, created_at
		 FROM order_events WHERE tenant_id = $1 AND order_id = $2 ORDER BY id`,
		tenantID, id,
	)
	if err != nil {
		return Order{}, fmt.Errorf("load order events: %w", err)
	}
	defer eventRows.Close()

	for eventRows.Next() {
		var e Event
		var from, to *string
		if err := eventRows.Scan(&e.ID, &from, &to, &e.ActorID, &e.Note, &e.CreatedAt); err != nil {
			return Order{}, err
		}
		if from != nil {
			st := Status(*from)
			e.FromStatus = &st
		}
		if to != nil {
			e.ToStatus = Status(*to)
		}
		o.Events = append(o.Events, e)
	}
	if err := eventRows.Err(); err != nil {
		return Order{}, err
	}
	if o.Items == nil {
		o.Items = []Item{}
	}
	if o.Events == nil {
		o.Events = []Event{}
	}
	return o, nil
}

type orderEventItem struct {
	SKU      string `json:"sku"`
	Quantity int    `json:"quantity"`
}

type orderEventBody struct {
	OrderID       uuid.UUID        `json:"order_id"`
	OrderNumber   string           `json:"order_number"`
	Status        Status           `json:"status"`
	WarehouseID   uuid.UUID        `json:"warehouse_id"`
	CustomerEmail string           `json:"customer_email"`
	Currency      string           `json:"currency"`
	TotalCents    int64            `json:"total_cents"`
	Items         []orderEventItem `json:"items"`
}

func orderEventPayload(o Order) orderEventBody {
	items := make([]orderEventItem, 0, len(o.Items))
	for _, item := range o.Items {
		items = append(items, orderEventItem{SKU: item.SKU, Quantity: item.Quantity})
	}
	return orderEventBody{
		OrderID:       o.ID,
		OrderNumber:   o.OrderNumber,
		Status:        o.Status,
		WarehouseID:   o.WarehouseID,
		CustomerEmail: o.CustomerEmail,
		Currency:      o.Currency,
		TotalCents:    o.TotalCents,
		Items:         items,
	}
}
