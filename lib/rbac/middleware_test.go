package rbac

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/quickbite/analytics-service/internal/testkit/assert"
	"github.com/quickbite/analytics-service/lib/appcontext"
	apperr "github.com/quickbite/analytics-service/lib/errors"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func ptr[T any](v T) *T { return &v }

var (
	admin    = appcontext.Claims{UserID: 1, Role: RoleSystemAdmin}
	owner    = appcontext.Claims{UserID: 10, Role: RoleRestaurantUser, RestaurantID: ptr(int64(42)), RestaurantRole: RestaurantRoleOwner}
	manager  = appcontext.Claims{UserID: 11, Role: RoleRestaurantUser, RestaurantID: ptr(int64(42)), RestaurantRole: "branch_manager", BranchIDs: []int64{7}}
	staff    = appcontext.Claims{UserID: 12, Role: RoleRestaurantUser, RestaurantID: ptr(int64(42)), RestaurantRole: "staff"}
	noRole   = appcontext.Claims{UserID: 13, Role: RoleRestaurantUser, RestaurantID: ptr(int64(42))}
	noTenant = appcontext.Claims{UserID: 14, Role: RoleRestaurantUser, RestaurantRole: "owner"}
	customer = appcontext.Claims{UserID: 30, Role: "customer"}
)

// guarded mounts mw on /r/{restaurantId}/b/{branchId} and serves one request.
func guarded(mw func(http.Handler) http.Handler, path string, caller *appcontext.Claims) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.With(mw).Get("/r/{restaurantId}/b/{branchId}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if caller != nil {
		req = req.WithContext(appcontext.WithClaims(req.Context(), *caller))
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func newGuard(f *fakeFetcher) *Guard {
	return NewGuard(NewCache(f, time.Minute), discard)
}

func TestGuard_Require(t *testing.T) {
	cases := []struct {
		name   string
		caller *appcontext.Claims
		status int
	}{
		{"allows a system admin without asking core", &admin, http.StatusNoContent},
		{"allows a restaurant role holding the permission", &owner, http.StatusNoContent},
		{"forbids a restaurant role without the permission", &staff, http.StatusForbidden},
		{"forbids a restaurant user without a restaurant role", &noRole, http.StatusForbidden},
		{"forbids any other system role", &customer, http.StatusForbidden},
		{"rejects an unauthenticated request", nil, http.StatusUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			f := newFakeFetcher()
			g := newGuard(f)

			// Act
			rec := guarded(g.Require("analytics:read"), "/r/42/b/7", c.caller)

			// Assert
			assert.Equal(t, rec.Code, c.status)
		})
	}

	t.Run("answers 503 when core cannot be reached", func(t *testing.T) {
		// Arrange
		f := newFakeFetcher()
		f.err = apperr.ErrUpstreamUnavailable
		g := newGuard(f)

		// Act
		rec := guarded(g.Require("analytics:read"), "/r/42/b/7", &owner)

		// Assert
		assert.Equal(t, rec.Code, http.StatusServiceUnavailable)
	})

	t.Run("answers 500 for an unexpected fetch error", func(t *testing.T) {
		// Arrange
		f := newFakeFetcher()
		f.err = errors.New("boom")
		g := newGuard(f)

		// Act
		rec := guarded(g.Require("analytics:read"), "/r/42/b/7", &owner)

		// Assert
		assert.Equal(t, rec.Code, http.StatusInternalServerError)
	})
}

func TestGuard_RequireRestaurantAccess(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		caller *appcontext.Claims
		status int
	}{
		{"allows the restaurant's own user", "/r/42/b/7", &owner, http.StatusNoContent},
		{"allows a system admin on any restaurant", "/r/99/b/7", &admin, http.StatusNoContent},
		{"forbids another restaurant", "/r/99/b/7", &owner, http.StatusForbidden},
		{"forbids a user without a restaurant", "/r/42/b/7", &noTenant, http.StatusForbidden},
		{"rejects a non-numeric id", "/r/abc/b/7", &owner, http.StatusBadRequest},
		{"rejects a non-positive id", "/r/0/b/7", &owner, http.StatusBadRequest},
		{"rejects an unauthenticated request", "/r/42/b/7", nil, http.StatusUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			g := newGuard(newFakeFetcher())

			// Act
			rec := guarded(g.RequireRestaurantAccess("restaurantId"), c.path, c.caller)

			// Assert
			assert.Equal(t, rec.Code, c.status)
		})
	}
}

func TestGuard_RequireBranchAccess(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		caller *appcontext.Claims
		status int
	}{
		{"allows a branch listed in branchIds", "/r/42/b/7", &manager, http.StatusNoContent},
		{"forbids a branch not listed in branchIds", "/r/42/b/8", &manager, http.StatusForbidden},
		{"lets the owner through to every branch", "/r/42/b/8", &owner, http.StatusNoContent},
		{"lets a system admin through", "/r/42/b/8", &admin, http.StatusNoContent},
		{"rejects a non-numeric branch id", "/r/42/b/x", &manager, http.StatusBadRequest},
		{"rejects an unauthenticated request", "/r/42/b/7", nil, http.StatusUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			g := newGuard(newFakeFetcher())

			// Act
			rec := guarded(g.RequireBranchAccess("branchId"), c.path, c.caller)

			// Assert
			assert.Equal(t, rec.Code, c.status)
		})
	}
}

func TestGuard_RequireRole(t *testing.T) {
	cases := []struct {
		name   string
		caller *appcontext.Claims
		status int
	}{
		{"allows a listed role", &admin, http.StatusNoContent},
		{"forbids any other role", &owner, http.StatusForbidden},
		{"rejects an unauthenticated request", nil, http.StatusUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			g := newGuard(newFakeFetcher())

			// Act
			rec := guarded(g.RequireRole(RoleSystemAdmin), "/r/42/b/7", c.caller)

			// Assert
			assert.Equal(t, rec.Code, c.status)
		})
	}
}
