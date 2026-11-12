package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const tokenIssuer = "waypoint-api"

type AccessClaims struct {
	TenantID  uuid.UUID `json:"tid"`
	TokenType string    `json:"typ"`
	jwt.RegisteredClaims
}

type TokenManager struct {
	secret []byte
	ttl    time.Duration
}

func NewTokenManager(secret string, accessTTL time.Duration) *TokenManager {
	return &TokenManager{secret: []byte(secret), ttl: accessTTL}
}

func (m *TokenManager) IssueAccess(userID, tenantID uuid.UUID) (string, time.Time, error) {
	now := time.Now().UTC()
	expires := now.Add(m.ttl)

	claims := AccessClaims{
		TenantID:  tenantID,
		TokenType: "access",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Subject:   userID.String(),
			Audience:  jwt.ClaimStrings{tokenIssuer},
			ExpiresAt: jwt.NewNumericDate(expires),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(-5 * time.Second)),
			ID:        uuid.NewString(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign access token: %w", err)
	}
	return signed, expires, nil
}

func (m *TokenManager) ParseAccess(raw string) (Identity, error) {
	if raw == "" {
		return Identity{}, ErrInvalidSession
	}

	claims := &AccessClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method: %s", t.Method.Alg())
		}
		return m.secret, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(tokenIssuer),
		jwt.WithAudience(tokenIssuer),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(15*time.Second),
	)
	if err != nil || !token.Valid {
		return Identity{}, ErrInvalidSession
	}
	if claims.TokenType != "access" || claims.TenantID == uuid.Nil {
		return Identity{}, ErrInvalidSession
	}

	userID, err := uuid.Parse(claims.Subject)
	if err != nil || userID == uuid.Nil {
		return Identity{}, ErrInvalidSession
	}

	return Identity{UserID: userID, TenantID: claims.TenantID}, nil
}

type refreshSecret struct {
	Raw       string
	Hash      string
	ExpiresAt time.Time
}

func newRefreshSecret(ttl time.Duration) (refreshSecret, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return refreshSecret{}, fmt.Errorf("generate refresh token: %w", err)
	}
	raw := base64.RawURLEncoding.EncodeToString(buf)
	return refreshSecret{
		Raw:       raw,
		Hash:      hashToken(raw),
		ExpiresAt: time.Now().UTC().Add(ttl),
	}, nil
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

var errRefreshRace = errors.New("refresh token already rotated")
