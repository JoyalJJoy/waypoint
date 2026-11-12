package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/joyaljoy/waypoint/internal/platform/database"
)

type Service struct {
	store      *Store
	tokens     *TokenManager
	logger     *slog.Logger
	accessTTL  time.Duration
	refreshTTL time.Duration
}

func NewService(db *database.DB, tokens *TokenManager, logger *slog.Logger, accessTTL, refreshTTL time.Duration) *Service {
	return &Service{
		store:      NewStore(db),
		tokens:     tokens,
		logger:     logger,
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
	}
}

type RegisterParams struct {
	Name       string
	Email      string
	Password   string
	TenantName string
	UserAgent  string
}

// This hash makes unknown-email logins take roughly the same time as known
// emails, reducing the account-enumeration signal from response timing.
var dummyPasswordHash, _ = HashPassword("waypoint-dummy-password")

func (s *Service) Register(ctx context.Context, p RegisterParams) (*Session, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.Email = normalizeEmail(p.Email)
	p.TenantName = strings.TrimSpace(p.TenantName)

	if n := utf8.RuneCountInString(p.Name); n < 2 || n > 120 {
		return nil, fmt.Errorf("%w: name must be between 2 and 120 characters", ErrValidation)
	}
	if !validEmail(p.Email) {
		return nil, fmt.Errorf("%w: a valid email is required", ErrValidation)
	}
	if n := utf8.RuneCountInString(p.Password); n < 8 || len(p.Password) > 128 {
		return nil, fmt.Errorf("%w: password must be between 8 and 128 characters", ErrValidation)
	}
	if n := utf8.RuneCountInString(p.TenantName); n < 2 || n > 120 {
		return nil, fmt.Errorf("%w: tenant name must be between 2 and 120 characters", ErrValidation)
	}

	passwordHash, err := HashPassword(p.Password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	refresh, err := newRefreshSecret(s.refreshTTL)
	if err != nil {
		return nil, err
	}

	baseSlug := slugify(p.TenantName)
	var user User
	var membership Membership
	for attempt := 0; attempt < 3; attempt++ {
		suffix := make([]byte, 3)
		if _, err := rand.Read(suffix); err != nil {
			return nil, fmt.Errorf("generate tenant slug: %w", err)
		}
		slug := fmt.Sprintf("%s-%s", baseSlug, hex.EncodeToString(suffix))

		user, membership, err = s.store.CreateAccount(ctx, createAccountParams{
			Name:             p.Name,
			Email:            p.Email,
			PasswordHash:     passwordHash,
			TenantName:       p.TenantName,
			TenantSlug:       slug,
			UserAgent:        truncate(p.UserAgent, 300),
			RefreshTokenID:   uuid.New(),
			RefreshTokenHash: refresh.Hash,
			RefreshExpiresAt: refresh.ExpiresAt,
		})
		if err == nil {
			break
		}
		if err != ErrSlugTaken {
			return nil, err
		}
	}
	if err != nil {
		return nil, err
	}

	return s.buildSession(user, membership, []Membership{membership}, refresh)
}

type LoginParams struct {
	Email     string
	Password  string
	UserAgent string
}

func (s *Service) Login(ctx context.Context, p LoginParams) (*Session, error) {
	email := normalizeEmail(p.Email)
	if !validEmail(email) {
		return nil, ErrInvalidCredentials
	}

	user, err := s.store.UserByEmail(ctx, email)
	if err != nil {
		if err == ErrInvalidCredentials {
			VerifyPassword(p.Password, dummyPasswordHash)
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	if !VerifyPassword(p.Password, user.PasswordHash) {
		return nil, ErrInvalidCredentials
	}

	memberships, err := s.store.MembershipsForUser(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	if len(memberships) == 0 {
		return nil, ErrNoMembership
	}
	membership := memberships[0]

	refresh, err := newRefreshSecret(s.refreshTTL)
	if err != nil {
		return nil, err
	}
	if err := s.store.CreateRefreshToken(ctx, refreshRecord{
		ID:        uuid.New(),
		UserID:    user.ID,
		TenantID:  membership.TenantID,
		TokenHash: refresh.Hash,
		FamilyID:  uuid.New(),
		UserAgent: truncate(p.UserAgent, 300),
		ExpiresAt: refresh.ExpiresAt,
	}); err != nil {
		return nil, err
	}

	return s.buildSession(user, membership, memberships, refresh)
}

func (s *Service) Refresh(ctx context.Context, rawToken, userAgent string) (*Session, error) {
	if rawToken == "" {
		return nil, ErrInvalidSession
	}

	record, err := s.store.RefreshTokenByHash(ctx, hashToken(rawToken))
	if err != nil {
		return nil, err
	}

	// Reuse of an already-rotated token is treated as theft: revoke the whole
	// token family, forcing a fresh login everywhere.
	if record.RevokedAt != nil {
		_ = s.store.RevokeFamily(ctx, record.FamilyID)
		return nil, ErrInvalidSession
	}
	if time.Now().UTC().After(record.ExpiresAt) {
		return nil, ErrInvalidSession
	}

	next, err := newRefreshSecret(s.refreshTTL)
	if err != nil {
		return nil, err
	}
	replacement := refreshRecord{
		ID:        uuid.New(),
		TokenHash: next.Hash,
		UserAgent: truncate(userAgent, 300),
		ExpiresAt: next.ExpiresAt,
	}

	membership, err := s.store.RotateRefreshToken(ctx, record, replacement)
	if err != nil {
		return nil, err
	}

	user, err := s.store.UserByID(ctx, record.UserID)
	if err != nil {
		return nil, err
	}
	memberships, err := s.store.MembershipsForUser(ctx, record.UserID)
	if err != nil {
		return nil, err
	}

	return s.buildSession(user, membership, memberships, next)
}

func (s *Service) Logout(ctx context.Context, rawToken string) error {
	if rawToken == "" {
		return nil
	}
	return s.store.RevokeRefreshToken(ctx, hashToken(rawToken))
}

type SwitchParams struct {
	Identity       Identity
	TenantID       uuid.UUID
	CurrentRefresh string
	UserAgent      string
}

func (s *Service) SwitchTenant(ctx context.Context, p SwitchParams) (*Session, error) {
	membership, err := s.store.Membership(ctx, p.TenantID, p.Identity.UserID)
	if err != nil {
		return nil, err
	}

	if p.CurrentRefresh != "" {
		_ = s.store.RevokeTokenForUser(ctx, hashToken(p.CurrentRefresh), p.Identity.UserID)
	}

	refresh, err := newRefreshSecret(s.refreshTTL)
	if err != nil {
		return nil, err
	}
	if err := s.store.CreateRefreshToken(ctx, refreshRecord{
		ID:        uuid.New(),
		UserID:    p.Identity.UserID,
		TenantID:  p.TenantID,
		TokenHash: refresh.Hash,
		FamilyID:  uuid.New(),
		UserAgent: truncate(p.UserAgent, 300),
		ExpiresAt: refresh.ExpiresAt,
	}); err != nil {
		return nil, err
	}

	user, err := s.store.UserByID(ctx, p.Identity.UserID)
	if err != nil {
		return nil, err
	}
	memberships, err := s.store.MembershipsForUser(ctx, p.Identity.UserID)
	if err != nil {
		return nil, err
	}

	return s.buildSession(user, membership, memberships, refresh)
}

func (s *Service) Me(ctx context.Context, identity Identity) (User, Membership, []Membership, error) {
	user, err := s.store.UserByID(ctx, identity.UserID)
	if err != nil {
		return User{}, Membership{}, nil, err
	}
	membership, err := s.store.Membership(ctx, identity.TenantID, identity.UserID)
	if err != nil {
		return User{}, Membership{}, nil, err
	}
	memberships, err := s.store.MembershipsForUser(ctx, identity.UserID)
	if err != nil {
		return User{}, Membership{}, nil, err
	}
	return user, membership, memberships, nil
}

func (s *Service) buildSession(user User, membership Membership, memberships []Membership, refresh refreshSecret) (*Session, error) {
	accessToken, accessExpires, err := s.tokens.IssueAccess(user.ID, membership.TenantID)
	if err != nil {
		return nil, err
	}
	return &Session{
		User:             user,
		Tenant:           membership,
		Memberships:      memberships,
		AccessToken:      accessToken,
		RefreshToken:     refresh.Raw,
		AccessExpiresAt:  accessExpires,
		RefreshExpiresAt: refresh.ExpiresAt,
	}, nil
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func validEmail(email string) bool {
	if email == "" || len(email) > 254 {
		return false
	}
	addr, err := mail.ParseAddress(email)
	return err == nil && addr.Address == email
}

func slugify(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > 30 {
		slug = strings.TrimRight(slug[:30], "-")
	}
	if slug == "" {
		slug = "workspace"
	}
	return slug
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
