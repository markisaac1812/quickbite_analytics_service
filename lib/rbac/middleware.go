package rbac

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/quickbite/analytics-service/lib/appcontext"
	apperr "github.com/quickbite/analytics-service/lib/errors"
)

// Guard builds RBAC middleware. Go analogue of lib/auth/rbac.ts.
// Must run after auth.Middleware.Authenticate.
type Guard struct {
	cache *Cache
	log   *slog.Logger
}

func NewGuard(cache *Cache, log *slog.Logger) *Guard {
	return &Guard{cache: cache, log: log}
}

// Require allows the request when the caller holds perm ("resource:action"):
//
//   - system_admin     → always allowed (same bypass as core/order)
//   - restaurant_user  → permissions of their restaurantRole, via the cache
//   - anyone else      → 403 FORBIDDEN
//
// If core is unreachable on a cache miss → 503 (fail closed).
func (g *Guard) Require(perm string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := appcontext.ClaimsFrom(r.Context())
			if !ok {
				apperr.Write(w, r, g.log, apperr.ErrUnauthenticated)
				return
			}

			switch claims.Role {
			case RoleSystemAdmin:
				next.ServeHTTP(w, r)
				return
			case RoleRestaurantUser:
				if claims.RestaurantRole == "" {
					apperr.Write(w, r, g.log, apperr.ErrForbidden)
					return
				}
				has, err := g.cache.Has(r.Context(), claims.RestaurantRole, perm)
				if err != nil {
					apperr.Write(w, r, g.log, err)
					return
				}
				if !has {
					apperr.Write(w, r, g.log, apperr.ErrForbidden)
					return
				}
				next.ServeHTTP(w, r)
			default:
				apperr.Write(w, r, g.log, apperr.ErrForbidden)
			}
		})
	}
}

// RequireRestaurantAccess scopes a restaurant user to their own restaurant:
// the {param} path value must equal the JWT's restaurantId. system_admin
// bypasses. Go analogue of requireRestaurantMember() in lib/auth/rbac.ts.
// Without it any owner holding analytics:read could read any restaurant.
func (g *Guard) RequireRestaurantAccess(param string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := appcontext.ClaimsFrom(r.Context())
			if !ok {
				apperr.Write(w, r, g.log, apperr.ErrUnauthenticated)
				return
			}
			if claims.Role == RoleSystemAdmin {
				next.ServeHTTP(w, r)
				return
			}

			id, err := strconv.ParseInt(chi.URLParam(r, param), 10, 64)
			if err != nil || id <= 0 {
				apperr.Write(w, r, g.log, apperr.ErrValidation.WithMessage(param+" must be a positive integer"))
				return
			}
			if claims.RestaurantID == nil || *claims.RestaurantID != id {
				apperr.Write(w, r, g.log, apperr.ErrForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireRole allows only callers whose system role is one of roles
// (e.g. platform-wide reports are system_admin only).
func (g *Guard) RequireRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := appcontext.ClaimsFrom(r.Context())
			if !ok {
				apperr.Write(w, r, g.log, apperr.ErrUnauthenticated)
				return
			}
			for _, role := range roles {
				if claims.Role == role {
					next.ServeHTTP(w, r)
					return
				}
			}
			apperr.Write(w, r, g.log, apperr.ErrForbidden)
		})
	}
}

// RequireBranchAccess scopes non-owner restaurant users to the branches in
// their JWT `branchIds`. system_admin and the restaurant owner bypass.
// Mount it AFTER RequireRestaurantAccess: an owner passes here only because
// the restaurant scope was already checked. Go analogue of
// requireBranchAccess() in order-service lib/auth/rbac.ts.
func (g *Guard) RequireBranchAccess(param string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := appcontext.ClaimsFrom(r.Context())
			if !ok {
				apperr.Write(w, r, g.log, apperr.ErrUnauthenticated)
				return
			}
			if claims.Role == RoleSystemAdmin || claims.RestaurantRole == RestaurantRoleOwner {
				next.ServeHTTP(w, r)
				return
			}

			id, err := strconv.ParseInt(chi.URLParam(r, param), 10, 64)
			if err != nil || id <= 0 {
				apperr.Write(w, r, g.log, apperr.ErrValidation.WithMessage(param+" must be a positive integer"))
				return
			}
			for _, b := range claims.BranchIDs {
				if b == id {
					next.ServeHTTP(w, r)
					return
				}
			}
			apperr.Write(w, r, g.log, apperr.ErrForbidden)
		})
	}
}
