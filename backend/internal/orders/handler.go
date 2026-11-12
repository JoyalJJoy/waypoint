package orders

import (
	"context"
	"errors"
	"fmt"
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
		OperationID:   "create-order",
		Method:        http.MethodPost,
		Path:          "/v1/orders",
		Summary:       "Create an order",
		Tags:          []string{"orders"},
		Middlewares:   h.auth.Protect(api, rbac.PermOrdersCreate),
		DefaultStatus: http.StatusCreated,
		Errors:        []int{400, 404, 409},
	}, h.create)

	huma.Register(api, huma.Operation{
		OperationID: "list-orders",
		Method:      http.MethodGet,
		Path:        "/v1/orders",
		Summary:     "List orders",
		Tags:        []string{"orders"},
		Middlewares: h.auth.Protect(api, rbac.PermOrdersRead),
	}, h.list)

	huma.Register(api, huma.Operation{
		OperationID: "get-order",
		Method:      http.MethodGet,
		Path:        "/v1/orders/{id}",
		Summary:     "Get an order with items and lifecycle events",
		Tags:        []string{"orders"},
		Middlewares: h.auth.Protect(api, rbac.PermOrdersRead),
		Errors:      []int{404},
	}, h.get)

	huma.Register(api, huma.Operation{
		OperationID: "confirm-order",
		Method:      http.MethodPost,
		Path:        "/v1/orders/{id}/confirm",
		Summary:     "Confirm an order and reserve stock atomically",
		Tags:        []string{"orders"},
		Middlewares: h.auth.Protect(api, rbac.PermOrdersUpdate),
		Errors:      []int{404, 409},
	}, h.confirm)

	huma.Register(api, huma.Operation{
		OperationID: "cancel-order",
		Method:      http.MethodPost,
		Path:        "/v1/orders/{id}/cancel",
		Summary:     "Cancel an order and release held stock",
		Tags:        []string{"orders"},
		Middlewares: h.auth.Protect(api, rbac.PermOrdersCancel),
		Errors:      []int{400, 404, 409},
	}, h.cancel)
}

type createItemBody struct {
	ProductID string `json:"product_id" required:"true" format:"uuid"`
	Quantity  int    `json:"quantity" required:"true" minimum:"1" maximum:"10000"`
}

type createOrderInput struct {
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"128" doc:"Required key that makes retries safe"`
	Body           struct {
		WarehouseID string `json:"warehouse_id" required:"true" format:"uuid"`
		Customer    struct {
			Name  string `json:"name" required:"true" minLength:"2" maxLength:"120"`
			Email string `json:"email" required:"true" format:"email"`
		} `json:"customer"`
		ShippingAddress ShippingAddress `json:"shipping_address"`
		// omitempty is not just cosmetic to Huma: a JSON field without it is
		// treated as required during validation.
		Notes string           `json:"notes,omitempty" maxLength:"1000"`
		Items []createItemBody `json:"items" required:"true" minItems:"1" maxItems:"100"`
	}
}

type orderOutput struct {
	Body Order
}

type ordersOutput struct {
	Body struct {
		Items  []Summary `json:"items"`
		Total  int64     `json:"total"`
		Limit  int       `json:"limit"`
		Offset int       `json:"offset"`
	}
}

type listOrdersInput struct {
	Status string `query:"status" doc:"pending, confirmed, processing, completed, or cancelled"`
	Query  string `query:"query" maxLength:"100"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
	Offset int    `query:"offset" default:"0" minimum:"0"`
}

type orderIDInput struct {
	ID string `path:"id" format:"uuid"`
}

type cancelOrderInput struct {
	ID string `path:"id" format:"uuid"`
	// A pointer body means the request body may be omitted entirely. Every
	// field inside it is optional too, so `{}` is also valid.
	Body *struct {
		Reason string `json:"reason,omitempty" maxLength:"500"`
	}
}

