package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/joyaljoy/waypoint/internal/auth"
	"github.com/joyaljoy/waypoint/internal/catalog"
	"github.com/joyaljoy/waypoint/internal/config"
	"github.com/joyaljoy/waypoint/internal/inventory"
	"github.com/joyaljoy/waypoint/internal/orders"
	"github.com/joyaljoy/waypoint/internal/platform/database"
	"github.com/joyaljoy/waypoint/internal/platform/httpx"
	"github.com/joyaljoy/waypoint/internal/shipments"
	"log/slog"

	"github.com/redis/go-redis/v9"
)

type Deps struct {
	Config    config.Config
	Logger    *slog.Logger
	DB        *database.DB
	Redis     *redis.Client
	Auth      *auth.Handler
	Catalog   *catalog.Handler
	Inventory *inventory.Handler
	Orders    *orders.Handler
	Shipments *shipments.Handler
}

func New(deps Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(RequestLogger(deps.Logger))
	r.Use(originGuard(deps.Config.AllowedOrigins))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   deps.Config.AllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PATCH", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "Idempotency-Key", "X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
		defer cancel()

		if err := deps.DB.Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "degraded", "database": "unavailable"})
			return
		}
		if err := deps.Redis.Ping(ctx).Err(); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "degraded", "redis": "unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	openapi := huma.DefaultConfig("Waypoint API", "0.1.0")
	openapi.Info.Description = "Multi-tenant logistics and order management platform."
	openapi.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"cookieAuth": {
			Type:        "apiKey",
			In:          "cookie",
			Name:        httpx.AccessCookieName,
			Description: "HttpOnly session cookie set by /v1/auth/login.",
		},
	}

	api := humachi.New(r, openapi)

	// Make the raw request and response available to handlers. Cookies need the
	// raw http.ResponseWriter; Huma's typed output model is not a good fit for
	// multiple Set-Cookie headers.
	api.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
		req, w := humachi.Unwrap(ctx)
		ctx = huma.WithContext(ctx, httpx.WithHTTP(ctx.Context(), req, w))
		next(ctx)
	})

	deps.Auth.Register(api)
	deps.Catalog.Register(api)
	deps.Inventory.Register(api)
	deps.Orders.Register(api)
	deps.Shipments.Register(api)

	// WebSocket upgrade lives outside Huma because it hijacks the connection.
	r.Get("/v1/track/{tracking_number}/ws", deps.Shipments.HandleTrackWS)

	return r
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
