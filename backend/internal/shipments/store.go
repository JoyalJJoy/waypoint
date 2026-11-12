package shipments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/joyaljoy/waypoint/internal/platform/audit"
	"github.com/joyaljoy/waypoint/internal/platform/database"
	"github.com/joyaljoy/waypoint/internal/platform/outbox"
	"github.com/joyaljoy/waypoint/internal/rbac"
)

type Store struct {
	db *database.DB
}

func NewStore(db *database.DB) *Store {
	return &Store{db: db}
}

type orderSnapshot struct {
	Status          Status
	OrderNumber     string
	CustomerName    string
	CustomerEmail   string
	WarehouseID     uuid.UUID
	WarehouseName   string
	ShippingAddress []byte
	WeightGrams     int
}

func (s *Store) Create(ctx context.Context, tenantID, userID uuid.UUID, p CreateParams, shipmentNumber, trackingNumber string) (Shipment, error) {
	var out Shipment

	err := s.db.InTenantTx(ctx, tenantID, userID, func(tx pgx.Tx) error {
		order, err := loadOrderForShipment(ctx, tx, tenantID, p.OrderID)
		if err != nil {
			return err
		}
		if order.Status != Status("confirmed") {
			return fmt.Errorf("%w: only confirmed orders can be shipped", ErrConflict)
		}

		var existing uuid.UUID
		err = tx.QueryRow(ctx,
			`SELECT id FROM shipments WHERE tenant_id = $1 AND order_id = $2`,
			tenantID, p.OrderID,
		).Scan(&existing)
		if err == nil {
			return fmt.Errorf("%w: order already has shipment %s", ErrConflict, existing)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("check existing shipment: %w", err)
		}

		var status string
		err = tx.QueryRow(ctx,
			`INSERT INTO shipments
			    (tenant_id, shipment_number, order_id, warehouse_id, tracking_number,
			     carrier, service_level, weight_grams, estimated_delivery_at, created_by)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			 RETURNING id, status, version, created_at, updated_at`,
			tenantID, shipmentNumber, p.OrderID, order.WarehouseID, trackingNumber,
			p.Carrier, p.ServiceLevel, order.WeightGrams, p.EstimatedDeliveryAt, userID,
		).Scan(&out.ID, &status, &out.Version, &out.CreatedAt, &out.UpdatedAt)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				switch pgErr.ConstraintName {
				case "shipments_tenant_id_order_id_key":
					return fmt.Errorf("%w: order already has a shipment", ErrConflict)
				case "shipments_tracking_number_key", "shipments_tenant_id_shipment_number_key":
					return ErrNumberConflict
				}
			}
			return fmt.Errorf("insert shipment: %w", err)
		}
		out.Status = Status(status)

		if _, err := tx.Exec(ctx,
			`INSERT INTO shipment_events (tenant_id, shipment_id, status, description, actor_id)
			 VALUES ($1, $2, 'pending', 'shipment created', $3)`,
			tenantID, out.ID, userID,
		); err != nil {
			return fmt.Errorf("insert shipment event: %w", err)
		}

		if err := outbox.Insert(ctx, tx, outbox.Event{
			TenantID: tenantID, AggregateType: "shipment", AggregateID: out.ID,
			EventType: "shipment.created", Payload: statusPayload{
				ShipmentID: out.ID, TrackingNumber: trackingNumber, OrderID: p.OrderID,
				Status: StatusPending, Description: "shipment created", OccurredAt: time.Now().UTC(),
			},
		}); err != nil {
			return err
		}
		if err := audit.Write(ctx, tx, tenantID, userID, "shipments.created", "shipment", out.ID, map[string]any{
			"shipment_number": shipmentNumber,
			"order_id":        p.OrderID,
			"carrier":         p.Carrier,
		}); err != nil {
			return err
		}

		out, err = loadShipment(ctx, tx, tenantID, out.ID, false)
		return err
	})
	return out, err
}

