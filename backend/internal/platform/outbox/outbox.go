package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Event struct {
	TenantID      uuid.UUID
	EventID       uuid.UUID
	AggregateType string
	AggregateID   uuid.UUID
	EventType     string
	Payload       any
	OccurredAt    time.Time
}

// Insert writes an event into the transactional outbox. Callers must use the
// same transaction as the business change. A relay publishes these rows to NATS
// at-least-once; consumers deduplicate by EventID.
func Insert(ctx context.Context, tx pgx.Tx, e Event) error {
	if e.EventID == uuid.Nil {
		e.EventID = uuid.New()
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now().UTC()
	}

	payload, err := json.Marshal(e.Payload)
	if err != nil {
		return fmt.Errorf("marshal outbox payload: %w", err)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO outbox_events
		    (tenant_id, event_id, aggregate_type, aggregate_id, event_type, payload, occurred_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		e.TenantID, e.EventID, e.AggregateType, e.AggregateID, e.EventType, payload, e.OccurredAt,
	)
	if err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}
	return nil
}
