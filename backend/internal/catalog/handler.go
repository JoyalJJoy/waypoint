package catalog

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/joyaljoy/waypoint/internal/auth"
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
		OperationID: "list-warehouses",
		Method:      http.MethodGet,
		Path:        "/v1/warehouses",
		Summary:     "List warehouses for the active tenant",
		Tags:        []string{"catalog"},
		Middlewares: h.auth.Protect(api, rbac.PermCatalogRead),
	}, h.listWarehouses)

	huma.Register(api, huma.Operation{
		OperationID:   "create-warehouse",
		Method:        http.MethodPost,
		Path:          "/v1/warehouses",
		Summary:       "Create a warehouse",
		Tags:          []string{"catalog"},
		Middlewares:   h.auth.Protect(api, rbac.PermCatalogWrite),
		DefaultStatus: http.StatusCreated,
		Errors:        []int{400, 409},
	}, h.createWarehouse)

	huma.Register(api, huma.Operation{
		OperationID: "get-warehouse",
		Method:      http.MethodGet,
		Path:        "/v1/warehouses/{id}",
		Summary:     "Get a warehouse",
		Tags:        []string{"catalog"},
		Middlewares: h.auth.Protect(api, rbac.PermCatalogRead),
		Errors:      []int{404},
	}, h.getWarehouse)

	huma.Register(api, huma.Operation{
		OperationID: "list-products",
		Method:      http.MethodGet,
		Path:        "/v1/products",
		Summary:     "Search products",
		Tags:        []string{"catalog"},
		Middlewares: h.auth.Protect(api, rbac.PermCatalogRead),
	}, h.listProducts)

	huma.Register(api, huma.Operation{
		OperationID:   "create-product",
		Method:        http.MethodPost,
		Path:          "/v1/products",
		Summary:       "Create a product",
		Tags:          []string{"catalog"},
		Middlewares:   h.auth.Protect(api, rbac.PermCatalogWrite),
		DefaultStatus: http.StatusCreated,
		Errors:        []int{400, 409},
	}, h.createProduct)

	huma.Register(api, huma.Operation{
		OperationID: "get-product",
		Method:      http.MethodGet,
		Path:        "/v1/products/{id}",
		Summary:     "Get a product",
		Tags:        []string{"catalog"},
		Middlewares: h.auth.Protect(api, rbac.PermCatalogRead),
		Errors:      []int{404},
	}, h.getProduct)
}

type warehouseListOutput struct {
	Body struct {
		Items []Warehouse `json:"items"`
	}
}

type warehouseOutput struct {
	Body Warehouse
}

type createWarehouseInput struct {
	Body struct {
		Name      string   `json:"name" required:"true" minLength:"2" maxLength:"120"`
		Code      string   `json:"code" required:"true" minLength:"2" maxLength:"32"`
		Address   Address  `json:"address"`
		Latitude  *float64 `json:"latitude,omitempty"`
		Longitude *float64 `json:"longitude,omitempty"`
	}
}

type warehouseIDInput struct {
	ID string `path:"id" format:"uuid"`
}

func (h *Handler) listWarehouses(ctx context.Context, _ *struct{}) (*warehouseListOutput, error) {
	identity, _ := auth.IdentityFrom(ctx)
	items, err := h.svc.ListWarehouses(ctx, identity)
	if err != nil {
		return nil, h.fail(err)
	}
	if items == nil {
		items = []Warehouse{}
	}
	return &warehouseListOutput{Body: struct {
		Items []Warehouse `json:"items"`
	}{Items: items}}, nil
}

func (h *Handler) createWarehouse(ctx context.Context, input *createWarehouseInput) (*warehouseOutput, error) {
	identity, _ := auth.IdentityFrom(ctx)
	w, err := h.svc.CreateWarehouse(ctx, identity, Warehouse{
		Name:      input.Body.Name,
		Code:      input.Body.Code,
		Address:   input.Body.Address,
		Latitude:  input.Body.Latitude,
		Longitude: input.Body.Longitude,
		IsActive:  true,
	})
	if err != nil {
		return nil, h.fail(err)
	}
	return &warehouseOutput{Body: w}, nil
}

