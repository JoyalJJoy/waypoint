package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joyaljoy/waypoint/internal/api"
	"github.com/joyaljoy/waypoint/internal/auth"
	"github.com/joyaljoy/waypoint/internal/catalog"
	"github.com/joyaljoy/waypoint/internal/config"
	"github.com/joyaljoy/waypoint/internal/inventory"
	"github.com/joyaljoy/waypoint/internal/orders"
	"github.com/joyaljoy/waypoint/internal/platform/database"
	"github.com/joyaljoy/waypoint/internal/platform/logger"
	"github.com/joyaljoy/waypoint/internal/shipments"
	"github.com/redis/go-redis/v9"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	log := logger.New(cfg.Env, cfg.LogLevel)
	log.Info("starting waypoint api", "env", cfg.Env, "addr", cfg.HTTPAddr)

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

	tokens := auth.NewTokenManager(cfg.JWTSecret, cfg.AccessTokenTTL)
	authService := auth.NewService(db, tokens, log, cfg.AccessTokenTTL, cfg.RefreshTokenTTL)
	authHandler := auth.NewHandler(authService, cfg, log)

	catalogService := catalog.NewService(db)
	catalogHandler := catalog.NewHandler(catalogService, authService, log)

	inventoryService := inventory.NewService(db)
	inventoryHandler := inventory.NewHandler(inventoryService, authService, log)

	orderService := orders.NewService(db)
	orderHandler := orders.NewHandler(orderService, authService, log)

	shipmentService := shipments.NewService(db)
	shipmentHandler := shipments.NewHandler(
		shipmentService, authService, rdb, shipments.NewHub(), cfg.AllowedOrigins, log,
	)

	handler := api.New(api.Deps{
		Config:    cfg,
		Logger:    log,
		DB:        db,
		Redis:     rdb,
		Auth:      authHandler,
		Catalog:   catalogHandler,
		Inventory: inventoryHandler,
		Orders:    orderHandler,
		Shipments: shipmentHandler,
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("http server listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("shutdown signal received")
	case err := <-errCh:
		log.Error("http server failed", "error", err)
		os.Exit(1)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", "error", err)
	}
	log.Info("api stopped")
}
