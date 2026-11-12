package orders

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/joyaljoy/waypoint/internal/auth"
)

// These tests exercise the validation that happens before any database work, so
// they can run without PostgreSQL.
func TestCreateRequiresIdempotencyKey(t *testing.T) {
	t.Parallel()

	svc := NewService(nil)
	_, _, err := svc.Create(context.Background(), auth.Identity{}, CreateParams{}, "  ")
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
}

func TestCreateRejectsOversizedIdempotencyKey(t *testing.T) {
	t.Parallel()

	svc := NewService(nil)
	_, _, err := svc.Create(context.Background(), auth.Identity{}, CreateParams{}, strings.Repeat("k", 129))
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
}

func TestListRejectsUnknownStatus(t *testing.T) {
	t.Parallel()

	svc := NewService(nil)
	_, _, err := svc.List(context.Background(), auth.Identity{}, "shipped", "", 50, 0)
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
}

func TestCancelRejectsOversizedReason(t *testing.T) {
	t.Parallel()

	svc := NewService(nil)
	_, err := svc.Cancel(context.Background(), auth.Identity{}, uuid.Nil, strings.Repeat("r", 501))
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
}
