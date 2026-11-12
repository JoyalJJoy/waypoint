package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/joyaljoy/waypoint/internal/platform/audit"
	"github.com/joyaljoy/waypoint/internal/platform/database"
	"github.com/joyaljoy/waypoint/internal/platform/idempotency"
)

type Store struct {
	db *database.DB
}

func NewStore(db *database.DB) *Store {
	return &Store{db: db}
}

func (s *Store) Adjust(ctx context.Context, tenantID, userID uuid.UUID, p AdjustParams, key string) (StockLevel, bool, error) {
	var out StockLevel
	replayed := false

	err := s.db.InTenantTx(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if key != "" {
			claim, err := idempotency.Claim(ctx, tx, tenantID, userID, key, p.RequestHash())
			if err != nil {
				return err
			}
			if claim.Replayed {
				replayed = true
				return json.Unmarshal(claim.Body, &out)
			}
		}

		var warehouseName, sku, productName string
		err := tx.QueryRow(ctx,
			`SELECT w.name, p.sku, p.name
			 FROM warehouses w, products p
			 WHERE w.tenant_id = $1 AND w.id = $2
			   AND p.tenant_id = $1 AND p.id = $3`,
			tenantID, p.WarehouseID, p.ProductID,
		).Scan(&warehouseName, &sku, &productName)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: warehouse or product not found", ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("load warehouse and product: %w", err)
		}

		// Make sure a stock row exists, then lock it. The lock serializes
		// concurrent adjustments for the same warehouse/product.
		if _, err := tx.Exec(ctx,
			`INSERT INTO stock_levels (tenant_id, warehouse_id, product_id)
			 VALUES ($1, $2, $3)
			 ON CONFLICT (tenant_id, warehouse_id, product_id) DO NOTHING`,
			tenantID, p.WarehouseID, p.ProductID,
		); err != nil {
			return fmt.Errorf("create stock level: %w", err)
		}

		var onHand, reserved int
		if err := tx.QueryRow(ctx,
			`SELECT on_hand, reserved FROM stock_levels
			 WHERE tenant_id = $1 AND warehouse_id = $2 AND product_id = $3
			 FOR UPDATE`,
			tenantID, p.WarehouseID, p.ProductID,
		).Scan(&onHand, &reserved); err != nil {
			return fmt.Errorf("lock stock level: %w", err)
		}

		newOnHand := onHand + p.Quantity
		if newOnHand < 0 || newOnHand < reserved {
			return fmt.Errorf("%w: adjustment would make on-hand (%d) lower than reserved (%d)", ErrInsufficientStock, newOnHand, reserved)
		}

		var updatedAt = out.UpdatedAt
		if err := tx.QueryRow(ctx,
			`UPDATE stock_levels
			 SET on_hand = $4, version = version + 1
			 WHERE tenant_id = $1 AND warehouse_id = $2 AND product_id = $3
			 RETURNING updated_at`,
			tenantID, p.WarehouseID, p.ProductID, newOnHand,
		).Scan(&updatedAt); err != nil {
			return fmt.Errorf("update stock level: %w", err)
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO stock_movements
			    (tenant_id, warehouse_id, product_id, kind, quantity, reason, actor_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			tenantID, p.WarehouseID, p.ProductID, p.Kind, p.Quantity, p.Reason, userID,
		); err != nil {
			return fmt.Errorf("insert stock movement: %w", err)
		}

		if err := audit.Write(ctx, tx, tenantID, userID, "inventory.adjusted", "stock_level", p.ProductID, map[string]any{
			"warehouse_id": p.WarehouseID, "kind": p.Kind, "quantity": p.Quantity, "reason": p.Reason,
		}); err != nil {
			return err
		}

		out = StockLevel{
			WarehouseID:   p.WarehouseID,
			WarehouseName: warehouseName,
			ProductID:     p.ProductID,
			SKU:           sku,
			ProductName:   productName,
			OnHand:        newOnHand,
			Reserved:      reserved,
			Available:     newOnHand - reserved,
			UpdatedAt:     updatedAt,
		}

		if key != "" {
			body, err := json.Marshal(out)
			if err != nil {
				return fmt.Errorf("marshal idempotent response: %w", err)
			}
			if err := idempotency.Complete(ctx, tx, tenantID, key, 200, body); err != nil {
				return err
			}
		}
		return nil
	})

	return out, replayed, err
}