func loadOrderForShipment(ctx context.Context, tx pgx.Tx, tenantID, orderID uuid.UUID) (orderSnapshot, error) {
	var o orderSnapshot
	var status string

	err := tx.QueryRow(ctx,
		`SELECT o.status::text, o.order_number, o.customer_name, o.customer_email,
		        o.warehouse_id, w.name, o.shipping_address,
		        COALESCE((
		            SELECT SUM(COALESCE(p.weight_grams, 0) * oi.quantity)
		            FROM order_items oi
		            JOIN products p ON p.tenant_id = oi.tenant_id AND p.id = oi.product_id
		            WHERE oi.tenant_id = o.tenant_id AND oi.order_id = o.id
		        ), 0)
		 FROM orders o
		 JOIN warehouses w ON w.tenant_id = o.tenant_id AND w.id = o.warehouse_id
		 WHERE o.tenant_id = $1 AND o.id = $2`,
		tenantID, orderID,
	).Scan(&status, &o.OrderNumber, &o.CustomerName, &o.CustomerEmail,
		&o.WarehouseID, &o.WarehouseName, &o.ShippingAddress, &o.WeightGrams)
	if errors.Is(err, pgx.ErrNoRows) {
		return orderSnapshot{}, fmt.Errorf("%w: order not found", ErrNotFound)
	}
	if err != nil {
		return orderSnapshot{}, fmt.Errorf("load order: %w", err)
	}
	o.Status = Status(status)
	return o, nil
}

func (s *Store) Get(ctx context.Context, tenantID, userID, id uuid.UUID) (Shipment, error) {
	var out Shipment
	err := s.db.InTenantTx(ctx, tenantID, userID, func(tx pgx.Tx) error {
		found, err := loadShipment(ctx, tx, tenantID, id, false)
		if err != nil {
			return err
		}
		out = found
		return nil
	})
	return out, err
}