func (h *Handler) create(ctx context.Context, input *createOrderInput) (*orderOutput, error) {
	identity, _ := auth.IdentityFrom(ctx)

	warehouseID, err := uuid.Parse(input.Body.WarehouseID)
	if err != nil {
		return nil, huma.Error400BadRequest("warehouse_id must be a valid UUID")
	}
	items := make([]CreateItem, 0, len(input.Body.Items))
	for i, item := range input.Body.Items {
		productID, err := uuid.Parse(item.ProductID)
		if err != nil {
			return nil, huma.Error400BadRequest(fmt.Sprintf("item %d has an invalid product_id", i+1))
		}
		items = append(items, CreateItem{ProductID: productID, Quantity: item.Quantity})
	}

	order, replayed, err := h.svc.Create(ctx, identity, CreateParams{
		WarehouseID:     warehouseID,
		CustomerName:    input.Body.Customer.Name,
		CustomerEmail:   input.Body.Customer.Email,
		ShippingAddress: input.Body.ShippingAddress,
		Notes:           input.Body.Notes,
		Items:           items,
	}, input.IdempotencyKey)
	if err != nil {
		return nil, h.fail(err)
	}
	if replayed {
		// A replay returns the same representation. The status code is still
		// 201, which keeps client retry logic simple.
		h.logger.Info("replayed idempotent order creation", "order_id", order.ID)
	}
	return &orderOutput{Body: order}, nil
}

func (h *Handler) list(ctx context.Context, input *listOrdersInput) (*ordersOutput, error) {
	identity, _ := auth.IdentityFrom(ctx)
	items, total, err := h.svc.List(ctx, identity, input.Status, input.Query, input.Limit, input.Offset)
	if err != nil {
		return nil, h.fail(err)
	}
	if items == nil {
		items = []Summary{}
	}
	out := &ordersOutput{}
	out.Body.Items = items
	out.Body.Total = total
	out.Body.Limit = input.Limit
	out.Body.Offset = input.Offset
	return out, nil
}

func (h *Handler) get(ctx context.Context, input *orderIDInput) (*orderOutput, error) {
	identity, _ := auth.IdentityFrom(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid order id")
	}
	order, err := h.svc.Get(ctx, identity, id)
	if err != nil {
		return nil, h.fail(err)
	}
	return &orderOutput{Body: order}, nil
}

func (h *Handler) confirm(ctx context.Context, input *orderIDInput) (*orderOutput, error) {
	identity, _ := auth.IdentityFrom(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid order id")
	}
	order, err := h.svc.Confirm(ctx, identity, id)
	if err != nil {
		return nil, h.fail(err)
	}
	return &orderOutput{Body: order}, nil
}

func (h *Handler) cancel(ctx context.Context, input *cancelOrderInput) (*orderOutput, error) {
	identity, _ := auth.IdentityFrom(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid order id")
	}
	reason := ""
	if input.Body != nil {
		reason = input.Body.Reason
	}
	order, err := h.svc.Cancel(ctx, identity, id, reason)
	if err != nil {
		return nil, h.fail(err)
	}
	return &orderOutput{Body: order}, nil
}

func (h *Handler) fail(err error) error {
	switch {
	case errors.Is(err, ErrValidation):
		return huma.Error400BadRequest(strings.TrimPrefix(err.Error(), ErrValidation.Error()+": "))
	case errors.Is(err, ErrNotFound):
		return huma.Error404NotFound(strings.TrimPrefix(err.Error(), ErrNotFound.Error()+": "))
	case errors.Is(err, ErrInsufficientStock), errors.Is(err, ErrConflict), errors.Is(err, ErrOrderNumberConflict):
		return huma.Error409Conflict(strings.TrimPrefix(err.Error(), ErrConflict.Error()+": "))
	case errors.Is(err, idempotency.ErrConflict):
		return huma.Error409Conflict(strings.TrimPrefix(err.Error(), idempotency.ErrConflict.Error()+": "))
	case errors.Is(err, idempotency.ErrInProgress):
		return huma.Error409Conflict("a request with this idempotency key is still in progress")
	default:
		h.logger.Error("order request failed", "error", err)
		return huma.Error500InternalServerError("internal server error")
	}
}
