package shipments

import (
	"context"
	"crypto/rand"
	"encoding/base32"
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

func (s *Service) Create(ctx context.Context, identity auth.Identity, p CreateParams) (Shipment, error) {
	if err := p.NormalizeAndValidate(); err != nil {
		return Shipment{}, err
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		trackingNumber, err := newTrackingNumber()
		if err != nil {
			return Shipment{}, err
		}
		shipment, err := s.store.Create(ctx, identity.TenantID, identity.UserID, p, newShipmentNumber(), trackingNumber)
		if err == nil {
			return shipment, nil
		}
		if err != ErrNumberConflict {
			return Shipment{}, err
		}
		lastErr = err
	}
	return Shipment{}, lastErr
}

func (s *Service) Get(ctx context.Context, identity auth.Identity, id uuid.UUID) (Shipment, error) {
	return s.store.Get(ctx, identity.TenantID, identity.UserID, id)
}

func (s *Service) List(ctx context.Context, identity auth.Identity, status, query string, limit, offset int) ([]ShipmentSummary, int64, error) {
	status = strings.TrimSpace(status)
	if status != "" && !Status(status).Valid() {
		return nil, 0, fmt.Errorf("%w: invalid shipment status", ErrValidation)
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return s.store.List(ctx, identity.TenantID, identity.UserID, status, strings.TrimSpace(query), limit, offset)
}

func (s *Service) Assign(ctx context.Context, identity auth.Identity, shipmentID, agentID uuid.UUID) (Shipment, error) {
	if shipmentID == uuid.Nil {
		return Shipment{}, fmt.Errorf("%w: shipment id is required", ErrValidation)
	}
	if agentID == uuid.Nil {
		return Shipment{}, fmt.Errorf("%w: agent_id is required", ErrValidation)
	}
	return s.store.Assign(ctx, identity.TenantID, identity.UserID, shipmentID, agentID)
}

func (s *Service) Transition(ctx context.Context, identity auth.Identity, shipmentID uuid.UUID, p TransitionParams) (Shipment, error) {
	if shipmentID == uuid.Nil {
		return Shipment{}, fmt.Errorf("%w: shipment id is required", ErrValidation)
	}
	if err := p.NormalizeAndValidate(); err != nil {
		return Shipment{}, err
	}
	return s.store.Transition(ctx, identity.TenantID, identity.UserID, identity.Role, shipmentID, p)
}

func (s *Service) PublicTracking(ctx context.Context, trackingNumber string) (PublicTracking, error) {
	trackingNumber = strings.TrimSpace(trackingNumber)
	if !validTrackingNumber(trackingNumber) {
		return PublicTracking{}, fmt.Errorf("%w: tracking number not found", ErrNotFound)
	}
	return s.store.PublicTracking(ctx, trackingNumber)
}

func (s *Service) ListAgents(ctx context.Context, identity auth.Identity) ([]Agent, error) {
	return s.store.ListAgents(ctx, identity.TenantID, identity.UserID)
}

func validTrackingNumber(v string) bool {
	if len(v) < 8 || len(v) > 64 {
		return false
	}
	for _, r := range v {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func newShipmentNumber() string {
	raw := strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", ""))
	return fmt.Sprintf("SHP-%s-%s", time.Now().UTC().Format("20060102"), raw[:8])
}

// Tracking numbers are unguessable because the public tracking endpoint treats
// them as bearer tokens. 96 bits of randomness is far beyond brute force.
func newTrackingNumber() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate tracking number: %w", err)
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf)
	return "WP" + encoded, nil
}