func (s *Store) List(ctx context.Context, tenantID, userID uuid.UUID, status, query string, limit, offset int) ([]ShipmentSummary, int64, error) {
	var out []ShipmentSummary
	var total int64

	err := s.db.InTenantTx(ctx, tenantID, userID, func(tx pgx.Tx) error {
		filter := `s.tenant_id = $1
		    AND ($2 = '' OR s.status::text = $2)
		    AND ($3 = '' OR s.shipment_number ILIKE '%' || $3 || '%'
		         OR s.tracking_number ILIKE '%' || $3 || '%'
		         OR o.order_number ILIKE '%' || $3 || '%'
		         OR o.customer_name ILIKE '%' || $3 || '%')`

		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM shipments s
			 JOIN orders o ON o.tenant_id = s.tenant_id AND o.id = s.order_id
			 WHERE `+filter, tenantID, status, query,
		).Scan(&total); err != nil {
			return err
		}

		rows, err := tx.Query(ctx,
			`SELECT s.id, s.shipment_number, s.tracking_number, s.status::text,
			        s.order_id, o.order_number, o.customer_name,
			        s.carrier, s.service_level, s.assigned_agent_id, COALESCE(u.name, ''),
			        s.created_at, s.delivered_at
			 FROM shipments s
			 JOIN orders o ON o.tenant_id = s.tenant_id AND o.id = s.order_id
			 LEFT JOIN users u ON u.id = s.assigned_agent_id
			 WHERE `+filter+`
			 ORDER BY s.created_at DESC, s.id
			 LIMIT $4 OFFSET $5`,
			tenantID, status, query, limit, offset,
		)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var item ShipmentSummary
			var st string
			var agentID uuid.NullUUID
			if err := rows.Scan(&item.ID, &item.ShipmentNumber, &item.TrackingNumber, &st,
				&item.OrderID, &item.OrderNumber, &item.CustomerName,
				&item.Carrier, &item.ServiceLevel, &agentID, &item.AgentName,
				&item.CreatedAt, &item.DeliveredAt); err != nil {
				return err
			}
			if agentID.Valid {
				id := agentID.UUID
				item.AgentID = &id
			}
			item.Status = Status(st)
			out = append(out, item)
		}
		return rows.Err()
	})

	return out, total, err
}

func (s *Store) Assign(ctx context.Context, tenantID, userID, shipmentID, agentID uuid.UUID) (Shipment, error) {
	var out Shipment

	err := s.db.InTenantTx(ctx, tenantID, userID, func(tx pgx.Tx) error {
		var status string
		var currentAgent uuid.NullUUID
		err := tx.QueryRow(ctx,
			`SELECT status::text, assigned_agent_id FROM shipments
			 WHERE tenant_id = $1 AND id = $2 FOR UPDATE`,
			tenantID, shipmentID,
		).Scan(&status, &currentAgent)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: shipment not found", ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("lock shipment: %w", err)
		}

		st := Status(status)
		switch st {
		case StatusPending, StatusAssigned:
		default:
			return fmt.Errorf("%w: shipments in %s status cannot be assigned", ErrConflict, st)
		}

		var agent Agent
		err = tx.QueryRow(ctx,
			`SELECT u.id, u.name, u.email
			 FROM memberships m
			 JOIN users u ON u.id = m.user_id
			 WHERE m.tenant_id = $1 AND m.user_id = $2 AND m.role = 'delivery_agent'`,
			tenantID, agentID,
		).Scan(&agent.ID, &agent.Name, &agent.Email)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: agent is not a delivery agent in this tenant", ErrValidation)
		}
		if err != nil {
			return fmt.Errorf("load agent: %w", err)
		}

		if currentAgent.Valid && currentAgent.UUID == agentID && st == StatusAssigned {
			out, err = loadShipment(ctx, tx, tenantID, shipmentID, false)
			return err
		}

		if _, err := tx.Exec(ctx,
			`UPDATE shipments
			 SET assigned_agent_id = $3, status = 'assigned', version = version + 1
			 WHERE tenant_id = $1 AND id = $2`,
			tenantID, shipmentID, agentID,
		); err != nil {
			return fmt.Errorf("assign shipment: %w", err)
		}

		description := "assigned to " + agent.Name
		if _, err := tx.Exec(ctx,
			`INSERT INTO shipment_events (tenant_id, shipment_id, status, description, actor_id)
			 VALUES ($1, $2, 'assigned', $3, $4)`,
			tenantID, shipmentID, description, userID,
		); err != nil {
			return fmt.Errorf("insert assign event: %w", err)
		}

		var trackingNumber string
		var orderID uuid.UUID
		if err := tx.QueryRow(ctx,
			`SELECT tracking_number, order_id FROM shipments WHERE tenant_id = $1 AND id = $2`,
			tenantID, shipmentID,
		).Scan(&trackingNumber, &orderID); err != nil {
			return fmt.Errorf("load shipment identifiers: %w", err)
		}

		if err := outbox.Insert(ctx, tx, outbox.Event{
			TenantID: tenantID, AggregateType: "shipment", AggregateID: shipmentID,
			EventType: "shipment.assigned", Payload: statusPayload{
				ShipmentID: shipmentID, TrackingNumber: trackingNumber, OrderID: orderID,
				Status: StatusAssigned, Description: description, OccurredAt: time.Now().UTC(),
			},
		}); err != nil {
			return err
		}
		if err := audit.Write(ctx, tx, tenantID, userID, "shipments.assigned", "shipment", shipmentID, map[string]any{
			"agent_id": agentID,
		}); err != nil {
			return err
		}

		out, err = loadShipment(ctx, tx, tenantID, shipmentID, false)
		return err
	})

	return out, err
}

func (s *Store) Transition(ctx context.Context, tenantID, userID uuid.UUID, role rbac.Role, shipmentID uuid.UUID, p TransitionParams) (Shipment, error) {
	var out Shipment

	err := s.db.InTenantTx(ctx, tenantID, userID, func(tx pgx.Tx) error {
		var status, trackingNumber, orderStatus, orderNumber string
		var orderID uuid.UUID
		var agentID uuid.NullUUID

		err := tx.QueryRow(ctx,
			`SELECT s.status::text, s.tracking_number, s.order_id, o.status::text,
			        o.order_number, s.assigned_agent_id
			 FROM shipments s
			 JOIN orders o ON o.tenant_id = s.tenant_id AND o.id = s.order_id
			 WHERE s.tenant_id = $1 AND s.id = $2
			 FOR UPDATE OF s`,
			tenantID, shipmentID,
		).Scan(&status, &trackingNumber, &orderID, &orderStatus, &orderNumber, &agentID)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: shipment not found", ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("lock shipment: %w", err)
		}

		current := Status(status)
		if role == rbac.RoleDeliveryAgent {
			if !agentID.Valid || agentID.UUID != userID {
				return fmt.Errorf("%w: this shipment is not assigned to you", ErrForbidden)
			}
		}

		// Repeating the current status is a no-op. This makes retries from a
		// flaky mobile connection safe without duplicating tracking events.
		if p.Status == current {
			out, err = loadShipment(ctx, tx, tenantID, shipmentID, false)
			return err
		}
		if !CanTransition(current, p.Status) {
			return fmt.Errorf("%w: cannot move shipment from %s to %s", ErrConflict, current, p.Status)
		}

		switch p.Status {
		case StatusPickedUp:
			if err := pickUpStock(ctx, tx, tenantID, userID, orderID, orderNumber); err != nil {
				return err
			}
		case StatusDelivered:
			if err := completeOrder(ctx, tx, tenantID, userID, orderID, orderNumber); err != nil {
				return err
			}
		}

		if _, err := tx.Exec(ctx,
			`UPDATE shipments
			 SET status = $3::shipment_status, version = version + 1,
			     picked_up_at = CASE WHEN $3::text = 'picked_up' THEN now() ELSE picked_up_at END,
			     delivered_at = CASE WHEN $3::text = 'delivered' THEN now() ELSE delivered_at END,
			     cancelled_at = CASE WHEN $3::text = 'cancelled' THEN now() ELSE cancelled_at END
			 WHERE tenant_id = $1 AND id = $2`,
			tenantID, shipmentID, p.Status,
		); err != nil {
			return fmt.Errorf("update shipment status: %w", err)
		}

		description := p.Note
		if description == "" {
			description = defaultDescription(p.Status)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO shipment_events
			    (tenant_id, shipment_id, status, description, latitude, longitude, actor_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			tenantID, shipmentID, p.Status, description, p.Latitude, p.Longitude, userID,
		); err != nil {
			return fmt.Errorf("insert shipment event: %w", err)
		}

		occurredAt := time.Now().UTC()
		if err := outbox.Insert(ctx, tx, outbox.Event{
			TenantID: tenantID, AggregateType: "shipment", AggregateID: shipmentID,
			EventType: "shipment.status_changed", Payload: statusPayload{
				ShipmentID: shipmentID, TrackingNumber: trackingNumber, OrderID: orderID,
				Status: p.Status, Description: description, OccurredAt: occurredAt,
				Latitude: p.Latitude, Longitude: p.Longitude,
			},
		}); err != nil {
			return err
		}
		if err := audit.Write(ctx, tx, tenantID, userID, "shipments.status_changed", "shipment", shipmentID, map[string]any{
			"from": current, "to": p.Status, "order_id": orderID,
		}); err != nil {
			return err
		}

		out, err = loadShipment(ctx, tx, tenantID, shipmentID, false)
		return err
	})

	return out, err
}

func pickUpStock(ctx context.Context, tx pgx.Tx, tenantID, userID, orderID uuid.UUID, orderNumber string) error {
	rows, err := tx.Query(ctx,
		`SELECT id, product_id, quantity FROM stock_reservations
		 WHERE tenant_id = $1 AND order_id = $2 AND status = 'held'
		 ORDER BY product_id
		 FOR UPDATE`,
		tenantID, orderID,
	)
	if err != nil {
		return fmt.Errorf("lock reservations: %w", err)
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

	if len(reservations) == 0 {
		return fmt.Errorf("%w: order has no held stock reservations", ErrConflict)
	}

	var warehouseID uuid.UUID
	if err := tx.QueryRow(ctx,
		`SELECT warehouse_id FROM orders WHERE tenant_id = $1 AND id = $2`,
		tenantID, orderID,
	).Scan(&warehouseID); err != nil {
		return fmt.Errorf("load order warehouse: %w", err)
	}

	for _, r := range reservations {
		var onHand, reserved int
		err := tx.QueryRow(ctx,
			`SELECT on_hand, reserved FROM stock_levels
			 WHERE tenant_id = $1 AND warehouse_id = $2 AND product_id = $3
			 FOR UPDATE`,
			tenantID, warehouseID, r.ProductID,
		).Scan(&onHand, &reserved)
		if err != nil {
			return fmt.Errorf("lock stock for pickup: %w", err)
		}
		if reserved < r.Quantity || onHand < r.Quantity {
			return fmt.Errorf("%w: stock ledger is inconsistent for pickup", ErrConflict)
		}

		if _, err := tx.Exec(ctx,
			`UPDATE stock_levels
			 SET on_hand = on_hand - $4, reserved = reserved - $4, version = version + 1
			 WHERE tenant_id = $1 AND warehouse_id = $2 AND product_id = $3`,
			tenantID, warehouseID, r.ProductID, r.Quantity,
		); err != nil {
			return fmt.Errorf("deduct stock at pickup: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO stock_movements
			    (tenant_id, warehouse_id, product_id, kind, quantity, reason, order_id, reservation_id, actor_id)
			 VALUES ($1, $2, $3, 'pick', $4, 'shipment picked up', $5, $6, $7)`,
			tenantID, warehouseID, r.ProductID, -r.Quantity, orderID, r.ID, userID,
		); err != nil {
			return fmt.Errorf("insert pick movement: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE stock_reservations SET status = 'fulfilled', fulfilled_at = now()
			 WHERE tenant_id = $1 AND id = $2`,
			tenantID, r.ID,
		); err != nil {
			return fmt.Errorf("fulfill reservation: %w", err)
		}
	}

	tag, err := tx.Exec(ctx,
		`UPDATE orders SET status = 'processing', version = version + 1
		 WHERE tenant_id = $1 AND id = $2 AND status = 'confirmed'`,
		tenantID, orderID,
	)
	if err != nil {
		return fmt.Errorf("advance order to processing: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("%w: order must be confirmed before pickup", ErrConflict)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO order_events (tenant_id, order_id, from_status, to_status, actor_id, note)
		 VALUES ($1, $2, 'confirmed', 'processing', $3, 'shipment picked up')`,
		tenantID, orderID, userID,
	); err != nil {
		return fmt.Errorf("insert order processing event: %w", err)
	}

	return outbox.Insert(ctx, tx, outbox.Event{
		TenantID: tenantID, AggregateType: "order", AggregateID: orderID,
		EventType: "order.processing", Payload: map[string]any{
			"order_id": orderID, "order_number": orderNumber, "status": "processing",
		},
	})
}

func completeOrder(ctx context.Context, tx pgx.Tx, tenantID, userID, orderID uuid.UUID, orderNumber string) error {
	tag, err := tx.Exec(ctx,
		`UPDATE orders SET status = 'completed', completed_at = now(), version = version + 1
		 WHERE tenant_id = $1 AND id = $2 AND status = 'processing'`,
		tenantID, orderID,
	)
	if err != nil {
		return fmt.Errorf("complete order: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("%w: order must be processing before delivery", ErrConflict)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO order_events (tenant_id, order_id, from_status, to_status, actor_id, note)
		 VALUES ($1, $2, 'processing', 'completed', $3, 'shipment delivered')`,
		tenantID, orderID, userID,
	); err != nil {
		return fmt.Errorf("insert order completed event: %w", err)
	}

	return outbox.Insert(ctx, tx, outbox.Event{
		TenantID: tenantID, AggregateType: "order", AggregateID: orderID,
		EventType: "order.completed", Payload: map[string]any{
			"order_id": orderID, "order_number": orderNumber, "status": "completed",
		},
	})
}

func (s *Store) PublicTracking(ctx context.Context, trackingNumber string) (PublicTracking, error) {
	var raw []byte
	err := s.db.Pool.QueryRow(ctx, `SELECT public_tracking($1)`, trackingNumber).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return PublicTracking{}, fmt.Errorf("%w: tracking number not found", ErrNotFound)
	}
	if err != nil {
		return PublicTracking{}, fmt.Errorf("load public tracking: %w", err)
	}
	if len(raw) == 0 {
		return PublicTracking{}, fmt.Errorf("%w: tracking number not found", ErrNotFound)
	}

	var out PublicTracking
	if err := json.Unmarshal(raw, &out); err != nil {
		return PublicTracking{}, fmt.Errorf("decode public tracking: %w", err)
	}
	if out.Events == nil {
		out.Events = []PublicEvent{}
	}
	return out, nil
}

func (s *Store) ListAgents(ctx context.Context, tenantID, userID uuid.UUID) ([]Agent, error) {
	var out []Agent
	err := s.db.InTenantTx(ctx, tenantID, userID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT u.id, u.name, u.email
			 FROM memberships m
			 JOIN users u ON u.id = m.user_id
			 WHERE m.tenant_id = $1 AND m.role = 'delivery_agent'
			 ORDER BY u.name, u.id`,
			tenantID,
		)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a Agent
			if err := rows.Scan(&a.ID, &a.Name, &a.Email); err != nil {
				return err
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, err
}

func loadShipment(ctx context.Context, tx pgx.Tx, tenantID, id uuid.UUID, forUpdate bool) (Shipment, error) {
	query := `SELECT s.id, s.shipment_number, s.tracking_number, s.status::text,
	                 s.order_id, o.order_number, o.customer_name, o.customer_email,
	                 s.warehouse_id, w.name, s.carrier, s.service_level, s.weight_grams,
	                 s.assigned_agent_id, u.name, u.email,
	                 s.estimated_delivery_at, s.picked_up_at, s.delivered_at, s.cancelled_at,
	                 s.version, o.shipping_address, s.created_at, s.updated_at
	          FROM shipments s
	          JOIN orders o ON o.tenant_id = s.tenant_id AND o.id = s.order_id
	          JOIN warehouses w ON w.tenant_id = s.tenant_id AND w.id = s.warehouse_id
	          LEFT JOIN users u ON u.id = s.assigned_agent_id
	          WHERE s.tenant_id = $1 AND s.id = $2`
	if forUpdate {
		query += " FOR UPDATE OF s"
	}

	var out Shipment
	var status string
	var agentID uuid.NullUUID
	var agentName, agentEmail *string
	var address []byte

	err := tx.QueryRow(ctx, query, tenantID, id).Scan(
		&out.ID, &out.ShipmentNumber, &out.TrackingNumber, &status,
		&out.OrderID, &out.OrderNumber, &out.CustomerName, &out.CustomerEmail,
		&out.WarehouseID, &out.WarehouseName, &out.Carrier, &out.ServiceLevel, &out.WeightGrams,
		&agentID, &agentName, &agentEmail,
		&out.EstimatedDeliveryAt, &out.PickedUpAt, &out.DeliveredAt, &out.CancelledAt,
		&out.Version, &address, &out.CreatedAt, &out.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Shipment{}, fmt.Errorf("%w: shipment not found", ErrNotFound)
	}
	if err != nil {
		return Shipment{}, fmt.Errorf("load shipment: %w", err)
	}
	out.Status = Status(status)
	out.Destination = parseDestination(address)
	if agentID.Valid && agentName != nil && agentEmail != nil {
		out.Agent = &Agent{ID: agentID.UUID, Name: *agentName, Email: *agentEmail}
	}

	rows, err := tx.Query(ctx,
		`SELECT id, status::text, description, latitude, longitude, actor_id, created_at
		 FROM shipment_events WHERE tenant_id = $1 AND shipment_id = $2 ORDER BY id`,
		tenantID, id,
	)
	if err != nil {
		return Shipment{}, fmt.Errorf("load shipment events: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e TrackingEvent
		var st string
		if err := rows.Scan(&e.ID, &st, &e.Description, &e.Latitude, &e.Longitude, &e.ActorID, &e.CreatedAt); err != nil {
			return Shipment{}, err
		}
		e.Status = Status(st)
		out.Events = append(out.Events, e)
	}
	if err := rows.Err(); err != nil {
		return Shipment{}, err
	}
	if out.Events == nil {
		out.Events = []TrackingEvent{}
	}
	return out, nil
}

func parseDestination(address []byte) Destination {
	var raw struct {
		City    string `json:"city"`
		Region  string `json:"region"`
		Country string `json:"country"`
	}
	_ = json.Unmarshal(address, &raw)
	return Destination{City: raw.City, Region: raw.Region, Country: raw.Country}
}

type statusPayload struct {
	ShipmentID     uuid.UUID `json:"shipment_id"`
	TrackingNumber string    `json:"tracking_number"`
	OrderID        uuid.UUID `json:"order_id"`
	Status         Status    `json:"status"`
	Description    string    `json:"description"`
	OccurredAt     time.Time `json:"occurred_at"`
	Latitude       *float64  `json:"latitude,omitempty"`
	Longitude      *float64  `json:"longitude,omitempty"`
}

func defaultDescription(s Status) string {
	switch s {
	case StatusAssigned:
		return "agent assigned"
	case StatusPickedUp:
		return "shipment picked up"
	case StatusInTransit:
		return "in transit"
	case StatusOutForDelivery:
		return "out for delivery"
	case StatusDelivered:
		return "delivered"
	case StatusFailed:
		return "delivery attempt failed"
	case StatusCancelled:
		return "shipment cancelled"
	default:
		return "shipment updated"
	}
}
