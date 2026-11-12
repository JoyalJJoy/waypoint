package catalog

import (
	"context"

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

func (s *Service) CreateWarehouse(ctx context.Context, identity auth.Identity, w Warehouse) (Warehouse, error) {
	if err := validateWarehouse(&w); err != nil {
		return Warehouse{}, err
	}
	return s.store.CreateWarehouse(ctx, identity.TenantID, identity.UserID, w)
}

func (s *Service) ListWarehouses(ctx context.Context, identity auth.Identity) ([]Warehouse, error) {
	return s.store.ListWarehouses(ctx, identity.TenantID, identity.UserID)
}

func (s *Service) GetWarehouse(ctx context.Context, identity auth.Identity, id uuid.UUID) (Warehouse, error) {
	return s.store.GetWarehouse(ctx, identity.TenantID, identity.UserID, id)
}

func (s *Service) CreateProduct(ctx context.Context, identity auth.Identity, p Product) (Product, error) {
	if err := validateProduct(&p); err != nil {
		return Product{}, err
	}
	return s.store.CreateProduct(ctx, identity.TenantID, identity.UserID, p)
}

func (s *Service) ListProducts(ctx context.Context, identity auth.Identity, query string, limit, offset int) ([]Product, int64, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return s.store.ListProducts(ctx, identity.TenantID, identity.UserID, query, limit, offset)
}

func (s *Service) GetProduct(ctx context.Context, identity auth.Identity, id uuid.UUID) (Product, error) {
	return s.store.GetProduct(ctx, identity.TenantID, identity.UserID, id)
}
