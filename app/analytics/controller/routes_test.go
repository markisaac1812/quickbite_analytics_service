package controller_test

import (
	"net/http"
	"testing"

	"github.com/quickbite/analytics-service/internal/testkit"
	"github.com/quickbite/analytics-service/internal/testkit/assert"
)

// Access rules of Mount: authenticate → analytics:read → own restaurant
// (→ own branch); platform endpoints are system_admin only.
func TestRoutes_AccessRules(t *testing.T) {
	restaurantPaths := []string{
		"/restaurants/42/days?" + rng,
		"/restaurants/42/failures?" + rng,
		"/restaurants/42/delivery-avg?" + rng,
		"/restaurants/42/payments?" + rng,
		"/restaurants/42/products/top?" + rng,
		"/restaurants/42/branches/7/days?" + rng,
	}
	platformPaths := []string{
		"/platform/days?" + rng,
		"/platform/active-restaurants?" + rng,
		"/platform/top-restaurants?" + rng + "&currency=EGP",
	}

	cases := []struct {
		name   string
		caller *testkit.Caller
		paths  []string
		status int
		code   string
	}{
		{"anonymous callers get 401", nil, append(restaurantPaths, platformPaths...), http.StatusUnauthorized, "UNAUTHENTICATED"},
		{"the owner reads their restaurant", &testkit.Owner, restaurantPaths, http.StatusOK, ""},
		{"an admin reads any restaurant", &testkit.Admin, restaurantPaths, http.StatusOK, ""},
		{"an admin reads platform reports", &testkit.Admin, platformPaths, http.StatusOK, ""},
		{"an owner of another restaurant is forbidden", &testkit.OtherOwner, restaurantPaths, http.StatusForbidden, "FORBIDDEN"},
		{"a role without analytics:read is forbidden", &testkit.Staff, restaurantPaths, http.StatusForbidden, "FORBIDDEN"},
		{"a customer is forbidden", &testkit.Customer, restaurantPaths, http.StatusForbidden, "FORBIDDEN"},
		{"restaurant users cannot read platform reports", &testkit.Owner, platformPaths, http.StatusForbidden, "FORBIDDEN"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, path := range c.paths {
				// Arrange
				h := newHarness(t)

				// Act
				res := h.get(t, path, c.caller)

				// Assert
				assert.Equal(t, res.Status, c.status, path)
				assert.Equal(t, res.Error.Code, c.code, path)
			}
		})
	}

	t.Run("a branch manager reads their own branch only", func(t *testing.T) {
		// Arrange
		h := newHarness(t)

		// Act
		own := h.get(t, "/restaurants/42/branches/7/days?"+rng, &testkit.BranchManager)
		other := h.get(t, "/restaurants/42/branches/8/days?"+rng, &testkit.BranchManager)

		// Assert
		assert.Equal(t, own.Status, http.StatusOK)
		assert.Equal(t, other.Status, http.StatusForbidden)
	})

	t.Run("fails closed with 503 when core-service is down on a cache miss", func(t *testing.T) {
		// Arrange
		h := newHarness(t)
		h.core.FailWith(http.StatusInternalServerError)

		// Act
		res := h.get(t, "/restaurants/42/days?"+rng, &testkit.Owner)

		// Assert
		assert.Equal(t, res.Status, http.StatusServiceUnavailable)
		assert.Equal(t, res.Error.Code, "UPSTREAM_UNAVAILABLE")
	})

	t.Run("hits core once per role while the permission cache is fresh", func(t *testing.T) {
		// Arrange
		h := newHarness(t)

		// Act
		for range 3 {
			h.get(t, "/restaurants/42/days?"+rng, &testkit.Owner)
		}

		// Assert
		assert.Len(t, h.core.Calls(), 1)
		assert.Equal(t, h.core.Calls()[0].Query, "role=owner")
	})
}
