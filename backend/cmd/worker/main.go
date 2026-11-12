package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joyaljoy/waypoint/internal/config"
	"github.com/joyaljoy/waypoint/internal/platform/database"
	"github.com/joyaljoy/waypoint/internal/platform/logger"
	"github.com/joyaljoy/waypoint/internal/platform/outbox"
	"github.com/joyaljoy/waypoint/internal/worker"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
)

// The worker is the asynchronous half of the platform. It relays committed
// outbox rows onto NATS and consumes the resulting events. Nothing here is on
// the request path, so a slow email provider can never slow down an order.
func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	log := logger.New(cfg.Env, cfg.LogLevel)
	log.Info("starting waypoint worker", "env", cfg.Env)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	redisOpts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		log.Error("parse redis url", "error", err)
		os.Exit(1)
	}
	rdb := redis.NewClient(redisOpts)
	defer rdb.Close()

	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Error("ping redis", "error", err)
		os.Exit(1)
	}

	nc, err := nats.Connect(cfg.NATSURL,
		nats.Name("waypoint-worker"),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
	)
	if err != nil {
		log.Error("connect to nats", "error", err)
		os.Exit(1)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		log.Error("create jetstream context", "error", err)
		os.Exit(1)
	}
	if err := worker.EnsureStream(js); err != nil {
		log.Error("ensure event stream", "error", err)
		os.Exit(1)
	}

	relay := outbox.NewRelay(db, js, log)
	go relay.Run(ctx)

	consumer := worker.NewConsumer(js, rdb, cfg.SMTPAddr, log)
	if err := consumer.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("consumer stopped", "error", err)
		os.Exit(1)
	}
	log.Info("worker stopped")
}
