package auth

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAccessTokenRoundTrip(t *testing.T) {
	t.Parallel()

	manager := NewTokenManager("0123456789abcdef0123456789abcdef", 15*time.Minute)
	userID, tenantID := uuid.New(), uuid.New()

	raw, expires, err := manager.IssueAccess(userID, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(expires) < 14*time.Minute {
		t.Fatalf("access token expiry is too short: %s", time.Until(expires))
	}

	identity, err := manager.ParseAccess(raw)
	if err != nil {
		t.Fatalf("ParseAccess: %v", err)
	}
	if identity.UserID != userID || identity.TenantID != tenantID {
		t.Fatalf("identity mismatch: got %+v", identity)
	}
}

func TestAccessTokenRejectsWrongSecretOrTampering(t *testing.T) {
	t.Parallel()

	issuer := NewTokenManager("0123456789abcdef0123456789abcdef", 15*time.Minute)
	other := NewTokenManager("fedcba9876543210fedcba9876543210", 15*time.Minute)
	raw, _, err := issuer.IssueAccess(uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := other.ParseAccess(raw); err == nil {
		t.Fatal("token signed with a different secret was accepted")
	}

	tampered := raw[:len(raw)-1] + string(raw[len(raw)-1]^1)
	if _, err := issuer.ParseAccess(tampered); err == nil {
		t.Fatal("tampered token was accepted")
	}

	if _, err := issuer.ParseAccess(""); err == nil {
		t.Fatal("empty token was accepted")
	}
}

func TestAccessTokenRejectsExpiredToken(t *testing.T) {
	t.Parallel()

	manager := NewTokenManager("0123456789abcdef0123456789abcdef", -time.Minute)
	raw, _, err := manager.IssueAccess(uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}

	// WithLeeway is 15s, so a token that expired a minute ago is invalid.
	if _, err := manager.ParseAccess(raw); err == nil {
		t.Fatal("expired access token was accepted")
	}
}

func TestRefreshSecretIsRandomAndHashed(t *testing.T) {
	t.Parallel()

	first, err := newRefreshSecret(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newRefreshSecret(time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	if first.Raw == second.Raw {
		t.Fatal("refresh secrets are not random")
	}
	if first.Raw == first.Hash {
		t.Fatal("refresh token is stored unhashed")
	}
	if len(first.Hash) != 64 {
		t.Fatalf("refresh token hash should be a 32-byte hex digest, got %d chars", len(first.Hash))
	}
	if hashToken(first.Raw) != first.Hash {
		t.Fatal("refresh token hash is not stable")
	}
	if time.Until(first.ExpiresAt) > time.Hour {
		t.Fatal("refresh token expiry exceeds the requested TTL")
	}
}
