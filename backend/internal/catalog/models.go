package catalog

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrValidation = errors.New("validation failed")
	ErrNotFound   = errors.New("not found")
	ErrConflict   = errors.New("conflict")
)

type Address struct {
	Line1      string `json:"line1"`
	Line2      string `json:"line2,omitempty"`
	City       string `json:"city"`
	Region     string `json:"region,omitempty"`
	PostalCode string `json:"postal_code"`
	Country    string `json:"country"`
}

type Warehouse struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Code      string    `json:"code"`
	Address   Address   `json:"address"`
	Latitude  *float64  `json:"latitude,omitempty"`
	Longitude *float64  `json:"longitude,omitempty"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Product struct {
	ID             uuid.UUID `json:"id"`
	SKU            string    `json:"sku"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	UnitPriceCents int64     `json:"unit_price_cents"`
	Currency       string    `json:"currency"`
	WeightGrams    *int      `json:"weight_grams,omitempty"`
	IsActive       bool      `json:"is_active"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func validateWarehouse(w *Warehouse) error {
	w.Name = strings.TrimSpace(w.Name)
	w.Code = strings.ToUpper(strings.TrimSpace(w.Code))
	if len(w.Name) < 2 || len(w.Name) > 120 {
		return fmt.Errorf("%w: warehouse name must be between 2 and 120 characters", ErrValidation)
	}
	if len(w.Code) < 2 || len(w.Code) > 32 {
		return fmt.Errorf("%w: warehouse code must be between 2 and 32 characters", ErrValidation)
	}
	return nil
}

func validateProduct(p *Product) error {
	p.SKU = strings.ToUpper(strings.TrimSpace(p.SKU))
	p.Name = strings.TrimSpace(p.Name)
	p.Description = strings.TrimSpace(p.Description)
	p.Currency = strings.ToUpper(strings.TrimSpace(p.Currency))

	if len(p.SKU) < 2 || len(p.SKU) > 64 {
		return fmt.Errorf("%w: SKU must be between 2 and 64 characters", ErrValidation)
	}
	if len(p.Name) < 1 || len(p.Name) > 200 {
		return fmt.Errorf("%w: product name must be between 1 and 200 characters", ErrValidation)
	}
	if p.UnitPriceCents < 0 {
		return fmt.Errorf("%w: price cannot be negative", ErrValidation)
	}
	if len(p.Currency) != 3 {
		return fmt.Errorf("%w: currency must be a 3-letter ISO code", ErrValidation)
	}
	if p.WeightGrams != nil && *p.WeightGrams < 0 {
		return fmt.Errorf("%w: weight cannot be negative", ErrValidation)
	}
	return nil
}
