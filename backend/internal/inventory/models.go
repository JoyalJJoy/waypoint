package inventory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrValidation        = errors.New("validation failed")
	ErrNotFound          = errors.New("not found")
	ErrInsufficientStock = errors.New("insufficient stock")
)

type StockLevel struct {
	WarehouseID   uuid.UUID `json:"warehouse_id"`
	WarehouseName string    `json:"warehouse_name"`
	ProductID     uuid.UUID `json:"product_id"`
	SKU           string    `json:"sku"`
	ProductName   string    `json:"product_name"`
	OnHand        int       `json:"on_hand"`
	Reserved      int       `json:"reserved"`
	Available     int       `json:"available"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type Movement struct {
	ID            int64      `json:"id"`
	WarehouseID   uuid.UUID  `json:"warehouse_id"`
	WarehouseName string     `json:"warehouse_name"`
	ProductID     uuid.UUID  `json:"product_id"`
	SKU           string     `json:"sku"`
	ProductName   string     `json:"product_name"`
	Kind          string     `json:"kind"`
	Quantity      int        `json:"quantity"`
	Reason        string     `json:"reason"`
	OrderID       *uuid.UUID `json:"order_id,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

type AdjustParams struct {
	WarehouseID uuid.UUID
	ProductID   uuid.UUID
	Kind        string
	Quantity    int
	Reason      string
}

func (p AdjustParams) Normalize() (AdjustParams, error) {
	p.Kind = strings.ToLower(strings.TrimSpace(p.Kind))
	p.Reason = strings.TrimSpace(p.Reason)

	switch p.Kind {
	case "receipt", "return":
		if p.Quantity <= 0 {
			return p, fmt.Errorf("%w: %s quantity must be positive", ErrValidation, p.Kind)
		}
	case "adjustment":
		if p.Quantity == 0 {
			return p, fmt.Errorf("%w: adjustment quantity cannot be zero", ErrValidation)
		}
	default:
		return p, fmt.Errorf("%w: kind must be receipt, return, or adjustment", ErrValidation)
	}
	if p.Quantity > 1_000_000 || p.Quantity < -1_000_000 {
		return p, fmt.Errorf("%w: quantity is out of range", ErrValidation)
	}
	if len(p.Reason) < 3 || len(p.Reason) > 500 {
		return p, fmt.Errorf("%w: reason must be between 3 and 500 characters", ErrValidation)
	}
	return p, nil
}

func (p AdjustParams) RequestHash() string {
	type canonical struct {
		WarehouseID string `json:"warehouse_id"`
		ProductID   string `json:"product_id"`
		Kind        string `json:"kind"`
		Quantity    int    `json:"quantity"`
		Reason      string `json:"reason"`
	}
	b, _ := json.Marshal(canonical{
		WarehouseID: p.WarehouseID.String(),
		ProductID:   p.ProductID.String(),
		Kind:        p.Kind,
		Quantity:    p.Quantity,
		Reason:      p.Reason,
	})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
