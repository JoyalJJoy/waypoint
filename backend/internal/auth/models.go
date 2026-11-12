package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/joyaljoy/waypoint/internal/rbac"
)

var (
	ErrValidation         = errors.New("validation failed")
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrEmailTaken         = errors.New("email is already registered")
	ErrSlugTaken          = errors.New("tenant slug is already taken")
	ErrNoMembership       = errors.New("user has no tenant membership")
	ErrInvalidSession     = errors.New("invalid or expired session")
)

type User struct {
	ID              uuid.UUID `json:"id"`
	Email           string    `json:"email"`
	Name            string    `json:"name"`
	IsPlatformAdmin bool      `json:"is_platform_admin"`
	PasswordHash    string    `json:"-"`
	CreatedAt       time.Time `json:"created_at"`
}

type Membership struct {
	TenantID   uuid.UUID `json:"tenant_id"`
	TenantName string    `json:"tenant_name"`
	TenantSlug string    `json:"tenant_slug"`
	Role       rbac.Role `json:"role"`
}

type Session struct {
	User             User         `json:"user"`
	Tenant           Membership   `json:"tenant"`
	Memberships      []Membership `json:"memberships"`
	AccessToken      string       `json:"-"`
	RefreshToken     string       `json:"-"`
	AccessExpiresAt  time.Time    `json:"access_expires_at"`
	RefreshExpiresAt time.Time    `json:"refresh_expires_at"`
}

type Identity struct {
	UserID   uuid.UUID
	TenantID uuid.UUID
	Role     rbac.Role
}

type identityKey struct{}

func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}
