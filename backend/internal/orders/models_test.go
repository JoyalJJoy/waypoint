package orders

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCanTransition(t *testing.T) {
	t.Parallel()

	allowed := map[Status][]Status{
		StatusPending:    {StatusConfirmed, StatusCancelled},
		StatusConfirmed:  {StatusProcessing, StatusCancelled},
		StatusProcessing: {StatusCompleted},
		StatusCompleted:  nil,
		StatusCancelled:  nil,
	}
	all := []Status{StatusPending, StatusConfirmed, StatusProcessing, StatusCompleted, StatusCancelled}

	for from, permitted := range allowed {
		set := make(map[Status]struct{}, len(permitted))
		for _, to := range permitted {
			set[to] = struct{}{}
		}
		for _, to := range all {
			want := false
			_, want = set[to]
			if got := CanTransition(from, to); got != want {
				t.Errorf("CanTransition(%s, %s) = %v, want %v", from, to, got, want)
			}
		}
	}
}

func TestCreateParamsValidation(t *testing.T) {
	t.Parallel()

	valid := func() CreateParams {
		return CreateParams{
			WarehouseID:   uuid.New(),
			CustomerName:  "  Casey Customer  ",
			CustomerEmail: "  CASEY@Example.COM ",
			ShippingAddress: ShippingAddress{
				Line1:      "42 Market St",
				City:       "San Francisco",
				PostalCode: "94105",
				Country:    "us",
			},
			Notes: "leave at the front desk",
			Items: []CreateItem{{ProductID: uuid.New(), Quantity: 2}},
		}
	}

	t.Run("valid params are normalized", func(t *testing.T) {
		p := valid()
		if err := p.NormalizeAndValidate(); err != nil {
			t.Fatal(err)
		}
		if p.CustomerName != "Casey Customer" {
			t.Errorf("name not trimmed: %q", p.CustomerName)
		}
		if p.CustomerEmail != "casey@example.com" {
			t.Errorf("email not lowercased: %q", p.CustomerEmail)
		}
		if p.ShippingAddress.Country != "US" {
			t.Errorf("country not uppercased: %q", p.ShippingAddress.Country)
		}
	})

	tests := map[string]func(*CreateParams){
		"missing warehouse":   func(p *CreateParams) { p.WarehouseID = uuid.Nil },
		"short customer name": func(p *CreateParams) { p.CustomerName = "A" },
		"invalid email":       func(p *CreateParams) { p.CustomerEmail = "not-an-email" },
		"missing address":     func(p *CreateParams) { p.ShippingAddress.Line1 = "" },
		"bad country":         func(p *CreateParams) { p.ShippingAddress.Country = "USA" },
		"no items":            func(p *CreateParams) { p.Items = nil },
		"zero quantity":       func(p *CreateParams) { p.Items[0].Quantity = 0 },
		"duplicate product":   func(p *CreateParams) { p.Items = append(p.Items, p.Items[0]) },
		"missing product id":  func(p *CreateParams) { p.Items[0].ProductID = uuid.Nil },
		"notes too long":      func(p *CreateParams) { p.Notes = strings.Repeat("x", 1001) },
		"too many line items": func(p *CreateParams) { p.Items = make([]CreateItem, 101) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			p := valid()
			mutate(&p)
			if err := p.NormalizeAndValidate(); !errors.Is(err, ErrValidation) {
				t.Fatalf("expected ErrValidation, got %v", err)
			}
		})
	}
}

func TestRequestHashIsStableAndSensitive(t *testing.T) {
	t.Parallel()

	productA, productB := uuid.New(), uuid.New()
	base := CreateParams{
		WarehouseID:   uuid.New(),
		CustomerName:  "Casey Customer",
		CustomerEmail: "casey@example.com",
		ShippingAddress: ShippingAddress{
			Line1: "42 Market St", City: "San Francisco", PostalCode: "94105", Country: "US",
		},
		Items: []CreateItem{
			{ProductID: productA, Quantity: 1},
			{ProductID: productB, Quantity: 2},
		},
	}
	if err := base.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	first := base.RequestHash()

	reordered := base
	reordered.Items = []CreateItem{
		{ProductID: productB, Quantity: 2},
		{ProductID: productA, Quantity: 1},
	}
	if got := reordered.RequestHash(); got != first {
		t.Fatal("request hash must be independent of client item order")
	}

	changed := base
	changed.Notes = "different notes"
	if got := changed.RequestHash(); got == first {
		t.Fatal("request hash did not change when the payload changed")
	}

	changed = base
	changed.Items[0].Quantity++
	if got := changed.RequestHash(); got == first {
		t.Fatal("request hash did not change when an item quantity changed")
	}
}