func (h *Handler) getWarehouse(ctx context.Context, input *warehouseIDInput) (*warehouseOutput, error) {
	identity, _ := auth.IdentityFrom(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid warehouse id")
	}
	w, err := h.svc.GetWarehouse(ctx, identity, id)
	if err != nil {
		return nil, h.fail(err)
	}
	return &warehouseOutput{Body: w}, nil
}

type productListOutput struct {
	Body struct {
		Items  []Product `json:"items"`
		Total  int64     `json:"total"`
		Limit  int       `json:"limit"`
		Offset int       `json:"offset"`
	}
}

type productOutput struct {
	Body Product
}

type listProductsInput struct {
	Query  string `query:"query" maxLength:"100"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
	Offset int    `query:"offset" default:"0" minimum:"0"`
}

type createProductInput struct {
	Body struct {
		SKU            string `json:"sku" required:"true" minLength:"2" maxLength:"64"`
		Name           string `json:"name" required:"true" minLength:"1" maxLength:"200"`
		Description    string `json:"description" maxLength:"2000"`
		UnitPriceCents int64  `json:"unit_price_cents" required:"true" minimum:"0"`
		Currency       string `json:"currency" required:"true" minLength:"3" maxLength:"3" example:"USD"`
		WeightGrams    *int   `json:"weight_grams,omitempty" minimum:"0"`
	}
}

type productIDInput struct {
	ID string `path:"id" format:"uuid"`
}

func (h *Handler) listProducts(ctx context.Context, input *listProductsInput) (*productListOutput, error) {
	identity, _ := auth.IdentityFrom(ctx)
	items, total, err := h.svc.ListProducts(ctx, identity, input.Query, input.Limit, input.Offset)
	if err != nil {
		return nil, h.fail(err)
	}
	if items == nil {
		items = []Product{}
	}
	out := &productListOutput{}
	out.Body.Items = items
	out.Body.Total = total
	out.Body.Limit = input.Limit
	out.Body.Offset = input.Offset
	return out, nil
}

func (h *Handler) createProduct(ctx context.Context, input *createProductInput) (*productOutput, error) {
	identity, _ := auth.IdentityFrom(ctx)
	p, err := h.svc.CreateProduct(ctx, identity, Product{
		SKU:            input.Body.SKU,
		Name:           input.Body.Name,
		Description:    input.Body.Description,
		UnitPriceCents: input.Body.UnitPriceCents,
		Currency:       input.Body.Currency,
		WeightGrams:    input.Body.WeightGrams,
		IsActive:       true,
	})
	if err != nil {
		return nil, h.fail(err)
	}
	return &productOutput{Body: p}, nil
}

func (h *Handler) getProduct(ctx context.Context, input *productIDInput) (*productOutput, error) {
	identity, _ := auth.IdentityFrom(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid product id")
	}
	p, err := h.svc.GetProduct(ctx, identity, id)
	if err != nil {
		return nil, h.fail(err)
	}
	return &productOutput{Body: p}, nil
}

func (h *Handler) fail(err error) error {
	switch {
	case errors.Is(err, ErrValidation):
		return huma.Error400BadRequest(strings.TrimPrefix(err.Error(), ErrValidation.Error()+": "))
	case errors.Is(err, ErrNotFound):
		return huma.Error404NotFound("resource not found")
	case errors.Is(err, ErrConflict):
		return huma.Error409Conflict(strings.TrimPrefix(err.Error(), ErrConflict.Error()+": "))
	default:
		h.logger.Error("catalog request failed", "error", err)
		return huma.Error500InternalServerError("internal server error")
	}
}
