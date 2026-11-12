package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/joyaljoy/waypoint/internal/platform/database"
	"github.com/joyaljoy/waypoint/internal/rbac"
)

type Store struct {
	db *database.DB
}

func NewStore(db *database.DB) *Store {
	return &Store{db: db}
}

func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	var u User
	err := s.db.Pool.QueryRow(ctx,
		`SELECT id, email, name, password_hash, is_platform_admin, created_at
		 FROM users WHERE email = $1`, email,
	).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.IsPlatformAdmin, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrInvalidCredentials
	}
	return u, err
}

func (s *Store) UserByID(ctx context.Context, id uuid.UUID) (User, error) {
	var u User
	err := s.db.Pool.QueryRow(ctx,
		`SELECT id, email, name, password_hash, is_platform_admin, created_at
		 FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.IsPlatformAdmin, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrInvalidSession
	}
	return u, err
}

func (s *Store) MembershipsForUser(ctx context.Context, userID uuid.UUID) ([]Membership, error) {
	var out []Membership
	err := s.db.InUserTx(ctx, userID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT m.tenant_id, t.name, t.slug, m.role
			 FROM memberships m
			 JOIN tenants t ON t.id = m.tenant_id
			 WHERE m.user_id = $1
			 ORDER BY m.created_at, t.name`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			m, err := scanMembership(rows)
			if err != nil {
				return err
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Store) Membership(ctx context.Context, tenantID, userID uuid.UUID) (Membership, error) {
	var m Membership
	err := s.db.InTenantTx(ctx, tenantID, userID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT m.tenant_id, t.name, t.slug, m.role
			 FROM memberships m
			 JOIN tenants t ON t.id = m.tenant_id
			 WHERE m.tenant_id = $1 AND m.user_id = $2`, tenantID, userID)
		found, err := scanMembership(row)
		if err != nil {
			return err
		}
		m = found
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, ErrNoMembership
	}
	return m, err
}

type createAccountParams struct {
	Name             string
	Email            string
	PasswordHash     string
	TenantName       string
	TenantSlug       string
	UserAgent        string
	RefreshTokenID   uuid.UUID
	RefreshTokenHash string
	RefreshExpiresAt time.Time
}

func (s *Store) CreateAccount(ctx context.Context, p createAccountParams) (User, Membership, error) {
	var user User
	var membership Membership

	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`INSERT INTO users (email, password_hash, name)
			 VALUES ($1, $2, $3)
			 RETURNING id, email, name, is_platform_admin, created_at`,
			p.Email, p.PasswordHash, p.Name,
		).Scan(&user.ID, &user.Email, &user.Name, &user.IsPlatformAdmin, &user.CreatedAt)
		if err != nil {
			if isConstraint(err, "users_email_key") {
				return ErrEmailTaken
			}
			return fmt.Errorf("insert user: %w", err)
		}
		user.PasswordHash = p.PasswordHash

		var tenantID uuid.UUID
		var tenantName, tenantSlug string
		err = tx.QueryRow(ctx,
			`INSERT INTO tenants (name, slug) VALUES ($1, $2)
			 RETURNING id, name, slug`,
			p.TenantName, p.TenantSlug,
		).Scan(&tenantID, &tenantName, &tenantSlug)
		if err != nil {
			if isConstraint(err, "tenants_slug_key") {
				return ErrSlugTaken
			}
			return fmt.Errorf("insert tenant: %w", err)
		}

		if _, err := tx.Exec(ctx,
			`SELECT set_config('app.tenant_id', $1, true), set_config('app.user_id', $2, true)`,
			tenantID.String(), user.ID.String(),
		); err != nil {
			return fmt.Errorf("set tenant context: %w", err)
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, $3)`,
			tenantID, user.ID, rbac.RoleOwner,
		); err != nil {
			return fmt.Errorf("insert membership: %w", err)
		}

		if err := insertRefreshToken(ctx, tx, refreshRecord{
			ID:        p.RefreshTokenID,
			UserID:    user.ID,
			TenantID:  tenantID,
			TokenHash: p.RefreshTokenHash,
			FamilyID:  uuid.New(),
			UserAgent: p.UserAgent,
			ExpiresAt: p.RefreshExpiresAt,
		}); err != nil {
			return err
		}

		metadata, _ := json.Marshal(map[string]string{"email": user.Email, "tenant": tenantSlug})
		if _, err := tx.Exec(ctx,
			`INSERT INTO audit_log (tenant_id, actor_id, action, entity_type, entity_id, metadata)
			 VALUES ($1, $2, 'auth.registered', 'user', $2, $3)`,
			tenantID, user.ID, metadata,
		); err != nil {
			return fmt.Errorf("write audit log: %w", err)
		}

		membership = Membership{
			TenantID:   tenantID,
			TenantName: tenantName,
			TenantSlug: tenantSlug,
			Role:       rbac.RoleOwner,
		}
		return nil
	})

	return user, membership, err
}

