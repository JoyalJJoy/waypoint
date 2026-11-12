package orders

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrValidation          = errors.New("validation failed")
	ErrNotFound            = errors.New("not found")
	ErrConflict            = errors.New("conflict")
	ErrInsufficientStock   = errors.New("insufficient stock")
	ErrOrderNumberConflict = errors.New("order number conflict")
)

type Status string

const (
	StatusPending    Status = "pending"
	StatusConfirmed  Status = "confirmed"
	StatusProcessing Status = "processing"
	StatusCompleted  Status = "completed"
	StatusCancelled  Status = "cancelled"
)

func (s Status) Valid() bool {
	switch s {
	case StatusPending, StatusConfirmed, StatusProcessing, StatusCompleted, StatusCancelled:
		return true
	default:
		return false
	}
}

// CanTransition is deliberately explicit. Status changes should be driven by
// domain actions (confirm, cancel, shipment lifecycle), not arbitrary PATCHes.
func CanTransition(from, to Status) bool {
	switch from {
	case StatusPending:
		return to == StatusConfirmed || to == StatusCancelled
	case StatusConfirmed:
		return to == StatusProcessing || to == StatusCancelled
	case StatusProcessing:
		return to == StatusCompleted
	default:
		return false
	}
}

type ShippingAddress struct {
	Line1      string `json:"line1" required:"true" maxLength:"200"`
	Line2      string `json:"line2,omitempty" maxLength:"200"`
	City       string `json:"city" required:"true" maxLength:"120"`
	Region     string `json:"region,omitempty" maxLength:"120"`
	PostalCode string `json:"postal_code" required:"true" maxLength:"32"`
	Country    string `json:"country" required:"true" maxLength:"2" example:"US"`
}

type Item struct {
	ID             uuid.UUID `json:"id"`
	ProductID      uuid.UUID `json:"product_id"`
	SKU            string    `json:"sku"`
	Name           string    `json:"name"`
	Quantity       int       `json:"quantity"`
	UnitPriceCents int64     `json:"unit_price_cents"`
	TotalCents     int64     `json:"total_cents"`
}

type Event struct {
	ID         int64      `json:"id"`
	FromStatus *Status    `json:"from_status"`
	ToStatus   Status     `json:"to_status"`
	ActorID    *uuid.UUID `json:"actor_id,omitempty"`
	Note       string     `json:"note"`
	CreatedAt  time.Time  `json:"created_at"`
}

