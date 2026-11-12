package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/joyaljoy/waypoint/internal/config"
	"github.com/joyaljoy/waypoint/internal/platform/httpx"
)

type Handler struct {
	svc    *Service
	cfg    config.Config
	logger *slog.Logger
}

func NewHandler(svc *Service, cfg config.Config, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, cfg: cfg, logger: logger}
}

type sessionBody struct {
	User             User         `json:"user"`
	Tenant           Membership   `json:"tenant"`
	Memberships      []Membership `json:"memberships"`
	AccessExpiresAt  time.Time    `json:"access_expires_at"`
	RefreshExpiresAt time.Time    `json:"refresh_expires_at"`
}

type sessionOutput struct {
	Body sessionBody
}

type registerInput struct {
	Body struct {
		Name       string `json:"name" required:"true" minLength:"2" maxLength:"120" example:"Ada Lovelace"`
		Email      string `json:"email" required:"true" format:"email" example:"ada@example.com"`
		Password   string `json:"password" required:"true" minLength:"8" maxLength:"128" example:"correct-horse-battery-staple"`
		TenantName string `json:"tenant_name" required:"true" minLength:"2" maxLength:"120" example:"Analytical Engines Ltd"`
	}
}

type loginInput struct {
	Body struct {
		Email    string `json:"email" required:"true" format:"email" example:"ada@example.com"`
		Password string `json:"password" required:"true" example:"correct-horse-battery-staple"`
	}
}

type switchTenantInput struct {
	Body struct {
		TenantID string `json:"tenant_id" required:"true" format:"uuid"`
	}
}

type meOutput struct {
	Body struct {
		User        User         `json:"user"`
		Tenant      Membership   `json:"tenant"`
		Memberships []Membership `json:"memberships"`
	}
}

func (h *Handler) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "register",
		Method:        http.MethodPost,
		Path:          "/v1/auth/register",
		Summary:       "Create a user and their first tenant",
		Tags:          []string{"auth"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{400, 409},
	}, h.register)

	huma.Register(api, huma.Operation{
		OperationID: "login",
		Method:      http.MethodPost,
		Path:        "/v1/auth/login",
		Summary:     "Log in and set session cookies",
		Tags:        []string{"auth"},
		Errors:      []int{401, 403},
	}, h.login)

	huma.Register(api, huma.Operation{
		OperationID: "refresh-session",
		Method:      http.MethodPost,
		Path:        "/v1/auth/refresh",
		Summary:     "Rotate the refresh token and issue a new access token",
		Tags:        []string{"auth"},
		Errors:      []int{401},
	}, h.refresh)

	huma.Register(api, huma.Operation{
		OperationID:   "logout",
		Method:        http.MethodPost,
		Path:          "/v1/auth/logout",
		Summary:       "Revoke the refresh token and clear cookies",
		Tags:          []string{"auth"},
		DefaultStatus: http.StatusNoContent,
	}, h.logout)

	huma.Register(api, huma.Operation{
		OperationID: "me",
		Method:      http.MethodGet,
		Path:        "/v1/auth/me",
		Summary:     "Return the current user, tenant, and memberships",
		Tags:        []string{"auth"},
		Middlewares: h.svc.Protect(api),
		Errors:      []int{401},
	}, h.me)

	huma.Register(api, huma.Operation{
		OperationID: "switch-tenant",
		Method:      http.MethodPost,
		Path:        "/v1/auth/switch-tenant",
		Summary:     "Switch the active tenant and rotate the session",
		Tags:        []string{"auth"},
		Middlewares: h.svc.Protect(api),
		Errors:      []int{401, 403},
	}, h.switchTenant)
}

func (h *Handler) register(ctx context.Context, input *registerInput) (*sessionOutput, error) {
	session, err := h.svc.Register(ctx, RegisterParams{
		Name:       input.Body.Name,
		Email:      input.Body.Email,
		Password:   input.Body.Password,
		TenantName: input.Body.TenantName,
		UserAgent:  userAgent(ctx),
	})
	if err != nil {
		return nil, h.mapError(err)
	}
	if err := h.setCookies(ctx, session); err != nil {
		return nil, err
	}
	return &sessionOutput{Body: sessionBodyFrom(session)}, nil
}

func (h *Handler) login(ctx context.Context, input *loginInput) (*sessionOutput, error) {
	session, err := h.svc.Login(ctx, LoginParams{
		Email:     input.Body.Email,
		Password:  input.Body.Password,
		UserAgent: userAgent(ctx),
	})
	if err != nil {
		return nil, h.mapError(err)
	}
	if err := h.setCookies(ctx, session); err != nil {
		return nil, err
	}
	return &sessionOutput{Body: sessionBodyFrom(session)}, nil
}

