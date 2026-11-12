package inventory

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/joyaljoy/waypoint/internal/auth"
	"github.com/joyaljoy/waypoint/internal/platform/idempotency"
	"github.com/joyaljoy/waypoint/internal/rbac"
)

type Handler struct {
	svc    *Service
	auth   *auth.Service
	logger *slog.Logger
}

func NewHandler(svc *Service, authSvc *auth.Service, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, auth: authSvc, logger: logger}
}

func (h *Handler) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-stock",
		Method:      http.MethodGet,
		Path:        "/v1/inventory",
		Summary:     "List stock levels",
		Tags:        []string{"inventory"},
		Middlewares: h.auth.Protect(api, rbac.PermInventoryRead),
	}, h.listStock)

	huma.Register(api, huma.Operation{
		OperationID: "list-stock-movements",
		Method:      http.MethodGet,
		Path:        "/v1/inventory/movements",
		Summary:     "List the append-only stock ledger",
		Tags:        []string{"inventory"},
		Middlewares: h.auth.Protect(api, rbac.PermInventoryRead),
	}, h.listMovements)

	huma.Register(api, huma.Operation{
		OperationID: "adjust-stock",
		Method:      http.MethodPost,
		Path:        "/v1/inventory/adjust",
		Summary:     "Receive, return, or adjust stock",
		Tags:        []string{"inventory"},
		Middlewares: h.auth.Protect(api, rbac.PermInventoryWrite),
		Errors:      []int{400, 404, 409},
	}, h.adjust)
}

type stockListOutput struct {
	Body struct {
		Items  []StockLevel `json:"items"`
		Total  int64        `json:"total"`
		Limit  int          `json:"limit"`
		Offset int          `json:"offset"`
	}
}

type stockOutput struct {
	Body StockLevel
}

type movementsOutput struct {
	Body struct {
		Items []Movement `json:"items"`
	}
}

type listStockInput struct {
	WarehouseID string `query:"warehouse_id" format:"uuid"`
	Query       string `query:"query" maxLength:"100"`
	Limit       int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
	Offset      int    `query:"offset" default:"0" minimum:"0"`
}

type listMovementsInput struct {
	WarehouseID string `query:"warehouse_id" format:"uuid"`
	ProductID   string `query:"product_id" format:"uuid"`
	Limit       int    `query:"limit" default:"100" minimum:"1" maximum:"500"`
}

type adjustInput struct {
	IdempotencyKey string `header:"Idempotency-Key" maxLength:"128" doc:"Optional key that makes retries safe"`
	Body           struct {
		WarehouseID string `json:"warehouse_id" required:"true" format:"uuid"`
		ProductID   string `json:"product_id" required:"true" format:"uuid"`
		Kind        string `json:"kind" required:"true" enum:"receipt,return,adjustment"`
		Quantity    int    `json:"quantity" required:"true"`
		Reason      string `json:"reason" required:"true" minLength:"3" maxLength:"500"`
	}
}

func (h *Handler) listStock(ctx context.Context, input *listStockInput) (*stockListOutput, error) {
	identity, _ := auth.IdentityFrom(ctx)
	warehouseID, err := optionalUUID(input.WarehouseID)
	if err != nil {
		return nil, huma.Error400BadRequest("warehouse_id must be a valid UUID")
	}

	items, total, err := h.svc.ListStock(ctx, identity, warehouseID, input.Query, input.Limit, input.Offset)
	if err != nil {
		return nil, h.fail(err)
	}
	if items == nil {
		items = []StockLevel{}
	}
	out := &stockListOutput{}
	out.Body.Items = items
	out.Body.Total = total
	out.Body.Limit = input.Limit
	out.Body.Offset = input.Offset
	return out, nil
}

func (h *Handler) listMovements(ctx context.Context, input *listMovementsInput) (*movementsOutput, error) {
	identity, _ := auth.IdentityFrom(ctx)
	warehouseID, err := optionalUUID(input.WarehouseID)
	if err != nil {
		return nil, huma.Error400BadRequest("warehouse_id must be a valid UUID")
	}
	productID, err := optionalUUID(input.ProductID)
	if err != nil {
		return nil, huma.Error400BadRequest("product_id must be a valid UUID")
	}

	items, err := h.svc.ListMovements(ctx, identity, warehouseID, productID, input.Limit)
	if err != nil {
		return nil, h.fail(err)
	}
	if items == nil {
		items = []Movement{}
	}
	out := &movementsOutput{}
	out.Body.Items = items
	return out, nil
}

func (h *Handler) adjust(ctx context.Context, input *adjustInput) (*stockOutput, error) {
	identity, _ := auth.IdentityFrom(ctx)
	warehouseID, err := uuid.Parse(input.Body.WarehouseID)
	if err != nil {
		return nil, huma.Error400BadRequest("warehouse_id must be a valid UUID")
	}
	productID, err := uuid.Parse(input.Body.ProductID)
	if err != nil {
		return nil, huma.Error400BadRequest("product_id must be a valid UUID")
	}

	stock, _, err := h.svc.Adjust(ctx, identity, AdjustParams{
		WarehouseID: warehouseID,
		ProductID:   productID,
		Kind:        input.Body.Kind,
		Quantity:    input.Body.Quantity,
		Reason:      input.Body.Reason,
	}, input.IdempotencyKey)
	if err != nil {
		return nil, h.fail(err)
	}
	return &stockOutput{Body: stock}, nil
}

func (h *Handler) fail(err error) error {
	switch {
	case errors.Is(err, ErrValidation):
		return huma.Error400BadRequest(strings.TrimPrefix(err.Error(), ErrValidation.Error()+": "))
	case errors.Is(err, ErrNotFound):
		return huma.Error404NotFound(strings.TrimPrefix(err.Error(), ErrNotFound.Error()+": "))
	case errors.Is(err, ErrInsufficientStock):
		return huma.Error409Conflict(strings.TrimPrefix(err.Error(), ErrInsufficientStock.Error()+": "))
	case errors.Is(err, idempotency.ErrConflict):
		return huma.Error409Conflict(strings.TrimPrefix(err.Error(), idempotency.ErrConflict.Error()+": "))
	case errors.Is(err, idempotency.ErrInProgress):
		return huma.Error409Conflict("a request with this idempotency key is still in progress")
	default:
		h.logger.Error("inventory request failed", "error", err)
		return huma.Error500InternalServerError("internal server error")
	}
}

func optionalUUID(raw string) (*uuid.UUID, error) {
	if raw == "" {
		return nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, err
	}
	return &id, nil
}
