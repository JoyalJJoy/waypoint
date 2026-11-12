package shipments

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func timeAgo() time.Time {
	return time.Now().Add(-time.Hour)
}

func TestCanTransition(t *testing.T) {
	t.Parallel()

	allowed := map[Status][]Status{
		StatusPending:        {StatusAssigned, StatusCancelled},
		StatusAssigned:       {StatusPickedUp, StatusCancelled},
		StatusPickedUp:       {StatusInTransit},
		StatusInTransit:      {StatusOutForDelivery},
		StatusOutForDelivery: {StatusDelivered, StatusFailed},
		StatusFailed:         {StatusOutForDelivery, StatusCancelled},
		StatusDelivered:      nil,
		StatusCancelled:      nil,
	}
	all := []Status{
		StatusPending, StatusAssigned, StatusPickedUp, StatusInTransit,
		StatusOutForDelivery, StatusDelivered, StatusFailed, StatusCancelled,
	}

	for from, permitted := range allowed {
		set := make(map[Status]struct{}, len(permitted))
		for _, to := range permitted {
			set[to] = struct{}{}
		}
		for _, to := range all {
			_, want := set[to]
			if got := CanTransition(from, to); got != want {
				t.Errorf("CanTransition(%s, %s) = %v, want %v", from, to, got, want)
			}
		}
	}
}

func TestCreateParamsValidation(t *testing.T) {
	t.Parallel()

	valid := func() CreateParams {
		return CreateParams{OrderID: uuid.New(), Carrier: "  UPS  ", ServiceLevel: "Ground"}
	}

	p := valid()
	if err := p.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	if p.Carrier != "UPS" || p.ServiceLevel != "ground" {
		t.Fatalf("params were not normalized: %+v", p)
	}

	p = valid()
	p.ServiceLevel = ""
	if err := p.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	if p.ServiceLevel != "ground" {
		t.Fatalf("expected the default service level, got %q", p.ServiceLevel)
	}

	cases := map[string]func(*CreateParams){
		"missing order":   func(p *CreateParams) { p.OrderID = uuid.Nil },
		"missing carrier": func(p *CreateParams) { p.Carrier = "  " },
		"unknown service": func(p *CreateParams) { p.ServiceLevel = "teleport" },
		"past eta":        func(p *CreateParams) { past := timeAgo(); p.EstimatedDeliveryAt = &past },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := valid()
			mutate(&p)
			if err := p.NormalizeAndValidate(); !errors.Is(err, ErrValidation) {
				t.Fatalf("expected ErrValidation, got %v", err)
			}
		})
	}
}

func TestTransitionParamsValidation(t *testing.T) {
	t.Parallel()

	base := func() TransitionParams {
		return TransitionParams{Status: StatusPickedUp, Note: "  collected from the dock  "}
	}

	p := base()
	if err := p.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	if p.Note != "collected from the dock" {
		t.Fatalf("note was not trimmed: %q", p.Note)
	}

	lat, lng := 37.79, -122.4
	cases := map[string]func(*TransitionParams){
		"unknown status":  func(p *TransitionParams) { p.Status = "teleported" },
		"long note":       func(p *TransitionParams) { p.Note = strings.Repeat("x", 501) },
		"lat without lng": func(p *TransitionParams) { p.Latitude = &lat },
		"lat out of range": func(p *TransitionParams) {
			bad := 91.0
			p.Latitude = &bad
			p.Longitude = &lng
		},
		"lng out of range": func(p *TransitionParams) {
			bad := 181.0
			p.Latitude = &lat
			p.Longitude = &bad
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := base()
			mutate(&p)
			if err := p.NormalizeAndValidate(); !errors.Is(err, ErrValidation) {
				t.Fatalf("expected ErrValidation, got %v", err)
			}
		})
	}
}

func TestTrackingNumberFormat(t *testing.T) {
	t.Parallel()

	first, err := newTrackingNumber()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newTrackingNumber()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("tracking numbers are not random")
	}
	if !strings.HasPrefix(first, "WP") {
		t.Fatalf("unexpected tracking number format: %s", first)
	}
	if !validTrackingNumber(first) {
		t.Fatalf("generated tracking number is rejected: %s", first)
	}
	for _, invalid := range []string{"", "short", "has-dashes", "has spaces", strings.Repeat("A", 65)} {
		if validTrackingNumber(invalid) {
			t.Errorf("%q should not be a valid tracking number", invalid)
		}
	}
}
