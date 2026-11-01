package controller_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/quickbite/analytics-service/app/analytics/dto"
	"github.com/quickbite/analytics-service/app/analytics/entity"
	"github.com/quickbite/analytics-service/internal/testkit"
	"github.com/quickbite/analytics-service/internal/testkit/assert"
)

func day(date string, c entity.DayCounters) entity.RestaurantDay {
	return entity.RestaurantDay{RestaurantID: 42, Date: date, Currency: "EGP", DayCounters: c}
}

func TestGetRestaurantDays(t *testing.T) {
	t.Run("returns the owner's daily rows in the success envelope", func(t *testing.T) {
		// Arrange
		h := newHarness(t)
		h.fakes.Restaurants.Rows = []entity.RestaurantDay{day("2026-10-02", entity.DayCounters{OrdersCount: 2, RevenueSum: 5000})}

		// Act
		res := h.get(t, "/restaurants/42/days?"+rng, &testkit.Owner)

		// Assert
		assert.Equal(t, res.Status, http.StatusOK)
		assert.True(t, res.Success)
		var data []dto.RestaurantDayResponse
		res.DataAs(t, &data)
		assert.Equal(t, data, []dto.RestaurantDayResponse{{Date: "2026-10-02", OrdersCount: 2, RevenueMinor: 5000, Currency: "EGP", AvgOrderMinor: 2500}})
	})

	t.Run("renders an empty range as []", func(t *testing.T) {
		// Arrange
		h := newHarness(t)

		// Act
		res := h.get(t, "/restaurants/42/days?"+rng, &testkit.Owner)

		// Assert
		assert.Equal(t, res.Status, http.StatusOK)
		assert.Equal(t, string(res.Data), "[]")
	})

	validation := map[string]struct{ query, message string }{
		"missing dates":     {"", "from is required; to is required"},
		"malformed date":    {"from=2026-13-01&to=2026-10-31", "from must be a date (YYYY-MM-DD)"},
		"from after to":     {"from=2026-10-31&to=2026-10-01", "from must be on or before to"},
		"range over a year": {"from=2025-01-01&to=2026-10-31", "date range must not exceed 366 days"},
	}
	for name, c := range validation {
		t.Run("rejects "+name+" with 400", func(t *testing.T) {
			// Arrange
			h := newHarness(t)

			// Act
			res := h.get(t, "/restaurants/42/days?"+c.query, &testkit.Owner)

			// Assert
			assert.Equal(t, res.Status, http.StatusBadRequest)
			assert.False(t, res.Success)
			assert.Equal(t, res.Error.Message, c.message)
		})
	}

	t.Run("reports a non-numeric restaurant id to an admin as a validation error", func(t *testing.T) {
		// Arrange — admins bypass the tenant guard, so the controller validates the id
		h := newHarness(t)

		// Act
		res := h.get(t, "/restaurants/abc/days?"+rng, &testkit.Admin)

		// Assert
		assert.Equal(t, res.Status, http.StatusBadRequest)
		assert.Equal(t, res.Error.Code, "VALIDATION_ERROR")
		assert.Equal(t, res.Error.Message, "restaurantId must be a positive integer")
	})

	t.Run("hides an unexpected store failure behind a generic 500", func(t *testing.T) {
		// Arrange
		h := newHarness(t)
		h.fakes.Restaurants.Err = errors.New("connection reset by peer")

		// Act
		res := h.get(t, "/restaurants/42/days?"+rng, &testkit.Owner)

		// Assert
		assert.Equal(t, res.Status, http.StatusInternalServerError)
		assert.Equal(t, res.Error.Code, "INTERNAL_ERROR")
		assert.Equal(t, res.Error.Message, "Something went wrong")
	})
}