type Order struct {
	ID              uuid.UUID       `json:"id"`
	OrderNumber     string          `json:"order_number"`
	Status          Status          `json:"status"`
	WarehouseID     uuid.UUID       `json:"warehouse_id"`
	WarehouseName   string          `json:"warehouse_name"`
	CustomerName    string          `json:"customer_name"`
	CustomerEmail   string          `json:"customer_email"`
	ShippingAddress ShippingAddress `json:"shipping_address"`
	Currency        string          `json:"currency"`
	SubtotalCents   int64           `json:"subtotal_cents"`
	TotalCents      int64           `json:"total_cents"`
	Notes           string          `json:"notes"`
	Version         int64           `json:"version"`
	Items           []Item          `json:"items"`
	Events          []Event         `json:"events"`
	ConfirmedAt     *time.Time      `json:"confirmed_at,omitempty"`
	CancelledAt     *time.Time      `json:"cancelled_at,omitempty"`
	CompletedAt     *time.Time      `json:"completed_at,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type Summary struct {
	ID            uuid.UUID `json:"id"`
	OrderNumber   string    `json:"order_number"`
	Status        Status    `json:"status"`
	CustomerName  string    `json:"customer_name"`
	CustomerEmail string    `json:"customer_email"`
	Currency      string    `json:"currency"`
	TotalCents    int64     `json:"total_cents"`
	ItemCount     int64     `json:"item_count"`
	CreatedAt     time.Time `json:"created_at"`
}

type CreateItem struct {
	ProductID uuid.UUID
	Quantity  int
}

type CreateParams struct {
	WarehouseID     uuid.UUID
	CustomerName    string
	CustomerEmail   string
	ShippingAddress ShippingAddress
	Notes           string
	Items           []CreateItem
}

func (p *CreateParams) NormalizeAndValidate() error {
	p.CustomerName = strings.TrimSpace(p.CustomerName)
	p.CustomerEmail = strings.ToLower(strings.TrimSpace(p.CustomerEmail))
	p.Notes = strings.TrimSpace(p.Notes)
	p.ShippingAddress.Line1 = strings.TrimSpace(p.ShippingAddress.Line1)
	p.ShippingAddress.Line2 = strings.TrimSpace(p.ShippingAddress.Line2)
	p.ShippingAddress.City = strings.TrimSpace(p.ShippingAddress.City)
	p.ShippingAddress.Region = strings.TrimSpace(p.ShippingAddress.Region)
	p.ShippingAddress.PostalCode = strings.TrimSpace(p.ShippingAddress.PostalCode)
	p.ShippingAddress.Country = strings.ToUpper(strings.TrimSpace(p.ShippingAddress.Country))

	if p.WarehouseID == uuid.Nil {
		return fmt.Errorf("%w: warehouse_id is required", ErrValidation)
	}
	if len(p.CustomerName) < 2 || len(p.CustomerName) > 120 {
		return fmt.Errorf("%w: customer name must be between 2 and 120 characters", ErrValidation)
	}
	addr, err := mail.ParseAddress(p.CustomerEmail)
	if err != nil || addr.Address != p.CustomerEmail {
		return fmt.Errorf("%w: a valid customer email is required", ErrValidation)
	}
	if len(p.Notes) > 1000 {
		return fmt.Errorf("%w: notes cannot exceed 1000 characters", ErrValidation)
	}
	if p.ShippingAddress.Line1 == "" || p.ShippingAddress.City == "" ||
		p.ShippingAddress.PostalCode == "" || len(p.ShippingAddress.Country) != 2 {
		return fmt.Errorf("%w: shipping address requires line1, city, postal code, and a 2-letter country", ErrValidation)
	}
	if len(p.Items) == 0 || len(p.Items) > 100 {
		return fmt.Errorf("%w: an order must contain between 1 and 100 line items", ErrValidation)
	}

	seen := make(map[uuid.UUID]struct{}, len(p.Items))
	for i := range p.Items {
		if p.Items[i].ProductID == uuid.Nil {
			return fmt.Errorf("%w: item %d has an invalid product_id", ErrValidation, i+1)
		}
		if p.Items[i].Quantity < 1 || p.Items[i].Quantity > 10_000 {
			return fmt.Errorf("%w: item %d quantity must be between 1 and 10000", ErrValidation, i+1)
		}
		if _, exists := seen[p.Items[i].ProductID]; exists {
			return fmt.Errorf("%w: duplicate product in order items", ErrValidation)
		}
		seen[p.Items[i].ProductID] = struct{}{}
	}

	sort.Slice(p.Items, func(i, j int) bool {
		return p.Items[i].ProductID.String() < p.Items[j].ProductID.String()
	})
	return nil
}

func (p CreateParams) RequestHash() string {
	type item struct {
		ProductID string `json:"product_id"`
		Quantity  int    `json:"quantity"`
	}
	items := make([]item, 0, len(p.Items))
	for _, it := range p.Items {
		items = append(items, item{ProductID: it.ProductID.String(), Quantity: it.Quantity})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ProductID < items[j].ProductID })

	b, _ := json.Marshal(struct {
		WarehouseID   string          `json:"warehouse_id"`
		CustomerName  string          `json:"customer_name"`
		CustomerEmail string          `json:"customer_email"`
		Address       ShippingAddress `json:"address"`
		Notes         string          `json:"notes"`
		Items         []item          `json:"items"`
	}{
		WarehouseID:   p.WarehouseID.String(),
		CustomerName:  p.CustomerName,
		CustomerEmail: p.CustomerEmail,
		Address:       p.ShippingAddress,
		Notes:         p.Notes,
		Items:         items,
	})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
