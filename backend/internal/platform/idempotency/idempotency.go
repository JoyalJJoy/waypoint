package idempotency

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrConflict   = errors.New("idempotency key conflict")
	ErrInProgress = errors.New("request with this idempotency key is still in progress")
)

type ClaimResult struct {
	Replayed bool
	Body     []byte
}

// Claim must run inside the same transaction as the business operation. The
// unique insert either claims the key or blocks on the in-flight transaction,
// which serializes duplicate requests without a distributed lock.
func Claim(ctx context.Context, tx pgx.Tx, tenantID, userID uuid.UUID, key, requestHash string) (ClaimResult, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return ClaimResult{}, nil
	}

	tag, err := tx.Exec(ctx,
		`INSERT INTO idempotency_keys (tenant_id, key, user_id, request_hash)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (tenant_id, key) DO NOTHING`,
		tenantID, key, userID, requestHash,
	)
	if err != nil {
		return ClaimResult{}, fmt.Errorf("claim idempotency key: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return ClaimResult{}, nil
	}

	var existingUser uuid.UUID
	var existingHash, status string
	var body []byte
	err = tx.QueryRow(ctx,
		`SELECT user_id, request_hash, status, response_body
		 FROM idempotency_keys
		 WHERE tenant_id = $1 AND key = $2
		 FOR UPDATE`,
		tenantID, key,
	).Scan(&existingUser, &existingHash, &status, &body)
	if err != nil {
		return ClaimResult{}, fmt.Errorf("read idempotency key: %w", err)
	}

	if existingUser != userID || existingHash != requestHash {
		return ClaimResult{}, fmt.Errorf("%w: this key was already used for a different request", ErrConflict)
	}
	if status != "completed" {
		return ClaimResult{}, ErrInProgress
	}

	return ClaimResult{Replayed: true, Body: body}, nil
}

func Complete(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, key string, status int, body []byte) error {
	if strings.TrimSpace(key) == "" {
		return nil
	}
	_, err := tx.Exec(ctx,
		`UPDATE idempotency_keys
		 SET status = 'completed', response_status = $3, response_body = $4, completed_at = now()
		 WHERE tenant_id = $1 AND key = $2`,
		tenantID, key, status, body,
	)
	if err != nil {
		return fmt.Errorf("complete idempotency key: %w", err)
	}
	return nil
}
