package shipments

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/joyaljoy/waypoint/internal/auth"
	"github.com/joyaljoy/waypoint/internal/rbac"
	"github.com/redis/go-redis/v9"
)

const trackingCacheTTL = 5 * time.Second

type Handler struct {
	svc     *Service
	auth    *auth.Service
	redis   *redis.Client
	hub     *Hub
	origins []string
	logger  *slog.Logger
}

func NewHandler(svc *Service, authSvc *auth.Service, rdb *redis.Client, hub *Hub, allowedOrigins []string, logger *slog.Logger) *Handler {
	return &Handler{
		svc:     svc,
		auth:    authSvc,
		redis:   rdb,
		hub:     hub,
		origins: hostPatterns(allowedOrigins),
		logger:  logger,
	}
}

func (h *Handler) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-shipment",
		Method:        http.MethodPost,
		Path:          "/v1/shipments",
		Summary:       "Create a shipment for a confirmed order",
		Tags:          []string{"shipments"},
		Middlewares:   h.auth.Protect(api, rbac.PermShipmentsWrite),
		DefaultStatus: http.StatusCreated,
		Errors:        []int{400, 404, 409},
	}, h.create)

	huma.Register(api, huma.Operation{
		OperationID: "list-shipments",
		Method:      http.MethodGet,
		Path:        "/v1/shipments",
		Summary:     "List shipments",
		Tags:        []string{"shipments"},
		Middlewares: h.auth.Protect(api, rbac.PermShipmentsRead),
	}, h.list)

	huma.Register(api, huma.Operation{
		OperationID: "get-shipment",
		Method:      http.MethodGet,
		Path:        "/v1/shipments/{id}",
		Summary:     "Get a shipment with its tracking timeline",
		Tags:        []string{"shipments"},
		Middlewares: h.auth.Protect(api, rbac.PermShipmentsRead),
		Errors:      []int{404},
	}, h.get)

	huma.Register(api, huma.Operation{
		OperationID: "assign-shipment",
		Method:      http.MethodPost,
		Path:        "/v1/shipments/{id}/assign",
		Summary:     "Assign a delivery agent to a shipment",
		Tags:        []string{"shipments"},
		Middlewares: h.auth.Protect(api, rbac.PermShipmentsAssign),
		Errors:      []int{400, 404, 409},
	}, h.assign)

	huma.Register(api, huma.Operation{
		OperationID: "transition-shipment",
		Method:      http.MethodPost,
		Path:        "/v1/shipments/{id}/status",
		Summary:     "Move a shipment through its delivery lifecycle",
		Tags:        []string{"shipments"},
		Middlewares: h.auth.Protect(api, rbac.PermShipmentsWrite),
		Errors:      []int{400, 403, 404, 409},
	}, h.transition)

	huma.Register(api, huma.Operation{
		OperationID: "list-delivery-agents",
		Method:      http.MethodGet,
		Path:        "/v1/agents",
		Summary:     "List delivery agents in the tenant",
		Tags:        []string{"shipments"},
		Middlewares: h.auth.Protect(api, rbac.PermShipmentsAssign),
	}, h.listAgents)

	// Public endpoints. Tracking numbers are unguessable bearer tokens, so they
	// work without a session on the customer-facing page.
	huma.Register(api, huma.Operation{
		OperationID: "public-tracking",
		Method:      http.MethodGet,
		Path:        "/v1/track/{tracking_number}",
		Summary:     "Public shipment tracking snapshot",
		Tags:        []string{"tracking"},
		Errors:      []int{404},
	}, h.track)
}

type shipmentOutput struct {
	Body Shipment
}

type shipmentsOutput struct {
	Body struct {
		Items  []ShipmentSummary `json:"items"`
		Total  int64             `json:"total"`
		Limit  int               `json:"limit"`
		Offset int               `json:"offset"`
	}
}

type agentsOutput struct {
	Body struct {
		Items []Agent `json:"items"`
	}
}

type trackingOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         PublicTracking
}

type createShipmentInput struct {
	Body struct {
		OrderID             string     `json:"order_id" required:"true" format:"uuid"`
		Carrier             string     `json:"carrier" required:"true" minLength:"1" maxLength:"80"`
		ServiceLevel        string     `json:"service_level,omitempty" maxLength:"40" doc:"ground, expedited, overnight, or freight"`
		EstimatedDeliveryAt *time.Time `json:"estimated_delivery_at,omitempty"`
	}
}

type shipmentIDInput struct {
	ID string `path:"id" format:"uuid"`
}

type assignShipmentInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		AgentID string `json:"agent_id" required:"true" format:"uuid"`
	}
}

type transitionShipmentInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Status    string   `json:"status" required:"true" doc:"assigned, picked_up, in_transit, out_for_delivery, delivered, failed, or cancelled"`
		Note      string   `json:"note,omitempty" maxLength:"500"`
		Latitude  *float64 `json:"latitude,omitempty" minimum:"-90" maximum:"90"`
		Longitude *float64 `json:"longitude,omitempty" minimum:"-180" maximum:"180"`
	}
}

type listShipmentsInput struct {
	Status string `query:"status" doc:"pending, assigned, picked_up, in_transit, out_for_delivery, delivered, failed, or cancelled"`
	Query  string `query:"query" maxLength:"100"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
	Offset int    `query:"offset" default:"0" minimum:"0"`
}

type trackInput struct {
	TrackingNumber string `path:"tracking_number" maxLength:"64"`
}

func (h *Handler) create(ctx context.Context, input *createShipmentInput) (*shipmentOutput, error) {
	identity, _ := auth.IdentityFrom(ctx)
	orderID, err := uuid.Parse(input.Body.OrderID)
	if err != nil {
		return nil, huma.Error400BadRequest("order_id must be a valid UUID")
	}

	shipment, err := h.svc.Create(ctx, identity, CreateParams{
		OrderID:             orderID,
		Carrier:             input.Body.Carrier,
		ServiceLevel:        input.Body.ServiceLevel,
		EstimatedDeliveryAt: input.Body.EstimatedDeliveryAt,
	})
	if err != nil {
		return nil, h.fail(err)
	}
	h.invalidate(ctx, shipment.TrackingNumber)
	h.broadcast(shipment)
	return &shipmentOutput{Body: shipment}, nil
}

func (h *Handler) list(ctx context.Context, input *listShipmentsInput) (*shipmentsOutput, error) {
	identity, _ := auth.IdentityFrom(ctx)
	items, total, err := h.svc.List(ctx, identity, input.Status, input.Query, input.Limit, input.Offset)
	if err != nil {
		return nil, h.fail(err)
	}
	if items == nil {
		items = []ShipmentSummary{}
	}
	out := &shipmentsOutput{}
	out.Body.Items = items
	out.Body.Total = total
	out.Body.Limit = input.Limit
	out.Body.Offset = input.Offset
	return out, nil
}

func (h *Handler) get(ctx context.Context, input *shipmentIDInput) (*shipmentOutput, error) {
	identity, _ := auth.IdentityFrom(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid shipment id")
	}
	shipment, err := h.svc.Get(ctx, identity, id)
	if err != nil {
		return nil, h.fail(err)
	}
	return &shipmentOutput{Body: shipment}, nil
}

func (h *Handler) assign(ctx context.Context, input *assignShipmentInput) (*shipmentOutput, error) {
	identity, _ := auth.IdentityFrom(ctx)
	shipmentID, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid shipment id")
	}
	agentID, err := uuid.Parse(input.Body.AgentID)
	if err != nil {
		return nil, huma.Error400BadRequest("agent_id must be a valid UUID")
	}

	shipment, err := h.svc.Assign(ctx, identity, shipmentID, agentID)
	if err != nil {
		return nil, h.fail(err)
	}
	h.invalidate(ctx, shipment.TrackingNumber)
	h.broadcast(shipment)
	return &shipmentOutput{Body: shipment}, nil
}

func (h *Handler) transition(ctx context.Context, input *transitionShipmentInput) (*shipmentOutput, error) {
	identity, _ := auth.IdentityFrom(ctx)
	shipmentID, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid shipment id")
	}

	shipment, err := h.svc.Transition(ctx, identity, shipmentID, TransitionParams{
		Status:    Status(strings.ToLower(strings.TrimSpace(input.Body.Status))),
		Note:      input.Body.Note,
		Latitude:  input.Body.Latitude,
		Longitude: input.Body.Longitude,
	})
	if err != nil {
		return nil, h.fail(err)
	}
	h.invalidate(ctx, shipment.TrackingNumber)
	h.broadcast(shipment)
	return &shipmentOutput{Body: shipment}, nil
}

func (h *Handler) listAgents(ctx context.Context, _ *struct{}) (*agentsOutput, error) {
	identity, _ := auth.IdentityFrom(ctx)
	agents, err := h.svc.ListAgents(ctx, identity)
	if err != nil {
		return nil, h.fail(err)
	}
	if agents == nil {
		agents = []Agent{}
	}
	out := &agentsOutput{}
	out.Body.Items = agents
	return out, nil
}

func (h *Handler) track(ctx context.Context, input *trackInput) (*trackingOutput, error) {
	tracking, err := h.cachedTracking(ctx, input.TrackingNumber)
	if err != nil {
		return nil, h.fail(err)
	}
	out := &trackingOutput{Body: tracking}
	out.CacheControl = "public, max-age=5"
	return out, nil
}

func (h *Handler) cachedTracking(ctx context.Context, trackingNumber string) (PublicTracking, error) {
	key := trackingCacheKey(trackingNumber)
	if h.redis != nil {
		if raw, err := h.redis.Get(ctx, key).Bytes(); err == nil {
			var cached PublicTracking
			if json.Unmarshal(raw, &cached) == nil {
				return cached, nil
			}
		}
	}

	tracking, err := h.svc.PublicTracking(ctx, trackingNumber)
	if err != nil {
		return PublicTracking{}, err
	}
	if h.redis != nil {
		if raw, err := json.Marshal(tracking); err == nil {
			if err := h.redis.Set(ctx, key, raw, trackingCacheTTL).Err(); err != nil {
				h.logger.Debug("cache tracking snapshot", "error", err)
			}
		}
	}
	return tracking, nil
}

func (h *Handler) invalidate(ctx context.Context, trackingNumber string) {
	if h.redis == nil {
		return
	}
	if err := h.redis.Del(context.WithoutCancel(ctx), trackingCacheKey(trackingNumber)).Err(); err != nil {
		h.logger.Debug("invalidate tracking cache", "error", err)
	}
}

type wsUpdate struct {
	Type           string    `json:"type"`
	TrackingNumber string    `json:"tracking_number"`
	Status         Status    `json:"status"`
	Description    string    `json:"description,omitempty"`
	OccurredAt     time.Time `json:"occurred_at"`
}

func (h *Handler) broadcast(s Shipment) {
	update := wsUpdate{
		Type:           "shipment.status_changed",
		TrackingNumber: s.TrackingNumber,
		Status:         s.Status,
		OccurredAt:     s.UpdatedAt,
	}
	if len(s.Events) > 0 {
		update.Description = s.Events[len(s.Events)-1].Description
	}
	payload, err := json.Marshal(update)
	if err != nil {
		return
	}
	h.hub.Publish(s.TrackingNumber, payload)
}

// HandleTrackWS serves the public tracking WebSocket. It is registered on the
// raw chi router because Huma's typed request/response model expects a normal
// HTTP response, while WebSocket upgrades hijack the connection.
func (h *Handler) HandleTrackWS(w http.ResponseWriter, r *http.Request) {
	trackingNumber := chi.URLParam(r, "tracking_number")
	snapshot, err := h.svc.PublicTracking(r.Context(), trackingNumber)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "tracking number not found")
		return
	}

	// The server-wide read/write timeouts are meant for short REST requests.
	// Clear them on this connection before upgrading, otherwise the WebSocket
	// would be torn down after 15-30 seconds.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: h.origins,
	})
	if err != nil {
		h.logger.Debug("websocket accept failed", "error", err)
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(4096)

	updates, unsubscribe := h.hub.Subscribe(trackingNumber)
	defer unsubscribe()

	ctx := conn.CloseRead(r.Context())

	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return
	}
	if err := conn.Write(ctx, websocket.MessageText, snapshotJSON); err != nil {
		return
	}

	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			conn.Close(websocket.StatusNormalClosure, "")
			return
		case payload, ok := <-updates:
			if !ok {
				return
			}
			if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
				return
			}
		case <-ping.C:
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

func (h *Handler) fail(err error) error {
	switch {
	case errors.Is(err, ErrValidation):
		return huma.Error400BadRequest(trimError(err, ErrValidation))
	case errors.Is(err, ErrNotFound):
		return huma.Error404NotFound(trimError(err, ErrNotFound))
	case errors.Is(err, ErrForbidden):
		return huma.Error403Forbidden(trimError(err, ErrForbidden))
	case errors.Is(err, ErrConflict), errors.Is(err, ErrNumberConflict):
		return huma.Error409Conflict(trimError(err, ErrConflict))
	default:
		h.logger.Error("shipment request failed", "error", err)
		return huma.Error500InternalServerError("internal server error")
	}
}

func trimError(err error, sentinel error) string {
	return strings.TrimPrefix(err.Error(), sentinel.Error()+": ")
}

func trackingCacheKey(trackingNumber string) string {
	return "waypoint:tracking:" + trackingNumber
}

// hostPatterns converts configured origins (https://app.example.com) into the
// host patterns expected by the WebSocket accept options (app.example.com).
func hostPatterns(origins []string) []string {
	out := make([]string, 0, len(origins))
	for _, origin := range origins {
		if u, err := url.Parse(origin); err == nil && u.Host != "" {
			out = append(out, u.Host)
			continue
		}
		out = append(out, strings.TrimPrefix(strings.TrimPrefix(origin, "http://"), "https://"))
	}
	return out
}

func writeJSONError(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"title":  http.StatusText(status),
		"status": status,
		"detail": detail,
	})
}
