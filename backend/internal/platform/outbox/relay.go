package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/joyaljoy/waypoint/internal/platform/database"
	"github.com/nats-io/nats.go"
)

// EventSubjectPrefix is the root subject for every published domain event.
// The concrete subject is prefix + event type, e.g. waypoint.events.order.confirmed.
const EventSubjectPrefix = "waypoint.events."

// Envelope is the wire format published to NATS. It carries the identity a
// consumer needs to deduplicate and to scope work to a tenant.
type Envelope struct {
	EventID       uuid.UUID       `json:"event_id"`
	TenantID      uuid.UUID       `json:"tenant_id"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   uuid.UUID       `json:"aggregate_id"`
	EventType     string          `json:"event_type"`
	Payload       json.RawMessage `json:"payload"`
	OccurredAt    time.Time       `json:"occurred_at"`
}

// Relay moves committed outbox rows onto the message bus.
//
// The relay is deliberately a separate process from the API: publishing is a
// side effect that must never be able to fail a business transaction. Events
// are delivered at-least-once, so consumers must deduplicate on EventID.
type Relay struct {
	db       *database.DB
	js       nats.JetStreamContext
	logger   *slog.Logger
	interval time.Duration
	batch    int
}

func NewRelay(db *database.DB, js nats.JetStreamContext, logger *slog.Logger) *Relay {
	return &Relay{
		db:       db,
		js:       js,
		logger:   logger,
		interval: 500 * time.Millisecond,
		batch:    100,
	}
}

func (r *Relay) Run(ctx context.Context) {
	r.logger.Info("outbox relay started", "interval", r.interval)

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		if err := r.drain(ctx); err != nil && ctx.Err() == nil {
			r.logger.Error("relay drain failed", "error", err)
		}

		select {
		case <-ctx.Done():
			r.logger.Info("outbox relay stopped")
			return
		case <-ticker.C:
		}
	}
}

// drain publishes every pending event, looping until a batch comes back empty.
func (r *Relay) drain(ctx context.Context) error {
	for {
		published, err := r.publishBatch(ctx)
		if err != nil {
			return err
		}
		if published < r.batch {
			return nil
		}
	}
}

func (r *Relay) publishBatch(ctx context.Context) (int, error) {
	published := 0

	err := r.db.InTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT id, tenant_id, event_id, aggregate_type, aggregate_id,
			        event_type, payload, occurred_at
			 FROM outbox_events
			 WHERE published_at IS NULL
			 ORDER BY id
			 LIMIT $1
			 FOR UPDATE SKIP LOCKED`,
			r.batch,
		)
		if err != nil {
			return fmt.Errorf("load pending outbox events: %w", err)
		}

		type record struct {
			id            int64
			eventID       uuid.UUID
			tenantID      uuid.UUID
			aggregateType string
			aggregateID   uuid.UUID
			eventType     string
			payload       []byte
			occurredAt    time.Time
		}

		var records []record
		for rows.Next() {
			var rec record
			if err := rows.Scan(&rec.id, &rec.tenantID, &rec.eventID, &rec.aggregateType,
				&rec.aggregateID, &rec.eventType, &rec.payload, &rec.occurredAt); err != nil {
				rows.Close()
				return err
			}
			records = append(records, rec)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		for _, rec := range records {
			envelope := Envelope{
				EventID:       rec.eventID,
				TenantID:      rec.tenantID,
				AggregateType: rec.aggregateType,
				AggregateID:   rec.aggregateID,
				EventType:     rec.eventType,
				Payload:       json.RawMessage(rec.payload),
				OccurredAt:    rec.occurredAt,
			}
			body, err := json.Marshal(envelope)
			if err != nil {
				return fmt.Errorf("marshal event envelope: %w", err)
			}

			msg := &nats.Msg{
				Subject: EventSubjectPrefix + rec.eventType,
				Data:    body,
				Header:  nats.Header{},
			}
			// The message id lets JetStream drop a duplicate publish that
			// happens because a transaction committed but the ack was lost.
			msg.Header.Set(nats.MsgIdHdr, rec.eventID.String())

			if _, err := r.js.PublishMsg(msg); err != nil {
				r.logger.Error("publish event failed",
					"event_id", rec.eventID, "event_type", rec.eventType, "error", err)
				if _, updateErr := tx.Exec(ctx,
					`UPDATE outbox_events SET attempts = attempts + 1, last_error = $2 WHERE id = $1`,
					rec.id, truncate(err.Error(), 500),
				); updateErr != nil {
					r.logger.Error("record publish failure", "error", updateErr)
				}
				continue
			}

			if _, err := tx.Exec(ctx,
				`UPDATE outbox_events
				 SET published_at = now(), attempts = attempts + 1, last_error = NULL
				 WHERE id = $1`,
				rec.id,
			); err != nil {
				return fmt.Errorf("mark event published: %w", err)
			}
			published++
		}
		return nil
	})

	return published, err
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
