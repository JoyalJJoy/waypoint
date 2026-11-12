package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/joyaljoy/waypoint/internal/auth"
	"github.com/joyaljoy/waypoint/internal/config"
	"github.com/joyaljoy/waypoint/internal/platform/database"
	"github.com/joyaljoy/waypoint/internal/rbac"
)

const demoPassword = "Passw0rd!"

type demoProduct struct {
	SKU          string
	Name         string
	Description  string
	PriceCents   int64
	WeightGrams  *int
	InitialStock int
}

type demoWarehouse struct {
	Name      string
	Code      string
	Line1     string
	City      string
	Region    string
	Postal    string
	Country   string
	Latitude  float64
	Longitude float64
	Products  []demoProduct
}

type demoUser struct {
	Email string
	Name  string
	Role  rbac.Role
}

type demoTenant struct {
	Name       string
	Slug       string
	Users      []demoUser
	Warehouses []demoWarehouse
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Seeding uses the owner URL because it intentionally writes across
	// tenants. The API itself must use the non-superuser URL.
	db, err := database.Open(ctx, cfg.MigrateDatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	hash, err := auth.HashPassword(demoPassword)
	if err != nil {
		log.Fatal(err)
	}

	tenants := []demoTenant{
		{
			Name: "Northwind Supply",
			Slug: "northwind",
			Users: []demoUser{
				{Email: "owner@northwind.test", Name: "Nora North", Role: rbac.RoleOwner},
				{Email: "dispatcher@northwind.test", Name: "Dev Dispatcher", Role: rbac.RoleDispatcher},
				{Email: "warehouse@northwind.test", Name: "Wendy Warehouse", Role: rbac.RoleWarehouseManager},
				{Email: "agent@northwind.test", Name: "Alex Agent", Role: rbac.RoleDeliveryAgent},
			},
			Warehouses: []demoWarehouse{
				{
					Name: "Main Warehouse", Code: "NW-MAIN",
					Line1: "1 Embarcadero Center", City: "San Francisco", Region: "CA", Postal: "94111", Country: "US",
					Latitude: 37.7936, Longitude: -122.3965,
					Products: []demoProduct{
						{SKU: "NW-TEE-001", Name: "Northwind Classic Tee", Description: "Heavyweight organic cotton tee", PriceCents: 2400, WeightGrams: intPtr(180), InitialStock: 120},
						{SKU: "NW-MUG-002", Name: "Enamel Camp Mug", Description: "12oz enamel-coated steel mug", PriceCents: 1850, WeightGrams: intPtr(320), InitialStock: 300},
						{SKU: "NW-PACK-003", Name: "Trail Daypack 20L", Description: "Weatherproof daypack with laptop sleeve", PriceCents: 7900, WeightGrams: intPtr(680), InitialStock: 45},
					},
				},
				{
					Name: "West Coast DC", Code: "NW-OAK",
					Line1: "1500 Maritime St", City: "Oakland", Region: "CA", Postal: "94607", Country: "US",
					Latitude: 37.8044, Longitude: -122.3207,
					Products: []demoProduct{
						{SKU: "NW-TEE-001", Name: "Northwind Classic Tee", Description: "Heavyweight organic cotton tee", PriceCents: 2400, WeightGrams: intPtr(180), InitialStock: 30},
						{SKU: "NW-MUG-002", Name: "Enamel Camp Mug", Description: "12oz enamel-coated steel mug", PriceCents: 1850, WeightGrams: intPtr(320), InitialStock: 100},
						{SKU: "NW-PACK-003", Name: "Trail Daypack 20L", Description: "Weatherproof daypack with laptop sleeve", PriceCents: 7900, WeightGrams: intPtr(680), InitialStock: 12},
					},
				},
			},
		},
		{
			Name: "Acme Retail",
			Slug: "acme",
			Users: []demoUser{
				{Email: "owner@acme.test", Name: "Avery Acme", Role: rbac.RoleOwner},
				{Email: "support@acme.test", Name: "Sam Support", Role: rbac.RoleSupport},
			},
			Warehouses: []demoWarehouse{
				{
					Name: "Acme Fulfillment", Code: "AC-NYC",
					Line1: "350 5th Ave", City: "New York", Region: "NY", Postal: "10118", Country: "US",
					Latitude: 40.7484, Longitude: -73.9857,
					Products: []demoProduct{
						{SKU: "AC-LAMP-001", Name: "Arc Desk Lamp", Description: "Adjustable LED desk lamp", PriceCents: 4200, WeightGrams: intPtr(900), InitialStock: 80},
						{SKU: "AC-CHAIR-002", Name: "Ergo Task Chair", Description: "Mesh-back task chair with lumbar support", PriceCents: 24900, WeightGrams: intPtr(12000), InitialStock: 25},
					},
				},
			},
		},
	}

	for _, tenant := range tenants {
		tenantID, ownerID, err := seedTenant(ctx, db, tenant, hash)
		if err != nil {
			log.Fatalf("seed %s: %v", tenant.Name, err)
		}
		if err := seedCatalog(ctx, db, tenantID, ownerID, tenant.Warehouses); err != nil {
			log.Fatalf("seed catalog for %s: %v", tenant.Name, err)
		}
		fmt.Printf("✓ %s (%s)\n", tenant.Name, tenant.Slug)
		for _, u := range tenant.Users {
			fmt.Printf("   %-28s %-18s %s\n", u.Email, u.Role, demoPassword)
		}
	}

	fmt.Println("\nSeed complete. All demo users share the password above.")
}

func seedTenant(ctx context.Context, db *database.DB, tenant demoTenant, passwordHash string) (uuid.UUID, uuid.UUID, error) {
	var tenantID, ownerID uuid.UUID

	err := db.InTx(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`INSERT INTO tenants (name, slug) VALUES ($1, $2)
			 ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name
			 RETURNING id`,
			tenant.Name, tenant.Slug,
		).Scan(&tenantID)
		if err != nil {
			return fmt.Errorf("upsert tenant: %w", err)
		}

		for _, u := range tenant.Users {
			var userID uuid.UUID
			err := tx.QueryRow(ctx,
				`INSERT INTO users (email, password_hash, name) VALUES ($1, $2, $3)
				 ON CONFLICT (email) DO UPDATE
				 SET password_hash = EXCLUDED.password_hash, name = EXCLUDED.name
				 RETURNING id`,
				u.Email, passwordHash, u.Name,
			).Scan(&userID)
			if err != nil {
				return fmt.Errorf("upsert user %s: %w", u.Email, err)
			}
			if u.Role == rbac.RoleOwner {
				ownerID = userID
			}

			if _, err := tx.Exec(ctx,
				`SELECT set_config('app.tenant_id', $1, true), set_config('app.user_id', $2, true)`,
				tenantID.String(), userID.String(),
			); err != nil {
				return fmt.Errorf("set tenant context: %w", err)
			}

			if _, err := tx.Exec(ctx,
				`INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, $3)
				 ON CONFLICT (tenant_id, user_id) DO UPDATE SET role = EXCLUDED.role`,
				tenantID, userID, u.Role,
			); err != nil {
				return fmt.Errorf("upsert membership for %s: %w", u.Email, err)
			}
		}
		return nil
	})

	return tenantID, ownerID, err
}

