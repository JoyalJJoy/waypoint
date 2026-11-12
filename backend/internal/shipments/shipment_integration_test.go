//go:build integration

package shipments

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/joyaljoy/waypoint/internal/auth"
	"github.com/joyaljoy/waypoint/internal/platform/database"
	"github.com/joyaljoy/waypoint/internal/rbac"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

type shipmentFixture struct {
	svc         *Service
	admin       *database.DB
	app         *database.DB
	tenantID    uuid.UUID
	otherTenant uuid.UUID
	ownerID     uuid.UUID
	agentID     uuid.UUID
	warehouseID uuid.UUID
	productID   uuid.UUID
	orderID     uuid.UUID
}

func newShipmentFixture(t *testing.T) *shipmentFixture {
	t.Helper()

	migrateURL := envOr("WAYPOINT_TEST_MIGRATE_DATABASE_URL", "postgres://waypoint:waypoint@localhost:5433/waypoint?sslmode=disable")
	appURL := envOr("WAYPOINT_TEST_DATABASE_URL", "postgres://waypoint_app:waypoint_app@localhost:5433/waypoint?sslmode=disable")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	admin, err := database.Open(ctx, migrateURL)
	if err != nil {
		t.Fatalf("open owner database: %v", err)
	}
	app, err := database.Open(ctx, appURL)
	if err != nil {
		admin.Close()
		t.Fatalf("open app database: %v", err)
	}

	f := &shipmentFixture{
		svc:         NewService(app),
		admin:       admin,
		app:         app,
		tenantID:    uuid.New(),
		otherTenant: uuid.New(),
		ownerID:     uuid.New(),
		agentID:     uuid.New(),
		warehouseID: uuid.New(),
		productID:   uuid.New(),
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = admin.Pool.Exec(cleanupCtx, `DELETE FROM tenants WHERE id = ANY($1)`, []uuid.UUID{f.tenantID, f.otherTenant})
		_, _ = admin.Pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{f.ownerID, f.agentID})
		app.Close()
		admin.Close()
	})

	suffix := uuid.NewString()[:8]
	mustExec(t, admin, `INSERT INTO tenants (id, name, slug) VALUES ($1, 'Ship Tenant', $2)`,
		f.tenantID, "ship-"+suffix)
	mustExec(t, admin, `INSERT INTO tenants (id, name, slug) VALUES ($1, 'Other Tenant', $2)`,
		f.otherTenant, "ship-other-"+suffix)
	mustExec(t, admin, `INSERT INTO users (id, email, password_hash, name) VALUES ($1, $2, 'x', 'Ship Owner')`,
		f.ownerID, "ship-owner-"+suffix+"@example.test")
	mustExec(t, admin, `INSERT INTO users (id, email, password_hash, name) VALUES ($1, $2, 'x', 'Ship Agent')`,
		f.agentID, "ship-agent-"+suffix+"@example.test")
	mustExec(t, admin, `INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, 'dispatcher')`,
		f.tenantID, f.ownerID)
	mustExec(t, admin, `INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, 'delivery_agent')`,
		f.tenantID, f.agentID)
	mustExec(t, admin, `INSERT INTO warehouses (id, tenant_id, name, code) VALUES ($1, $2, 'Ship Warehouse', 'SHIP-WH')`,
		f.warehouseID, f.tenantID)
	mustExec(t, admin, `INSERT INTO products (id, tenant_id, sku, name, unit_price_cents, currency, weight_grams)
	                    VALUES ($1, $2, 'SHIP-SKU', 'Ship Product', 1250, 'USD', 500)`,
		f.productID, f.tenantID)
	// 10 on hand, 3 already reserved for the confirmed order.
	mustExec(t, admin, `INSERT INTO stock_levels (tenant_id, warehouse_id, product_id, on_hand, reserved)
	                    VALUES ($1, $2, $3, 10, 3)`,
		f.tenantID, f.warehouseID, f.productID)

	f.orderID = uuid.New()
	mustExec(t, admin, `INSERT INTO orders
	    (id, tenant_id, order_number, warehouse_id, customer_name, customer_email,
	     shipping_address, currency, subtotal_cents, total_cents, status, confirmed_at)
	    VALUES ($1, $2, $3, $4, 'Ship Customer', 'ship-customer@example.test',
	            '{"line1":"1 Dock St","city":"Testville","region":"CA","postal_code":"00000","country":"US"}',
	            'USD', 3750, 3750, 'confirmed', now())`,
		f.orderID, f.tenantID, "SHIP-"+suffix, f.warehouseID)

	var orderItemID uuid.UUID
	if err := admin.Pool.QueryRow(ctx,
		`INSERT INTO order_items (tenant_id, order_id, product_id, sku, name, quantity, unit_price_cents, total_cents)
		 VALUES ($1, $2, $3, 'SHIP-SKU', 'Ship Product', 3, 1250, 3750) RETURNING id`,
		f.tenantID, f.orderID, f.productID,
	).Scan(&orderItemID); err != nil {
		t.Fatalf("insert order item: %v", err)
	}

	mustExec(t, admin, `INSERT INTO stock_reservations (tenant_id, warehouse_id, product_id, order_id, order_item_id, quantity)
	                    VALUES ($1, $2, $3, $4, $5, 3)`,
		f.tenantID, f.warehouseID, f.productID, f.orderID, orderItemID)

	return f
}

func mustExec(t *testing.T, db *database.DB, sql string, args ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := db.Pool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("exec fixture SQL: %v", err)
	}
}

