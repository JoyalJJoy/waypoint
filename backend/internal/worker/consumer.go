package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/smtp"
	"strings"
	"time"

	"github.com/joyaljoy/waypoint/internal/platform/outbox"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
)

const (
	streamName     = "WAYPOINT_EVENTS"
	consumerName   = "waypoint-worker"
	dedupeTTL      = 7 * 24 * time.Hour
	trackingPrefix = "waypoint:tracking:"
)

// EnsureStream creates the JetStream stream if it does not exist yet. It is
// safe to call on every start.
func EnsureStream(js nats.JetStreamContext) error {
	if _, err := js.StreamInfo(streamName); err == nil {
		return nil
	} else if !errors.Is(err, nats.ErrStreamNotFound) {
		return fmt.Errorf("inspect stream: %w", err)
	}

	_, err := js.AddStream(&nats.StreamConfig{
		Name:       streamName,
		Subjects:   []string{outbox.EventSubjectPrefix + ">"},
		Storage:    nats.FileStorage,
		Retention:  nats.LimitsPolicy,
		MaxAge:     7 * 24 * time.Hour,
		Duplicates: 2 * time.Minute,
		Discard:    nats.DiscardOld,
		MaxMsgs:    1_000_000,
		Mirror:     nil,
	})
	if err != nil {
		return fmt.Errorf("create stream: %w", err)
	}
	return nil
}

// Consumer handles domain events published by the outbox relay.
//
// Delivery is at-least-once, so every handler must be safe to run twice. The
// worker records processed event IDs in Redis to avoid repeating side effects.
type Consumer struct {
	js       nats.JetStreamContext
	redis    *redis.Client
	smtpAddr string
	logger   *slog.Logger
}

func NewConsumer(js nats.JetStreamContext, rdb *redis.Client, smtpAddr string, logger *slog.Logger) *Consumer {
	return &Consumer{js: js, redis: rdb, smtpAddr: smtpAddr, logger: logger}
}

func (c *Consumer) Run(ctx context.Context) error {
	sub, err := c.js.PullSubscribe(
		outbox.EventSubjectPrefix+">",
		consumerName,
		nats.BindStream(streamName),
		nats.ManualAck(),
		nats.AckExplicit(),
		nats.DeliverAll(),
	)
	if err != nil {
		return fmt.Errorf("subscribe to events: %w", err)
	}
	defer sub.Unsubscribe()

	c.logger.Info("event consumer started", "consumer", consumerName)

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		msgs, err := sub.Fetch(20, nats.MaxWait(2*time.Second))
		if err != nil {
			if errors.Is(err, nats.ErrTimeout) || errors.Is(err, context.Canceled) {
				continue
			}
			c.logger.Error("fetch events failed", "error", err)
			time.Sleep(time.Second)
			continue
		}

		for _, msg := range msgs {
			c.process(ctx, msg)
		}
	}
}

func (c *Consumer) process(ctx context.Context, msg *nats.Msg) {
	var envelope outbox.Envelope
	if err := json.Unmarshal(msg.Data, &envelope); err != nil {
		c.logger.Error("decode event failed", "error", err)
		_ = msg.Term()
		return
	}

	handleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()

	if err := c.handle(handleCtx, envelope); err != nil {
		c.logger.Error("handle event failed",
			"event_id", envelope.EventID, "event_type", envelope.EventType, "error", err)
		// Ask for a redelivery instead of losing the event.
		_ = msg.NakWithDelay(5 * time.Second)
		return
	}

	// Only mark the event as handled after it succeeded, so a crash mid-handler
	// results in a redelivery rather than a silently dropped event.
	if c.redis != nil {
		key := "waypoint:processed:" + envelope.EventID.String()
		if err := c.redis.SetNX(handleCtx, key, envelope.EventType, dedupeTTL).Err(); err != nil {
			c.logger.Error("record processed event failed", "error", err)
		}
	}

	_ = msg.Ack()
	c.logger.Info("event handled", "event_type", envelope.EventType, "event_id", envelope.EventID)
}

func (c *Consumer) handle(ctx context.Context, envelope outbox.Envelope) error {
	switch envelope.EventType {
	case "order.confirmed":
		return c.sendOrderEmail(ctx, envelope, "confirmed")
	case "order.cancelled":
		return c.sendOrderEmail(ctx, envelope, "cancelled")
	case "shipment.created", "shipment.assigned", "shipment.status_changed":
		return c.invalidateTracking(ctx, envelope)
	default:
		c.logger.Debug("no handler for event", "event_type", envelope.EventType)
		return nil
	}
}

type orderPayload struct {
	OrderID       string `json:"order_id"`
	OrderNumber   string `json:"order_number"`
	Status        string `json:"status"`
	CustomerEmail string `json:"customer_email"`
	Currency      string `json:"currency"`
	TotalCents    int64  `json:"total_cents"`
	Items         []struct {
		SKU      string `json:"sku"`
		Quantity int    `json:"quantity"`
	} `json:"items"`
}

// invalidateTracking drops the cached public snapshot so the tracking page
// reflects the new status immediately instead of after the cache TTL.
func (c *Consumer) invalidateTracking(ctx context.Context, envelope outbox.Envelope) error {
	if c.redis == nil {
		return nil
	}
	var payload struct {
		TrackingNumber string `json:"tracking_number"`
	}
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return nil
	}
	if payload.TrackingNumber == "" {
		return nil
	}
	if err := c.redis.Del(ctx, trackingPrefix+payload.TrackingNumber).Err(); err != nil {
		return fmt.Errorf("invalidate tracking cache: %w", err)
	}
	return nil
}

func (c *Consumer) sendOrderEmail(ctx context.Context, envelope outbox.Envelope, kind string) error {
	if c.smtpAddr == "" {
		c.logger.Debug("smtp not configured, skipping email", "event_type", envelope.EventType)
		return nil
	}

	var payload orderPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return fmt.Errorf("decode order payload: %w", err)
	}
	if payload.CustomerEmail == "" {
		return nil
	}

	subject := fmt.Sprintf("Order %s %s", payload.OrderNumber, kind)
	var body strings.Builder
	fmt.Fprintf(&body, "Hello,\n\n")
	fmt.Fprintf(&body, "your order %s has been %s.\n\n", payload.OrderNumber, kind)
	if payload.TotalCents > 0 {
		fmt.Fprintf(&body, "Total: %s %s\n\n", formatMoney(payload.TotalCents), payload.Currency)
	}
	if len(payload.Items) > 0 {
		body.WriteString("Items:\n")
		for _, item := range payload.Items {
			fmt.Fprintf(&body, "  - %s x%d\n", item.SKU, item.Quantity)
		}
		body.WriteString("\n")
	}
	body.WriteString("Thanks for your order.\n\nWaypoint\n")

	message := fmt.Sprintf(
		"From: Waypoint <no-reply@waypoint.test>\r\nTo: %s\r\nSubject: %s\r\n"+
			"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		payload.CustomerEmail, subject, body.String(),
	)

	if err := smtp.SendMail(c.smtpAddr, nil, "no-reply@waypoint.test", []string{payload.CustomerEmail}, []byte(message)); err != nil {
		return fmt.Errorf("send email: %w", err)
	}
	c.logger.Info("order email sent", "to", payload.CustomerEmail, "kind", kind)
	return nil
}

func formatMoney(cents int64) string {
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}