func TestGetRestaurantReports(t *testing.T) {
	t.Run("failures", func(t *testing.T) {
		// Arrange
		h := newHarness(t)
		h.fakes.Restaurants.Rows = []entity.RestaurantDay{day("2026-10-01", entity.DayCounters{OrdersCount: 4, RejectedCount: 1})}

		// Act
		res := h.get(t, "/restaurants/42/failures?"+rng, &testkit.Owner)

		// Assert
		assert.Equal(t, res.Status, http.StatusOK)
		var data dto.FailureReportResponse
		res.DataAs(t, &data)
		assert.Equal(t, data, dto.FailureReportResponse{
			From: "2026-10-01", To: "2026-10-31", OrdersCount: 4, RejectedCount: 1, FailureRate: 0.25,
			Days: []dto.FailureDayResponse{{Date: "2026-10-01", OrdersCount: 4, RejectedCount: 1, FailureRate: 0.25}},
		})
	})

	t.Run("delivery-avg", func(t *testing.T) {
		// Arrange
		h := newHarness(t)
		h.fakes.Restaurants.Rows = []entity.RestaurantDay{day("2026-10-01", entity.DayCounters{DeliveryMsSum: 3000, DeliveryMsCount: 2})}

		// Act
		res := h.get(t, "/restaurants/42/delivery-avg?"+rng, &testkit.Owner)

		// Assert
		assert.Equal(t, res.Status, http.StatusOK)
		var data dto.DeliveryReportResponse
		res.DataAs(t, &data)
		assert.Equal(t, data.AvgDeliveryMs, int64(1500))
		assert.Equal(t, data.DeliveredCount, int64(2))
	})

	t.Run("payments", func(t *testing.T) {
		// Arrange
		h := newHarness(t)
		h.fakes.Restaurants.Rows = []entity.RestaurantDay{day("2026-10-01", entity.DayCounters{PaymentsCount: 1, PaymentsSum: 4200})}

		// Act
		res := h.get(t, "/restaurants/42/payments?"+rng, &testkit.Owner)

		// Assert
		assert.Equal(t, res.Status, http.StatusOK)
		var data dto.PaymentsReportResponse
		res.DataAs(t, &data)
		assert.Equal(t, data.PaymentsMinor, int64(4200))
		assert.Equal(t, data.Currency, "EGP")
	})

	for _, path := range []string{"failures", "delivery-avg", "payments"} {
		t.Run(path+" validates the range", func(t *testing.T) {
			// Arrange
			h := newHarness(t)

			// Act
			bad := h.get(t, "/restaurants/42/"+path+"?from=x&to=2026-10-01", &testkit.Owner)
			reversed := h.get(t, "/restaurants/42/"+path+"?from=2026-10-31&to=2026-10-01", &testkit.Owner)

			// Assert
			assert.Equal(t, bad.Status, http.StatusBadRequest)
			assert.Equal(t, reversed.Error.Code, "ANALYTICS_INVALID_DATE_RANGE")
		})
	}
}

func TestGetTopProducts(t *testing.T) {
	t.Run("ranks products with the requested limit", func(t *testing.T) {
		// Arrange
		h := newHarness(t)
		h.fakes.Products.Top = []entity.ProductTotal{{ProductID: 1, Currency: "EGP", UnitsSold: 2, RevenueSum: 2000, OrdersCount: 1}}

		// Act
		res := h.get(t, "/restaurants/42/products/top?"+rng+"&limit=5", &testkit.Owner)

		// Assert
		assert.Equal(t, res.Status, http.StatusOK)
		var data []dto.ProductRankResponse
		res.DataAs(t, &data)
		assert.Equal(t, data, []dto.ProductRankResponse{{ProductID: 1, UnitsSold: 2, RevenueMinor: 2000, OrdersCount: 1, Currency: "EGP"}})
		assert.Equal(t, h.fakes.Products.Reads[0].Limit, 5)
	})

	limits := map[string]string{"non-numeric": "abc", "negative": "-1", "over 100": "101"}
	for name, limit := range limits {
		t.Run("rejects a "+name+" limit", func(t *testing.T) {
			// Arrange
			h := newHarness(t)

			// Act
			res := h.get(t, "/restaurants/42/products/top?"+rng+"&limit="+limit, &testkit.Owner)

			// Assert
			assert.Equal(t, res.Status, http.StatusBadRequest)
			assert.Equal(t, res.Error.Code, "VALIDATION_ERROR")
		})
	}

	t.Run("surfaces range errors from the service", func(t *testing.T) {
		// Arrange
		h := newHarness(t)

		// Act
		res := h.get(t, "/restaurants/42/products/top?from=2024-01-01&to=2026-01-01", &testkit.Owner)

		// Assert
		assert.Equal(t, res.Error.Code, "ANALYTICS_DATE_RANGE_TOO_LARGE")
	})
}

func TestGetBranchDays(t *testing.T) {
	t.Run("returns a branch's days to its branch manager", func(t *testing.T) {
		// Arrange
		h := newHarness(t)
		h.fakes.Branches.Rows = []entity.BranchDay{{BranchID: 7, RestaurantID: 42, Date: "2026-10-03", Currency: "EGP",
			DayCounters: entity.DayCounters{OrdersCount: 1, RevenueSum: 900}}}

		// Act
		res := h.get(t, "/restaurants/42/branches/7/days?"+rng, &testkit.BranchManager)

		// Assert
		assert.Equal(t, res.Status, http.StatusOK)
		var data []dto.RestaurantDayResponse
		res.DataAs(t, &data)
		assert.Equal(t, data, []dto.RestaurantDayResponse{{Date: "2026-10-03", OrdersCount: 1, RevenueMinor: 900, Currency: "EGP", AvgOrderMinor: 900}})
		assert.Equal(t, h.fakes.Branches.Reads[0], testkit.RangeCall{ID: 7, ParentID: 42, From: "2026-10-01", To: "2026-10-31"})
	})

	t.Run("validates the branch id and range for the owner", func(t *testing.T) {
		// Arrange
		h := newHarness(t)

		// Act
		badID := h.get(t, "/restaurants/42/branches/x/days?"+rng, &testkit.Owner)
		badRange := h.get(t, "/restaurants/42/branches/7/days?from=2026-10-31&to=2026-10-01", &testkit.Owner)

		// Assert
		assert.Equal(t, badID.Status, http.StatusBadRequest)
		assert.Equal(t, badID.Error.Message, "branchId must be a positive integer")
		assert.Equal(t, badRange.Error.Code, "ANALYTICS_INVALID_DATE_RANGE")
	})
}
