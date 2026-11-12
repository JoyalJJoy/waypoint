//go:build integration

package orders

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/joyaljoy/waypoint/internal/auth"
	"github.com/joyaljoy/waypoint/internal/platform/database"
	"github.com/joyaljoy/waypoint/internal/platform/idempotency"
)

// Run with:
//
//	go test -tags=integration ./internal/orders -run Integration -v
//
// The test uses the local compose database and cleans up after itself. It also
// demonstrates the intended split: the owner role for fixtures, the app role
// (subject to RLS) for everything the service does.
type orderFixture struct {
	svc         *Service
	admin       *database.DB
	app         *database.DB
	tenantID    uuid.UUID
	otherTenant uuid.UUID
	userID      uuid.UUID
	warehouseID uuid.UUID
	productID   uuid.UUID
}

func newOrderFixture(t *testing.T) *orderFixture {
	t.Helper()

	migrateURL := envOr("WAYPOINT_TEST_MIGRATE_DATABASE_URL", "postgres://waypoint:waypoint@localhost:5433/waypoint?sslmode=disable")
	appURL := envOr("WAYPOINT_TEST_DATABASE_URL", "postgres://waypoint_app:waypoint_app@localhost:5433/waypoint?sslmode=disable")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	admin, err := database.Open(ctx, migrateURL)
	if err != nil {
		t.Fatalf("open owner database (is compose running?): %v", err)
	}
	app, err := database.Open(ctx, appURL)
	if err != nil {
		admin.Close()
		t.Fatalf("open app database: %v", err)
	}

	f := &orderFixture{
		svc:         NewService(app),
		admin:       admin,
		app:         app,
		tenantID:    uuid.New(),
		otherTenant: uuid.New(),
		userID:      uuid.New(),
		warehouseID: uuid.New(),
		productID:   uuid.New(),
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = admin.Pool.Exec(cleanupCtx, `DELETE FROM tenants WHERE id = ANY($1)`, []uuid.UUID{f.tenantID, f.otherTenant})
		_, _ = admin.Pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1`, f.userID)
		app.Close()
		admin.Close()
	})

	suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	mustExec(t, admin, `INSERT INTO tenants (id, name, slug) VALUES ($1, $2, $3)`,
		f.tenantID, "Integration Tenant", "integration-"+suffix)
	mustExec(t, admin, `INSERT INTO tenants (id, name, slug) VALUES ($1, $2, $3)`,
		f.otherTenant, "Other Tenant", "integration-other-"+suffix)
	mustExec(t, admin, `INSERT INTO users (id, email, password_hash, name) VALUES ($1, $2, 'x', 'Integration User')`,
		f.userID, "integration-"+suffix+"@example.test")
	mustExec(t, admin, `INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, 'owner')`,
		f.tenantID, f.userID)
	mustExec(t, admin, `INSERT INTO warehouses (id, tenant_id, name, code) VALUES ($1, $2, 'Test Warehouse', 'TEST-WH')`,
		f.warehouseID, f.tenantID)
	mustExec(t, admin, `INSERT INTO products (id, tenant_id, sku, name, unit_price_cents, currency)
	                    VALUES ($1, $2, 'TEST-SKU', 'Test Product', 1250, 'USD')`,
		f.productID, f.tenantID)
	mustExec(t, admin, `INSERT INTO stock_levels (tenant_id, warehouse_id, product_id, on_hand)
	                    VALUES ($1, $2, $3, 10)`,
		f.tenantID, f.warehouseID, f.productID)

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

func (f *orderFixture) identity() auth.Identity {
	return auth.Identity{UserID: f.userID, TenantID: f.tenantID}
}

func (f *orderFixture) insertPendingOrder(t *testing.T, suffix string, quantity int) uuid.UUID {
	t.Helper()

	orderID := uuid.New()

	mustExec(t, f.admin, `INSERT INTO orders
	    (id, tenant_id, order_number, warehouse_id, customer_name, customer_email,
	     shipping_address, currency, subtotal_cents, total_cents)
	    VALUES ($1, $2, $3, $4, 'Test Customer', 'customer@example.test',
	            '{"line1":"1 Test Way","city":"Testville","postal_code":"00000","country":"US"}',
	            'USD', $5, $5)`,
		orderID, f.tenantID, "TEST-"+suffix, f.warehouseID, int64(quantity)*1250)
	mustExec(t, f.admin, `INSERT INTO order_items
	    (tenant_id, order_id, product_id, sku, name, quantity, unit_price_cents, total_cents)
	    VALUES ($1, $2, $3, 'TEST-SKU', 'Test Product', $4, 1250, $5)`,
		f.tenantID, orderID, f.productID, quantity, int64(quantity)*1250)

	return orderID
}

func TestIntegrationConfirmPreventsOversell(t *testing.T) {
	f := newOrderFixture(t)

	first := f.insertPendingOrder(t, "oversell-a", 8)
	second := f.insertPendingOrder(t, "oversell-b", 8)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ids := []uuid.UUID{first, second}
	errs := make([]error, len(ids))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id uuid.UUID) {
			defer wg.Done()
			<-start
			_, errs[i] = f.svc.Confirm(ctx, f.identity(), id)
		}(i, id)
	}
	close(start)
	wg.Wait()

	successes, conflicts := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrInsufficientStock):
			conflicts++
		default:
			t.Fatalf("unexpected confirm error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("expected exactly one success and one insufficient-stock conflict, got %d successes and %d conflicts", successes, conflicts)
	}

	var reserved int
	if err := f.admin.Pool.QueryRow(ctx,
		`SELECT reserved FROM stock_levels WHERE tenant_id = $1 AND warehouse_id = $2 AND product_id = $3`,
		f.tenantID, f.warehouseID, f.productID,
	).Scan(&reserved); err != nil {
		t.Fatal(err)
	}
	if reserved != 8 {
		t.Fatalf("expected 8 reserved units, got %d", reserved)
	}

	var held int
	if err := f.admin.Pool.QueryRow(ctx,
		`SELECT count(*) FROM stock_reservations WHERE tenant_id = $1 AND status = 'held'`,
		f.tenantID,
	).Scan(&held); err != nil {
		t.Fatal(err)
	}
	if held != 1 {
		t.Fatalf("expected one held reservation, got %d", held)
	}

	var confirmedEvents int
	if err := f.admin.Pool.QueryRow(ctx,
		`SELECT count(*) FROM outbox_events WHERE tenant_id = $1 AND event_type = 'order.confirmed'`,
		f.tenantID,
	).Scan(&confirmedEvents); err != nil {
		t.Fatal(err)
	}
	if confirmedEvents != 1 {
		t.Fatalf("expected one confirmed event, got %d", confirmedEvents)
	}
}

func TestIntegrationCreateIsIdempotent(t *testing.T) {
	f := newOrderFixture(t)

	params := CreateParams{
		WarehouseID:   f.warehouseID,
		CustomerName:  "Idempotent Customer",
		CustomerEmail: "idem@example.test",
		ShippingAddress: ShippingAddress{
			Line1: "1 Test Way", City: "Testville", PostalCode: "00000", Country: "US",
		},
		Items: []CreateItem{{ProductID: f.productID, Quantity: 2}},
	}

	first, replayed, err := f.svc.Create(context.Background(), f.identity(), params, "integration-key-1")
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	if replayed {
		t.Fatal("first create reported a replay")
	}

	second, replayed, err := f.svc.Create(context.Background(), f.identity(), params, "integration-key-1")
	if err != nil {
		t.Fatalf("replayed create: %v", err)
	}
	if !replayed {
		t.Fatal("second create with the same key was not a replay")
	}
	if first.ID != second.ID {
		t.Fatalf("replay returned a different order: %s vs %s", first.ID, second.ID)
	}

	conflicting := params
	conflicting.Notes = "this changes the request hash"
	if _, _, err := f.svc.Create(context.Background(), f.identity(), conflicting, "integration-key-1"); !errors.Is(err, idempotency.ErrConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}

	var orders int
	if err := f.admin.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM orders WHERE tenant_id = $1 AND customer_email = 'idem@example.test'`,
		f.tenantID,
	).Scan(&orders); err != nil {
		t.Fatal(err)
	}
	if orders != 1 {
		t.Fatalf("expected exactly one order for the idempotent customer, got %d", orders)
	}
}

func TestIntegrationRLSHidesCrossTenantOrder(t *testing.T) {
	f := newOrderFixture(t)
	orderID := f.insertPendingOrder(t, "rls", 1)

	_, err := f.svc.Get(context.Background(), auth.Identity{
		UserID:   f.userID,
		TenantID: f.otherTenant,
	}, orderID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for a cross-tenant order, got %v", err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