func (f *shipmentFixture) dispatcher() auth.Identity {
	return auth.Identity{UserID: f.ownerID, TenantID: f.tenantID, Role: rbac.RoleDispatcher}
}

func (f *shipmentFixture) agent() auth.Identity {
	return auth.Identity{UserID: f.agentID, TenantID: f.tenantID, Role: rbac.RoleDeliveryAgent}
}

func TestIntegrationShipmentLifecycle(t *testing.T) {
	f := newShipmentFixture(t)
	ctx := context.Background()

	shipment, err := f.svc.Create(ctx, f.dispatcher(), CreateParams{
		OrderID:      f.orderID,
		Carrier:      "UPS",
		ServiceLevel: "ground",
	})
	if err != nil {
		t.Fatalf("create shipment: %v", err)
	}
	if shipment.Status != StatusPending {
		t.Fatalf("expected a pending shipment, got %s", shipment.Status)
	}
	if shipment.WeightGrams != 1500 {
		t.Fatalf("expected 1500g from 3 x 500g items, got %d", shipment.WeightGrams)
	}

	// A second shipment for the same order must be rejected.
	if _, err := f.svc.Create(ctx, f.dispatcher(), CreateParams{OrderID: f.orderID, Carrier: "DHL"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected a conflict for a duplicate shipment, got %v", err)
	}

	// The agent cannot move a shipment that is not assigned to them.
	if _, err := f.svc.Transition(ctx, f.agent(), shipment.ID, TransitionParams{Status: StatusPickedUp}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected a forbidden error, got %v", err)
	}

	if _, err := f.svc.Assign(ctx, f.dispatcher(), shipment.ID, f.agentID); err != nil {
		t.Fatalf("assign shipment: %v", err)
	}

	// Illegal jump: assigned -> in_transit is not a valid transition.
	if _, err := f.svc.Transition(ctx, f.agent(), shipment.ID, TransitionParams{Status: StatusInTransit}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected a conflict for an illegal transition, got %v", err)
	}

	shipment, err = f.svc.Transition(ctx, f.agent(), shipment.ID, TransitionParams{
		Status: StatusPickedUp, Note: "loaded on the van",
	})
	if err != nil {
		t.Fatalf("pick up shipment: %v", err)
	}
	if shipment.Status != StatusPickedUp {
		t.Fatalf("expected picked_up, got %s", shipment.Status)
	}

	var onHand, reserved int
	if err := f.admin.Pool.QueryRow(ctx,
		`SELECT on_hand, reserved FROM stock_levels WHERE tenant_id = $1 AND warehouse_id = $2 AND product_id = $3`,
		f.tenantID, f.warehouseID, f.productID,
	).Scan(&onHand, &reserved); err != nil {
		t.Fatal(err)
	}
	if onHand != 7 || reserved != 0 {
		t.Fatalf("expected on_hand 7 and reserved 0 after pickup, got %d/%d", onHand, reserved)
	}

	var orderStatus string
	if err := f.admin.Pool.QueryRow(ctx,
		`SELECT status::text FROM orders WHERE id = $1`, f.orderID).Scan(&orderStatus); err != nil {
		t.Fatal(err)
	}
	if orderStatus != "processing" {
		t.Fatalf("expected the order to be processing, got %s", orderStatus)
	}

	// The lifecycle is strict: picked_up -> in_transit -> out_for_delivery -> delivered.
	for _, step := range []Status{StatusInTransit, StatusOutForDelivery} {
		if _, err := f.svc.Transition(ctx, f.agent(), shipment.ID, TransitionParams{Status: step}); err != nil {
			t.Fatalf("advance shipment to %s: %v", step, err)
		}
	}

	shipment, err = f.svc.Transition(ctx, f.agent(), shipment.ID, TransitionParams{Status: StatusDelivered})
	if err != nil {
		t.Fatalf("deliver shipment: %v", err)
	}
	if shipment.Status != StatusDelivered {
		t.Fatalf("expected delivered, got %s", shipment.Status)
	}

	if err := f.admin.Pool.QueryRow(ctx,
		`SELECT status::text FROM orders WHERE id = $1`, f.orderID).Scan(&orderStatus); err != nil {
		t.Fatal(err)
	}
	if orderStatus != "completed" {
		t.Fatalf("expected the order to be completed, got %s", orderStatus)
	}

	// The public tracking view must not leak customer details.
	tracking, err := f.svc.PublicTracking(ctx, shipment.TrackingNumber)
	if err != nil {
		t.Fatalf("public tracking: %v", err)
	}
	if tracking.Status != StatusDelivered {
		t.Fatalf("expected delivered tracking status, got %s", tracking.Status)
	}
	if tracking.Destination.City != "Testville" {
		t.Fatalf("unexpected destination: %+v", tracking.Destination)
	}
}

func TestIntegrationPublicTrackingHidesUnknownNumbers(t *testing.T) {
	f := newShipmentFixture(t)

	if _, err := f.svc.PublicTracking(context.Background(), "WPUNKNOWN"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestIntegrationShipmentIsHiddenAcrossTenants(t *testing.T) {
	f := newShipmentFixture(t)
	ctx := context.Background()

	shipment, err := f.svc.Create(ctx, f.dispatcher(), CreateParams{OrderID: f.orderID, Carrier: "UPS"})
	if err != nil {
		t.Fatalf("create shipment: %v", err)
	}

	_, err = f.svc.Get(ctx, auth.Identity{UserID: f.ownerID, TenantID: f.otherTenant}, shipment.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for a cross-tenant shipment, got %v", err)
	}
}
