package audit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Write records an audit entry in the caller's transaction. Keeping it in the
// same transaction means a business change can never commit without its audit
// trail.
func Write(ctx context.Context, tx pgx.Tx, tenantID, actorID uuid.UUID, action, entityType string, entityID uuid.UUID, metadata any) error {
	payload := []byte("{}")
	if metadata != nil {
		b, err := json.Marshal(metadata)
		if err != nil {
			return fmt.Errorf("marshal audit metadata: %w", err)
		}
		payload = b
	}

	_, err := tx.Exec(ctx,
		`INSERT INTO audit_log (tenant_id, actor_id, action, entity_type, entity_id, metadata)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		tenantID, actorID, action, entityType, entityID, payload,
	)
	if err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}
	return nil
}
