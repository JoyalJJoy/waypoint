package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/joyaljoy/waypoint/internal/platform/audit"
	"github.com/joyaljoy/waypoint/internal/platform/database"
)

type Store struct {
	db *database.DB
}

func NewStore(db *database.DB) *Store {
	return &Store{db: db}
}

func (s *Store) CreateWarehouse(ctx context.Context, tenantID, userID uuid.UUID, w Warehouse) (Warehouse, error) {
	var out Warehouse
	err := s.db.InTenantTx(ctx, tenantID, userID, func(tx pgx.Tx) error {
		address, err := json.Marshal(w.Address)
		if err != nil {
			return fmt.Errorf("marshal address: %w", err)
		}
		row := tx.QueryRow(ctx,
			`INSERT INTO warehouses (tenant_id, name, code, address, latitude, longitude, is_active)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)
			 RETURNING id, name, code, address, latitude, longitude, is_active, created_at, updated_at`,
			tenantID, w.Name, w.Code, address, w.Latitude, w.Longitude, w.IsActive,
		)
		found, err := scanWarehouse(row)
		if err != nil {
			return err
		}
		out = found
		return audit.Write(ctx, tx, tenantID, userID, "catalog.warehouse.created", "warehouse", out.ID, map[string]string{
			"code": out.Code, "name": out.Name,
		})
	})
	if err != nil {
		return Warehouse{}, mapUnique(err, "warehouses_tenant_id_code_key", "a warehouse with this code already exists")
	}
	return out, nil
}

func (s *Store) ListWarehouses(ctx context.Context, tenantID, userID uuid.UUID) ([]Warehouse, error) {
	var out []Warehouse
	err := s.db.InTenantTx(ctx, tenantID, userID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT id, name, code, address, latitude, longitude, is_active, created_at, updated_at
			 FROM warehouses WHERE tenant_id = $1 ORDER BY name, id`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			w, err := scanWarehouse(rows)
			if err != nil {
				return err
			}
			out = append(out, w)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Store) GetWarehouse(ctx context.Context, tenantID, userID, id uuid.UUID) (Warehouse, error) {
	var out Warehouse
	err := s.db.InTenantTx(ctx, tenantID, userID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT id, name, code, address, latitude, longitude, is_active, created_at, updated_at
			 FROM warehouses WHERE tenant_id = $1 AND id = $2`, tenantID, id)
		found, err := scanWarehouse(row)
		if err != nil {
			return err
		}
		out = found
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Warehouse{}, fmt.Errorf("%w: warehouse not found", ErrNotFound)
	}
	return out, err
}

func (s *Store) CreateProduct(ctx context.Context, tenantID, userID uuid.UUID, p Product) (Product, error) {
	var out Product
	err := s.db.InTenantTx(ctx, tenantID, userID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`INSERT INTO products
			    (tenant_id, sku, name, description, unit_price_cents, currency, weight_grams, is_active)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			 RETURNING id, sku, name, description, unit_price_cents, currency, weight_grams, is_active, created_at, updated_at`,
			tenantID, p.SKU, p.Name, p.Description, p.UnitPriceCents, p.Currency, p.WeightGrams, p.IsActive,
		)
		found, err := scanProduct(row)
		if err != nil {
			return err
		}
		out = found
		return audit.Write(ctx, tx, tenantID, userID, "catalog.product.created", "product", out.ID, map[string]string{
			"sku": out.SKU, "name": out.Name,
		})
	})
	if err != nil {
		return Product{}, mapUnique(err, "products_tenant_id_sku_key", "a product with this SKU already exists")
	}
	return out, nil
}

func (s *Store) ListProducts(ctx context.Context, tenantID, userID uuid.UUID, query string, limit, offset int) ([]Product, int64, error) {
	var out []Product
	var total int64

	err := s.db.InTenantTx(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM products
			 WHERE tenant_id = $1
			   AND ($2 = '' OR sku ILIKE '%' || $2 || '%' OR name ILIKE '%' || $2 || '%')`,
			tenantID, query,
		).Scan(&total); err != nil {
			return err
		}

		rows, err := tx.Query(ctx,
			`SELECT id, sku, name, description, unit_price_cents, currency, weight_grams, is_active, created_at, updated_at
			 FROM products
			 WHERE tenant_id = $1
			   AND ($2 = '' OR sku ILIKE '%' || $2 || '%' OR name ILIKE '%' || $2 || '%')
			 ORDER BY name, id
			 LIMIT $3 OFFSET $4`,
			tenantID, query, limit, offset,
		)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			p, err := scanProduct(rows)
			if err != nil {
				return err
			}
			out = append(out, p)
		}
		return rows.Err()
	})

	return out, total, err
}

func (s *Store) GetProduct(ctx context.Context, tenantID, userID, id uuid.UUID) (Product, error) {
	var out Product
	err := s.db.InTenantTx(ctx, tenantID, userID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT id, sku, name, description, unit_price_cents, currency, weight_grams, is_active, created_at, updated_at
			 FROM products WHERE tenant_id = $1 AND id = $2`, tenantID, id)
		found, err := scanProduct(row)
		if err != nil {
			return err
		}
		out = found
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Product{}, fmt.Errorf("%w: product not found", ErrNotFound)
	}
	return out, err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanWarehouse(row rowScanner) (Warehouse, error) {
	var w Warehouse
	var address []byte
	var lat, lng *float64
	err := row.Scan(&w.ID, &w.Name, &w.Code, &address, &lat, &lng, &w.IsActive, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return Warehouse{}, err
	}
	if len(address) > 0 {
		if err := json.Unmarshal(address, &w.Address); err != nil {
			return Warehouse{}, fmt.Errorf("decode warehouse address: %w", err)
		}
	}
	w.Latitude = lat
	w.Longitude = lng
	return w, nil
}

func scanProduct(row rowScanner) (Product, error) {
	var p Product
	err := row.Scan(
		&p.ID, &p.SKU, &p.Name, &p.Description, &p.UnitPriceCents,
		&p.Currency, &p.WeightGrams, &p.IsActive, &p.CreatedAt, &p.UpdatedAt,
	)
	return p, err
}

func mapUnique(err error, constraint, message string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint {
		return fmt.Errorf("%w: %s", ErrConflict, message)
	}
	return err
}