func seedCatalog(ctx context.Context, db *database.DB, tenantID, ownerID uuid.UUID, warehouses []demoWarehouse) error {
	return db.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`SELECT set_config('app.tenant_id', $1, true), set_config('app.user_id', $2, true)`,
			tenantID.String(), ownerID.String(),
		); err != nil {
			return err
		}

		for _, wh := range warehouses {
			address, _ := json.Marshal(map[string]string{
				"line1": wh.Line1, "city": wh.City, "region": wh.Region,
				"postal_code": wh.Postal, "country": wh.Country,
			})

			var warehouseID uuid.UUID
			err := tx.QueryRow(ctx,
				`INSERT INTO warehouses (tenant_id, name, code, address, latitude, longitude)
				 VALUES ($1, $2, $3, $4, $5, $6)
				 ON CONFLICT (tenant_id, code) DO UPDATE SET name = EXCLUDED.name
				 RETURNING id`,
				tenantID, wh.Name, wh.Code, address, wh.Latitude, wh.Longitude,
			).Scan(&warehouseID)
			if err != nil {
				return fmt.Errorf("upsert warehouse %s: %w", wh.Code, err)
			}

			for _, p := range wh.Products {
				var productID uuid.UUID
				err := tx.QueryRow(ctx,
					`INSERT INTO products (tenant_id, sku, name, description, unit_price_cents, currency, weight_grams)
					 VALUES ($1, $2, $3, $4, $5, 'USD', $6)
					 ON CONFLICT (tenant_id, sku) DO UPDATE SET
					   name = EXCLUDED.name, description = EXCLUDED.description,
					   unit_price_cents = EXCLUDED.unit_price_cents, weight_grams = EXCLUDED.weight_grams
					 RETURNING id`,
					tenantID, p.SKU, p.Name, p.Description, p.PriceCents, p.WeightGrams,
				).Scan(&productID)
				if err != nil {
					return fmt.Errorf("upsert product %s: %w", p.SKU, err)
				}

				tag, err := tx.Exec(ctx,
					`INSERT INTO stock_levels (tenant_id, warehouse_id, product_id, on_hand)
					 VALUES ($1, $2, $3, $4)
					 ON CONFLICT (tenant_id, warehouse_id, product_id) DO NOTHING`,
					tenantID, warehouseID, productID, p.InitialStock,
				)
				if err != nil {
					return fmt.Errorf("seed stock for %s: %w", p.SKU, err)
				}
				if tag.RowsAffected() == 1 {
					if _, err := tx.Exec(ctx,
						`INSERT INTO stock_movements
						    (tenant_id, warehouse_id, product_id, kind, quantity, reason, actor_id)
						 VALUES ($1, $2, $3, 'receipt', $4, 'seed initial stock', $5)`,
						tenantID, warehouseID, productID, p.InitialStock, ownerID,
					); err != nil {
						return fmt.Errorf("seed movement for %s: %w", p.SKU, err)
					}
				}
			}
		}
		return nil
	})
}

func intPtr(v int) *int { return &v }
