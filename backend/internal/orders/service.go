package orders

import (
	"context"
	"fmt"
	"strings"
	"time"

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

func (s *Service) Create(ctx context.Context, identity auth.Identity, p CreateParams, key string) (Order, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return Order{}, false, fmt.Errorf("%w: Idempotency-Key header is required", ErrValidation)
	}
	if len(key) > 128 {
		return Order{}, false, fmt.Errorf("%w: Idempotency-Key is too long", ErrValidation)
	}
	if err := p.NormalizeAndValidate(); err != nil {
		return Order{}, false, err
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		number := newOrderNumber()
		order, replayed, err := s.store.Create(ctx, identity.TenantID, identity.UserID, p, number, key)
		if err == nil {
			return order, replayed, nil
		}
		if err != ErrOrderNumberConflict {
			return Order{}, false, err
		}
		lastErr = err
	}
	return Order{}, false, lastErr
}

func (s *Service) Get(ctx context.Context, identity auth.Identity, id uuid.UUID) (Order, error) {
	return s.store.Get(ctx, identity.TenantID, identity.UserID, id)
}

func (s *Service) List(ctx context.Context, identity auth.Identity, status, query string, limit, offset int) ([]Summary, int64, error) {
	status = strings.TrimSpace(status)
	if status != "" && !Status(status).Valid() {
		return nil, 0, fmt.Errorf("%w: invalid order status", ErrValidation)
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return s.store.List(ctx, identity.TenantID, identity.UserID, status, strings.TrimSpace(query), limit, offset)
}

func (s *Service) Confirm(ctx context.Context, identity auth.Identity, id uuid.UUID) (Order, error) {
	return s.store.Confirm(ctx, identity.TenantID, identity.UserID, id)
}

func (s *Service) Cancel(ctx context.Context, identity auth.Identity, id uuid.UUID, reason string) (Order, error) {
	reason = strings.TrimSpace(reason)
	if len(reason) > 500 {
		return Order{}, fmt.Errorf("%w: cancellation reason cannot exceed 500 characters", ErrValidation)
	}
	return s.store.Cancel(ctx, identity.TenantID, identity.UserID, id, reason)
}

func newOrderNumber() string {
	raw := strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", ""))
	return fmt.Sprintf("ORD-%s-%s", time.Now().UTC().Format("20060102"), raw[:8])
}