type refreshRecord struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	TenantID  uuid.UUID
	TokenHash string
	FamilyID  uuid.UUID
	UserAgent string
	ExpiresAt time.Time
	RevokedAt *time.Time
}

func (s *Store) CreateRefreshToken(ctx context.Context, t refreshRecord) error {
	return insertRefreshToken(ctx, s.db.Pool, t)
}

type refreshQuerier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

func insertRefreshToken(ctx context.Context, q refreshQuerier, t refreshRecord) error {
	_, err := q.Exec(ctx,
		`INSERT INTO refresh_tokens
		    (id, user_id, tenant_id, token_hash, family_id, user_agent, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		t.ID, t.UserID, t.TenantID, t.TokenHash, t.FamilyID, t.UserAgent, t.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("insert refresh token: %w", err)
	}
	return nil
}

func (s *Store) RefreshTokenByHash(ctx context.Context, hash string) (refreshRecord, error) {
	var t refreshRecord
	err := s.db.Pool.QueryRow(ctx,
		`SELECT id, user_id, tenant_id, token_hash, family_id, user_agent, expires_at, revoked_at
		 FROM refresh_tokens WHERE token_hash = $1`, hash,
	).Scan(&t.ID, &t.UserID, &t.TenantID, &t.TokenHash, &t.FamilyID, &t.UserAgent, &t.ExpiresAt, &t.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return refreshRecord{}, ErrInvalidSession
	}
	return t, err
}

// RotateRefreshToken checks membership, ensures the old token is still usable,
// inserts the replacement, and revokes the old token — all atomically.
func (s *Store) RotateRefreshToken(ctx context.Context, old refreshRecord, replacement refreshRecord) (Membership, error) {
	var membership Membership

	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		var revokedAt *time.Time
		var expiresAt time.Time
		var userID, tenantID, familyID uuid.UUID
		err := tx.QueryRow(ctx,
			`SELECT user_id, tenant_id, family_id, expires_at, revoked_at
			 FROM refresh_tokens WHERE id = $1 FOR UPDATE`, old.ID,
		).Scan(&userID, &tenantID, &familyID, &expiresAt, &revokedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidSession
		}
		if err != nil {
			return fmt.Errorf("lock refresh token: %w", err)
		}
		if revokedAt != nil || time.Now().UTC().After(expiresAt) {
			return ErrInvalidSession
		}

		replacement.UserID = userID
		replacement.TenantID = tenantID
		replacement.FamilyID = familyID
		if err := insertRefreshToken(ctx, tx, replacement); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx,
			`UPDATE refresh_tokens SET revoked_at = now(), replaced_by = $1
			 WHERE id = $2 AND revoked_at IS NULL`,
			replacement.ID, old.ID,
		); err != nil {
			return fmt.Errorf("revoke old refresh token: %w", err)
		}

		if _, err := tx.Exec(ctx,
			`SELECT set_config('app.tenant_id', $1, true), set_config('app.user_id', $2, true)`,
			tenantID.String(), userID.String(),
		); err != nil {
			return fmt.Errorf("set tenant context: %w", err)
		}

		row := tx.QueryRow(ctx,
			`SELECT m.tenant_id, t.name, t.slug, m.role
			 FROM memberships m
			 JOIN tenants t ON t.id = m.tenant_id
			 WHERE m.tenant_id = $1 AND m.user_id = $2`, tenantID, userID)
		membership, err = scanMembership(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoMembership
		}
		if err != nil {
			return err
		}

		return nil
	})

	return membership, err
}

func (s *Store) RevokeRefreshToken(ctx context.Context, hash string) error {
	_, err := s.db.Pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now()
		 WHERE token_hash = $1 AND revoked_at IS NULL`, hash)
	if err != nil {
		return fmt.Errorf("revoke refresh token: %w", err)
	}
	return nil
}

func (s *Store) RevokeFamily(ctx context.Context, familyID uuid.UUID) error {
	_, err := s.db.Pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now()
		 WHERE family_id = $1 AND revoked_at IS NULL`, familyID)
	if err != nil {
		return fmt.Errorf("revoke refresh token family: %w", err)
	}
	return nil
}

func (s *Store) RevokeTokenForUser(ctx context.Context, hash string, userID uuid.UUID) error {
	_, err := s.db.Pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now()
		 WHERE token_hash = $1 AND user_id = $2 AND revoked_at IS NULL`, hash, userID)
	if err != nil {
		return fmt.Errorf("revoke refresh token: %w", err)
	}
	return nil
}

func scanMembership(scanner interface{ Scan(dest ...any) error }) (Membership, error) {
	var m Membership
	var role string
	if err := scanner.Scan(&m.TenantID, &m.TenantName, &m.TenantSlug, &role); err != nil {
		return Membership{}, err
	}
	m.Role = rbac.Role(role)
	return m, nil
}

func isConstraint(err error, name string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == name
}
