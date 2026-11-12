package shipments

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrValidation     = errors.New("validation failed")
	ErrNotFound       = errors.New("not found")
	ErrConflict       = errors.New("conflict")
	ErrForbidden      = errors.New("forbidden")
	ErrNumberConflict = errors.New("shipment number conflict")
)

type Status string

const (
	StatusPending        Status = "pending"
	StatusAssigned       Status = "assigned"
	StatusPickedUp       Status = "picked_up"
	StatusInTransit      Status = "in_transit"
	StatusOutForDelivery Status = "out_for_delivery"
	StatusDelivered      Status = "delivered"
	StatusFailed         Status = "failed"
	StatusCancelled      Status = "cancelled"
)

func (s Status) Valid() bool {
	switch s {
	case StatusPending, StatusAssigned, StatusPickedUp, StatusInTransit,
		StatusOutForDelivery, StatusDelivered, StatusFailed, StatusCancelled:
		return true
	default:
		return false
	}
}

// CanTransition encodes the courier lifecycle. Agents move a shipment forward
// through these steps; arbitrary status PATCHes are rejected.
func CanTransition(from, to Status) bool {
	switch from {
	case StatusPending:
		return to == StatusAssigned || to == StatusCancelled
	case StatusAssigned:
		return to == StatusPickedUp || to == StatusCancelled
	case StatusPickedUp:
		return to == StatusInTransit
	case StatusInTransit:
		return to == StatusOutForDelivery
	case StatusOutForDelivery:
		return to == StatusDelivered || to == StatusFailed
	case StatusFailed:
		return to == StatusOutForDelivery || to == StatusCancelled
	default:
		return false
	}
}

var validServiceLevels = map[string]struct{}{
	"ground":    {},
	"expedited": {},
	"overnight": {},
	"freight":   {},
}

type Destination struct {
	City    string `json:"city"`
	Region  string `json:"region"`
	Country string `json:"country"`
}

type Agent struct {
	ID    uuid.UUID `json:"id"`
	Name  string    `json:"name"`
	Email string    `json:"email"`
}

