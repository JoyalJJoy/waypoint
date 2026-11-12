package auth

import (
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/joyaljoy/waypoint/internal/platform/httpx"
	"github.com/joyaljoy/waypoint/internal/rbac"
)

// Protect returns Huma operation middleware that authenticates the session,
// reloads the membership from the database, and checks the required
// permissions. Reloading membership on each request means a revoked user or a
// downgraded role takes effect immediately instead of waiting for token expiry.
func (s *Service) Protect(api huma.API, perms ...rbac.Permission) huma.Middlewares {
	authenticate := func(ctx huma.Context, next func(huma.Context)) {
		hc, ok := httpx.FromContext(ctx.Context())
		if !ok {
			_ = huma.WriteErr(api, ctx, http.StatusInternalServerError, "request context unavailable")
			return
		}

		identity, err := s.tokens.ParseAccess(httpx.TokenFromRequest(hc.Request))
		if err != nil {
			_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, "authentication required")
			return
		}

		membership, err := s.store.Membership(ctx.Context(), identity.TenantID, identity.UserID)
		if err != nil {
			if errors.Is(err, ErrNoMembership) {
				_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, "session is no longer valid")
				return
			}
			s.logger.Error("load membership", "error", err)
			_ = huma.WriteErr(api, ctx, http.StatusInternalServerError, "internal server error")
			return
		}

		identity.Role = membership.Role
		ctx = huma.WithContext(ctx, WithIdentity(ctx.Context(), identity))
		next(ctx)
	}

	authorize := func(ctx huma.Context, next func(huma.Context)) {
		identity, ok := IdentityFrom(ctx.Context())
		if !ok {
			_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, "authentication required")
			return
		}
		for _, p := range perms {
			if !identity.Role.Can(p) {
				_ = huma.WriteErr(api, ctx, http.StatusForbidden, "you do not have permission to perform this action")
				return
			}
		}
		next(ctx)
	}

	return huma.Middlewares{authenticate, authorize}
}
