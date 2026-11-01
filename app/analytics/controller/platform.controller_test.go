package controller_test

import (
	"net/http"
	"testing"

	"github.com/quickbite/analytics-service/app/analytics/dto"
	"github.com/quickbite/analytics-service/app/analytics/entity"
	"github.com/quickbite/analytics-service/internal/testkit"
	"github.com/quickbite/analytics-service/internal/testkit/assert"
)

func TestGetPlatformDays(t *testing.T) {
	t.Run("returns per-(day, currency) totals to a system admin", func(t *testing.T) {
		// Arrange
		h := newHarness(t)
		h.fakes.Platform.Rows = []entity.PlatformDay{{Date: "2026-10-01", Currency: "EGP", DayCounters: entity.DayCounters{OrdersCount: 2, RevenueSum: 5000}}}

		// Act
		res := h.get(t, "/platform/days?"+rng, &testkit.Admin)

		// Assert
		assert.Equal(t, res.Status, http.StatusOK)
		var data []dto.PlatformDayResponse
		res.DataAs(t, &data)
		assert.Equal(t, data, []dto.PlatformDayResponse{{Date: "2026-10-01", Currency: "EGP", OrdersCount: 2, RevenueMinor: 5000, AvgOrderMinor: 2500}})
	})

	t.Run("validates the range", func(t *testing.T) {
		// Arrange
		h := newHarness(t)

		// Act
		missing := h.get(t, "/platform/days", &testkit.Admin)
		reversed := h.get(t, "/platform/days?from=2026-10-31&to=2026-10-01", &testkit.Admin)

		// Assert
		assert.Equal(t, missing.Error.Code, "VALIDATION_ERROR")
		assert.Equal(t, reversed.Error.Code, "ANALYTICS_INVALID_DATE_RANGE")
	})
}

func TestGetActiveRestaurants(t *testing.T) {
	t.Run("returns the distinct total with the per-day counts", func(t *testing.T) {
		// Arrange
		h := newHarness(t)
		h.fakes.Restaurants.ActiveTotal = 2
		h.fakes.Restaurants.ActiveDays = []entity.DateCount{{Date: "2026-10-01", Count: 2}}

		// Act
		res := h.get(t, "/platform/active-restaurants?"+rng, &testkit.Admin)

		// Assert
		assert.Equal(t, res.Status, http.StatusOK)
		var data dto.ActiveRestaurantsResponse
		res.DataAs(t, &data)
		assert.Equal(t, data, dto.ActiveRestaurantsResponse{
			From: "2026-10-01", To: "2026-10-31", ActiveRestaurants: 2,
			Days: []dto.ActiveRestaurantsDayResponse{{Date: "2026-10-01", ActiveRestaurants: 2}},
		})
	})

	t.Run("validates the range", func(t *testing.T) {
		// Arrange
		h := newHarness(t)

		// Act
		missing := h.get(t, "/platform/active-restaurants?from=2026-10-01", &testkit.Admin)
		tooLarge := h.get(t, "/platform/active-restaurants?from=2024-01-01&to=2026-10-01", &testkit.Admin)

		// Assert
		assert.Equal(t, missing.Error.Message, "to is required")
		assert.Equal(t, tooLarge.Error.Code, "ANALYTICS_DATE_RANGE_TOO_LARGE")
	})
}

func TestGetTopRestaurants(t *testing.T) {
	t.Run("ranks restaurants by revenue in the requested currency", func(t *testing.T) {
		// Arrange
		h := newHarness(t)
		h.fakes.Restaurants.Top = []entity.RestaurantTotal{{RestaurantID: 42, Currency: "EGP", OrdersCount: 2, RevenueSum: 5000}}

		// Act
		res := h.get(t, "/platform/top-restaurants?"+rng+"&currency=EGP&limit=3", &testkit.Admin)

		// Assert
		assert.Equal(t, res.Status, http.StatusOK)
		var data []dto.RestaurantRankResponse
		res.DataAs(t, &data)
		assert.Equal(t, data, []dto.RestaurantRankResponse{{RestaurantID: 42, OrdersCount: 2, RevenueMinor: 5000, AvgOrderMinor: 2500, Currency: "EGP"}})
		assert.Equal(t, h.fakes.Restaurants.Reads[0], testkit.RangeCall{From: "2026-10-01", To: "2026-10-31", Currency: "EGP", Limit: 3})
	})

	currencies := map[string]struct{ currency, message string }{
		"missing":   {"", "currency is required"},
		"too long":  {"EGPP", "currency must be exactly 3 characters"},
		"lowercase": {"egp", "currency must be uppercase (ISO 4217, e.g. EGP)"},
	}
	for name, c := range currencies {
		t.Run("rejects a "+name+" currency", func(t *testing.T) {
			// Arrange
			h := newHarness(t)

			// Act
			res := h.get(t, "/platform/top-restaurants?"+rng+"&currency="+c.currency, &testkit.Admin)

			// Assert
			assert.Equal(t, res.Status, http.StatusBadRequest)
			assert.Equal(t, res.Error.Message, c.message)
		})
	}

	t.Run("surfaces range errors from the service", func(t *testing.T) {
		// Arrange
		h := newHarness(t)

		// Act
		res := h.get(t, "/platform/top-restaurants?from=2026-10-31&to=2026-10-01&currency=EGP", &testkit.Admin)

		// Assert
		assert.Equal(t, res.Error.Code, "ANALYTICS_INVALID_DATE_RANGE")
	})
}