func (s *Store) ListStock(ctx context.Context, tenantID, userID uuid.UUID, warehouseID *uuid.UUID, query string, limit, offset int) ([]StockLevel, int64, error) {
	var out []StockLevel
	var total int64

	err := s.db.InTenantTx(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`SELECT count(*)
			 FROM stock_levels sl
			 JOIN warehouses w ON w.tenant_id = sl.tenant_id AND w.id = sl.warehouse_id
			 JOIN products p ON p.tenant_id = sl.tenant_id AND p.id = sl.product_id
			 WHERE sl.tenant_id = $1
			   AND ($2::uuid IS NULL OR sl.warehouse_id = $2)
			   AND ($3 = '' OR p.sku ILIKE '%' || $3 || '%' OR p.name ILIKE '%' || $3 || '%')`,
			tenantID, warehouseID, query,
		).Scan(&total); err != nil {
			return err
		}

		rows, err := tx.Query(ctx,
			`SELECT sl.warehouse_id, w.name, sl.product_id, p.sku, p.name,
			        sl.on_hand, sl.reserved, sl.on_hand - sl.reserved, sl.updated_at
			 FROM stock_levels sl
			 JOIN warehouses w ON w.tenant_id = sl.tenant_id AND w.id = sl.warehouse_id
			 JOIN products p ON p.tenant_id = sl.tenant_id AND p.id = sl.product_id
			 WHERE sl.tenant_id = $1
			   AND ($2::uuid IS NULL OR sl.warehouse_id = $2)
			   AND ($3 = '' OR p.sku ILIKE '%' || $3 || '%' OR p.name ILIKE '%' || $3 || '%')
			 ORDER BY w.name, p.name
			 LIMIT $4 OFFSET $5`,
			tenantID, warehouseID, query, limit, offset,
		)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var sl StockLevel
			if err := rows.Scan(
				&sl.WarehouseID, &sl.WarehouseName, &sl.ProductID, &sl.SKU, &sl.ProductName,
				&sl.OnHand, &sl.Reserved, &sl.Available, &sl.UpdatedAt,
			); err != nil {
				return err
			}
			out = append(out, sl)
		}
		return rows.Err()
	})

	return out, total, err
}

func (s *Store) ListMovements(ctx context.Context, tenantID, userID uuid.UUID, warehouseID, productID *uuid.UUID, limit int) ([]Movement, error) {
	var out []Movement

	err := s.db.InTenantTx(ctx, tenantID, userID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT sm.id, sm.warehouse_id, w.name, sm.product_id, p.sku, p.name,
			        sm.kind, sm.quantity, sm.reason, sm.order_id, sm.created_at
			 FROM stock_movements sm
			 JOIN warehouses w ON w.tenant_id = sm.tenant_id AND w.id = sm.warehouse_id
			 JOIN products p ON p.tenant_id = sm.tenant_id AND p.id = sm.product_id
			 WHERE sm.tenant_id = $1
			   AND ($2::uuid IS NULL OR sm.warehouse_id = $2)
			   AND ($3::uuid IS NULL OR sm.product_id = $3)
			 ORDER BY sm.id DESC
			 LIMIT $4`,
			tenantID, warehouseID, productID, limit,
		)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var m Movement
			if err := rows.Scan(
				&m.ID, &m.WarehouseID, &m.WarehouseName, &m.ProductID, &m.SKU, &m.ProductName,
				&m.Kind, &m.Quantity, &m.Reason, &m.OrderID, &m.CreatedAt,
			); err != nil {
				return err
			}
			out = append(out, m)
		}
		return rows.Err()
	})

	return out, err
}
