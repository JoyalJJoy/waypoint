package inventory

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/joyaljoy/waypoint/internal/auth"
	"github.com/joyaljoy/waypoint/internal/platform/database"
)

type Service struct {
	store *Store
}

func NewService(db *database.DB) *Service {
	return &Service{store: NewStore(db)}
}

func (s *Service) Adjust(ctx context.Context, identity auth.Identity, params AdjustParams, key string) (StockLevel, bool, error) {
	normalized, err := params.Normalize()
	if err != nil {
		return StockLevel{}, false, err
	}
	key = strings.TrimSpace(key)
	if len(key) > 128 {
		return StockLevel{}, false, ErrValidation
	}
	return s.store.Adjust(ctx, identity.TenantID, identity.UserID, normalized, key)
}

func (s *Service) ListStock(ctx context.Context, identity auth.Identity, warehouseID *uuid.UUID, query string, limit, offset int) ([]StockLevel, int64, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return s.store.ListStock(ctx, identity.TenantID, identity.UserID, warehouseID, strings.TrimSpace(query), limit, offset)
}

func (s *Service) ListMovements(ctx context.Context, identity auth.Identity, warehouseID, productID *uuid.UUID, limit int) ([]Movement, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	return s.store.ListMovements(ctx, identity.TenantID, identity.UserID, warehouseID, productID, limit)
}