func (h *Handler) refresh(ctx context.Context, _ *struct{}) (*sessionOutput, error) {
	session, err := h.svc.Refresh(ctx, refreshTokenFromContext(ctx), userAgent(ctx))
	if err != nil {
		return nil, h.mapError(err)
	}
	if err := h.setCookies(ctx, session); err != nil {
		return nil, err
	}
	return &sessionOutput{Body: sessionBodyFrom(session)}, nil
}

func (h *Handler) logout(ctx context.Context, _ *struct{}) (*struct{}, error) {
	hc, ok := httpx.FromContext(ctx)
	if !ok {
		return nil, huma.Error500InternalServerError("request context unavailable")
	}

	if err := h.svc.Logout(ctx, httpx.RefreshTokenFromRequest(hc.Request)); err != nil {
		h.logger.Error("logout", "error", err)
		return nil, huma.Error500InternalServerError("could not log out")
	}

	httpx.ClearAuthCookies(hc.Response, h.cfg.CookieSecure)
	return nil, nil
}

func (h *Handler) me(ctx context.Context, _ *struct{}) (*meOutput, error) {
	identity, ok := IdentityFrom(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("authentication required")
	}

	user, tenant, memberships, err := h.svc.Me(ctx, identity)
	if err != nil {
		return nil, h.mapError(err)
	}

	out := &meOutput{}
	out.Body.User = user
	out.Body.Tenant = tenant
	out.Body.Memberships = memberships
	return out, nil
}

func (h *Handler) switchTenant(ctx context.Context, input *switchTenantInput) (*sessionOutput, error) {
	identity, ok := IdentityFrom(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("authentication required")
	}

	tenantID, err := uuid.Parse(input.Body.TenantID)
	if err != nil {
		return nil, huma.Error400BadRequest("tenant_id must be a valid UUID")
	}

	session, err := h.svc.SwitchTenant(ctx, SwitchParams{
		Identity:       identity,
		TenantID:       tenantID,
		CurrentRefresh: refreshTokenFromContext(ctx),
		UserAgent:      userAgent(ctx),
	})
	if err != nil {
		return nil, h.mapError(err)
	}
	if err := h.setCookies(ctx, session); err != nil {
		return nil, err
	}
	return &sessionOutput{Body: sessionBodyFrom(session)}, nil
}

func (h *Handler) setCookies(ctx context.Context, session *Session) error {
	hc, ok := httpx.FromContext(ctx)
	if !ok {
		return huma.Error500InternalServerError("request context unavailable")
	}
	httpx.SetAuthCookies(
		hc.Response,
		httpx.CookieConfig{Secure: h.cfg.CookieSecure},
		session.AccessToken,
		session.RefreshToken,
		session.AccessExpiresAt,
		session.RefreshExpiresAt,
	)
	return nil
}

func (h *Handler) mapError(err error) error {
	switch {
	case errors.Is(err, ErrValidation):
		msg := strings.TrimPrefix(err.Error(), ErrValidation.Error()+": ")
		return huma.Error400BadRequest(msg)
	case errors.Is(err, ErrInvalidCredentials):
		return huma.Error401Unauthorized("invalid email or password")
	case errors.Is(err, ErrInvalidSession):
		return huma.Error401Unauthorized("invalid or expired session")
	case errors.Is(err, ErrEmailTaken):
		return huma.Error409Conflict("an account with this email already exists")
	case errors.Is(err, ErrNoMembership):
		return huma.Error403Forbidden("you do not have access to this tenant")
	default:
		h.logger.Error("auth request failed", "error", err)
		return huma.Error500InternalServerError("internal server error")
	}
}

func sessionBodyFrom(s *Session) sessionBody {
	memberships := s.Memberships
	if memberships == nil {
		memberships = []Membership{}
	}
	return sessionBody{
		User:             s.User,
		Tenant:           s.Tenant,
		Memberships:      memberships,
		AccessExpiresAt:  s.AccessExpiresAt,
		RefreshExpiresAt: s.RefreshExpiresAt,
	}
}

func refreshTokenFromContext(ctx context.Context) string {
	hc, ok := httpx.FromContext(ctx)
	if !ok {
		return ""
	}
	return httpx.RefreshTokenFromRequest(hc.Request)
}

func userAgent(ctx context.Context) string {
	hc, ok := httpx.FromContext(ctx)
	if !ok {
		return ""
	}
	return hc.Request.UserAgent()
}