type TrackingEvent struct {
	ID          int64      `json:"id"`
	Status      Status     `json:"status"`
	Description string     `json:"description"`
	Latitude    *float64   `json:"latitude,omitempty"`
	Longitude   *float64   `json:"longitude,omitempty"`
	ActorID     *uuid.UUID `json:"actor_id,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

type Shipment struct {
	ID                  uuid.UUID       `json:"id"`
	ShipmentNumber      string          `json:"shipment_number"`
	TrackingNumber      string          `json:"tracking_number"`
	Status              Status          `json:"status"`
	OrderID             uuid.UUID       `json:"order_id"`
	OrderNumber         string          `json:"order_number"`
	CustomerName        string          `json:"customer_name"`
	CustomerEmail       string          `json:"customer_email"`
	WarehouseID         uuid.UUID       `json:"warehouse_id"`
	WarehouseName       string          `json:"warehouse_name"`
	Carrier             string          `json:"carrier"`
	ServiceLevel        string          `json:"service_level"`
	Destination         Destination     `json:"destination"`
	WeightGrams         int             `json:"weight_grams"`
	Agent               *Agent          `json:"agent"`
	EstimatedDeliveryAt *time.Time      `json:"estimated_delivery_at,omitempty"`
	PickedUpAt          *time.Time      `json:"picked_up_at,omitempty"`
	DeliveredAt         *time.Time      `json:"delivered_at,omitempty"`
	CancelledAt         *time.Time      `json:"cancelled_at,omitempty"`
	Version             int64           `json:"version"`
	Events              []TrackingEvent `json:"events"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

type ShipmentSummary struct {
	ID             uuid.UUID  `json:"id"`
	ShipmentNumber string     `json:"shipment_number"`
	TrackingNumber string     `json:"tracking_number"`
	Status         Status     `json:"status"`
	OrderID        uuid.UUID  `json:"order_id"`
	OrderNumber    string     `json:"order_number"`
	CustomerName   string     `json:"customer_name"`
	Carrier        string     `json:"carrier"`
	ServiceLevel   string     `json:"service_level"`
	AgentID        *uuid.UUID `json:"agent_id,omitempty"`
	AgentName      string     `json:"agent_name"`
	CreatedAt      time.Time  `json:"created_at"`
	DeliveredAt    *time.Time `json:"delivered_at,omitempty"`
}

type CreateParams struct {
	OrderID             uuid.UUID
	Carrier             string
	ServiceLevel        string
	EstimatedDeliveryAt *time.Time
}

func (p *CreateParams) NormalizeAndValidate() error {
	p.Carrier = strings.TrimSpace(p.Carrier)
	p.ServiceLevel = strings.ToLower(strings.TrimSpace(p.ServiceLevel))
	if p.ServiceLevel == "" {
		p.ServiceLevel = "ground"
	}

	if p.OrderID == uuid.Nil {
		return fmt.Errorf("%w: order_id is required", ErrValidation)
	}
	if len(p.Carrier) < 1 || len(p.Carrier) > 80 {
		return fmt.Errorf("%w: carrier must be between 1 and 80 characters", ErrValidation)
	}
	if _, ok := validServiceLevels[p.ServiceLevel]; !ok {
		return fmt.Errorf("%w: service_level must be ground, expedited, overnight, or freight", ErrValidation)
	}
	if p.EstimatedDeliveryAt != nil && p.EstimatedDeliveryAt.Before(time.Now().Add(-time.Minute)) {
		return fmt.Errorf("%w: estimated_delivery_at cannot be in the past", ErrValidation)
	}
	return nil
}

type TransitionParams struct {
	Status    Status
	Note      string
	Latitude  *float64
	Longitude *float64
}

func (p *TransitionParams) NormalizeAndValidate() error {
	p.Note = strings.TrimSpace(p.Note)
	if !p.Status.Valid() {
		return fmt.Errorf("%w: invalid shipment status", ErrValidation)
	}
	if len(p.Note) > 500 {
		return fmt.Errorf("%w: note cannot exceed 500 characters", ErrValidation)
	}
	if (p.Latitude == nil) != (p.Longitude == nil) {
		return fmt.Errorf("%w: latitude and longitude must be provided together", ErrValidation)
	}
	if p.Latitude != nil && (*p.Latitude < -90 || *p.Latitude > 90) {
		return fmt.Errorf("%w: latitude must be between -90 and 90", ErrValidation)
	}
	if p.Longitude != nil && (*p.Longitude < -180 || *p.Longitude > 180) {
		return fmt.Errorf("%w: longitude must be between -180 and 180", ErrValidation)
	}
	return nil
}

// PublicTracking is the sanitized projection used by the customer-facing
// tracking page and WebSocket. It intentionally omits customer, address, and
// actor identifiers.
type PublicTracking struct {
	TrackingNumber      string        `json:"tracking_number"`
	Status              Status        `json:"status"`
	Carrier             string        `json:"carrier"`
	ServiceLevel        string        `json:"service_level"`
	OrderNumber         string        `json:"order_number"`
	Destination         Destination   `json:"destination"`
	EstimatedDeliveryAt *time.Time    `json:"estimated_delivery_at,omitempty"`
	PickedUpAt          *time.Time    `json:"picked_up_at,omitempty"`
	DeliveredAt         *time.Time    `json:"delivered_at,omitempty"`
	Events              []PublicEvent `json:"events"`
	CreatedAt           time.Time     `json:"created_at"`
	UpdatedAt           time.Time     `json:"updated_at"`
}

type PublicEvent struct {
	Status      Status    `json:"status"`
	Description string    `json:"description"`
	Latitude    *float64  `json:"latitude,omitempty"`
	Longitude   *float64  `json:"longitude,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}
