//go:build integration

package boot

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/quickbite/analytics-service/app/analytics"
	"github.com/quickbite/analytics-service/app/analytics/dto"
	"github.com/quickbite/analytics-service/app/analytics/repository"
	"github.com/quickbite/analytics-service/internal/testkit"
	"github.com/quickbite/analytics-service/internal/testkit/assert"
)

// HTTP reads against real Mongo. Rows are seeded straight through the
// repositories (≈ the Node tests' insertOrder() factories), then every
// endpoint is called over a real socket.

var seededAt = time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)

func seed(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	db := app.mongo.DB
	restaurants, branches := repository.NewRestaurantDayRepo(db), repository.NewBranchDayRepo(db)
	products, platform := repository.NewProductDayRepo(db), repository.NewPlatformDayRepo(db)

	day := func(restaurantID, branchID int64, date, currency string, d analytics.DayDelta) {
		assert.NoErr(t, restaurants.Increment(ctx, restaurantID, date, currency, d, seededAt))
		assert.NoErr(t, branches.Increment(ctx, branchID, restaurantID, date, currency, d, seededAt))
		assert.NoErr(t, platform.Increment(ctx, date, currency, d, seededAt))
	}
	// restaurant 42: two days of orders, one rejection, deliveries, payments
	day(42, 7, "2026-10-01", "EGP", analytics.DayDelta{Orders: 3, Revenue: 9000, Rejected: 1, RejectedRevenue: 3000, DeliveryMs: 1800000, Deliveries: 2, Payments: 1, PaymentsSum: 3000})
	day(42, 8, "2026-10-02", "EGP", analytics.DayDelta{Orders: 1, Revenue: 1000, DeliveryMs: 600000, Deliveries: 1})
	// restaurant 99: another tenant, same day
	day(99, 50, "2026-10-01", "EGP", analytics.DayDelta{Orders: 1, Revenue: 20000})
	// outside the range
	day(42, 7, "2026-09-30", "EGP", analytics.DayDelta{Orders: 5, Revenue: 5})

	assert.NoErr(t, products.IncrementLines(ctx, 42, "2026-10-01", "EGP", []analytics.OrderLine{
		{ProductID: 1, Quantity: 2, LineTotalMinor: 6000}, {ProductID: 2, Quantity: 3, LineTotalMinor: 3000},
	}, seededAt))
	assert.NoErr(t, products.IncrementLines(ctx, 42, "2026-10-02", "EGP", []analytics.OrderLine{{ProductID: 2, Quantity: 1, LineTotalMinor: 1000}}, seededAt))
}

func TestAPI_RestaurantEndpoints(t *testing.T) {
	app.reset(t)
	seed(t)

	t.Run("GET /restaurants/{id}/days", func(t *testing.T) {
		// Arrange — seeded above

		// Act
		res := app.get(t, "/restaurants/42/days"+octRange, &testkit.Owner)

		// Assert
		assert.Equal(t, res.Status, http.StatusOK)
		var data []dto.RestaurantDayResponse
		res.DataAs(t, &data)
		assert.Equal(t, data, []dto.RestaurantDayResponse{
			{Date: "2026-10-01", OrdersCount: 3, RevenueMinor: 9000, Currency: "EGP", AvgOrderMinor: 3000},
			{Date: "2026-10-02", OrdersCount: 1, RevenueMinor: 1000, Currency: "EGP", AvgOrderMinor: 1000},
		})
	})

	t.Run("GET /restaurants/{id}/failures", func(t *testing.T) {
		// Arrange — seeded above

		// Act
		res := app.get(t, "/restaurants/42/failures"+octRange, &testkit.Owner)

		// Assert
		var data dto.FailureReportResponse
		res.DataAs(t, &data)
		assert.Equal(t, data.OrdersCount, int64(4))
		assert.Equal(t, data.RejectedCount, int64(1))
		assert.Equal(t, data.FailureRate, 0.25)
	})

	t.Run("GET /restaurants/{id}/delivery-avg", func(t *testing.T) {
		// Arrange — seeded above

		// Act
		res := app.get(t, "/restaurants/42/delivery-avg"+octRange, &testkit.Owner)

		// Assert — (1 800 000 + 600 000) / 3
		var data dto.DeliveryReportResponse
		res.DataAs(t, &data)
		assert.Equal(t, data.DeliveredCount, int64(3))
		assert.Equal(t, data.AvgDeliveryMs, int64(800000))
	})

	t.Run("GET /restaurants/{id}/payments", func(t *testing.T) {
		// Arrange — seeded above

		// Act
		res := app.get(t, "/restaurants/42/payments"+octRange, &testkit.Owner)

		// Assert
		var data dto.PaymentsReportResponse
		res.DataAs(t, &data)
		assert.Equal(t, data, dto.PaymentsReportResponse{
			From: "2026-10-01", To: "2026-10-31", PaymentsCount: 1, PaymentsMinor: 3000, Currency: "EGP",
			Days: []dto.PaymentDayResponse{{Date: "2026-10-01", PaymentsCount: 1, PaymentsMinor: 3000, Currency: "EGP"}},
		})
	})

	t.Run("GET /restaurants/{id}/products/top", func(t *testing.T) {
		// Arrange — seeded above

		// Act
		res := app.get(t, "/restaurants/42/products/top"+octRange+"&limit=1", &testkit.Owner)

		// Assert
		var data []dto.ProductRankResponse
		res.DataAs(t, &data)
		assert.Equal(t, data, []dto.ProductRankResponse{{ProductID: 1, UnitsSold: 2, RevenueMinor: 6000, OrdersCount: 1, Currency: "EGP"}})
	})

	t.Run("GET /restaurants/{id}/branches/{bid}/days for the branch manager", func(t *testing.T) {
		// Arrange — seeded above

		// Act
		own := app.get(t, "/restaurants/42/branches/7/days"+octRange, &testkit.BranchManager)
		other := app.get(t, "/restaurants/42/branches/8/days"+octRange, &testkit.BranchManager)

		// Assert
		var data []dto.RestaurantDayResponse
		own.DataAs(t, &data)
		assert.Equal(t, data, []dto.RestaurantDayResponse{{Date: "2026-10-01", OrdersCount: 3, RevenueMinor: 9000, Currency: "EGP", AvgOrderMinor: 3000}})
		assert.Equal(t, other.Status, http.StatusForbidden)
	})

	t.Run("never leaks another tenant's rows", func(t *testing.T) {
		// Arrange — restaurant 99 exists in the seed

		// Act
		res := app.get(t, "/restaurants/99/days"+octRange, &testkit.Owner)

		// Assert
		assert.Equal(t, res.Status, http.StatusForbidden)
	})
}

func TestAPI_PlatformEndpoints(t *testing.T) {
	app.reset(t)
	seed(t)

	t.Run("GET /platform/days", func(t *testing.T) {
		// Arrange — seeded above

		// Act
		res := app.get(t, "/platform/days"+octRange, &testkit.Admin)

		// Assert
		var data []dto.PlatformDayResponse
		res.DataAs(t, &data)
		assert.Len(t, data, 2)
		assert.Equal(t, data[0], dto.PlatformDayResponse{
			Date: "2026-10-01", Currency: "EGP", OrdersCount: 4, RevenueMinor: 29000, AvgOrderMinor: 7250,
			RejectedCount: 1, FailureRate: 0.25, DeliveredCount: 2, AvgDeliveryMs: 900000, PaymentsCount: 1, PaymentsMinor: 3000,
		})
	})

	t.Run("GET /platform/active-restaurants", func(t *testing.T) {
		// Arrange — seeded above

		// Act
		res := app.get(t, "/platform/active-restaurants"+octRange, &testkit.Admin)

		// Assert
		var data dto.ActiveRestaurantsResponse
		res.DataAs(t, &data)
		assert.Equal(t, data, dto.ActiveRestaurantsResponse{
			From: "2026-10-01", To: "2026-10-31", ActiveRestaurants: 2,
			Days: []dto.ActiveRestaurantsDayResponse{{Date: "2026-10-01", ActiveRestaurants: 2}, {Date: "2026-10-02", ActiveRestaurants: 1}},
		})
	})

	t.Run("GET /platform/top-restaurants", func(t *testing.T) {
		// Arrange — seeded above

		// Act
		res := app.get(t, "/platform/top-restaurants"+octRange+"&currency=EGP", &testkit.Admin)

		// Assert
		var data []dto.RestaurantRankResponse
		res.DataAs(t, &data)
		assert.Equal(t, data, []dto.RestaurantRankResponse{
			{RestaurantID: 99, OrdersCount: 1, RevenueMinor: 20000, AvgOrderMinor: 20000, Currency: "EGP"},
			{RestaurantID: 42, OrdersCount: 4, RevenueMinor: 10000, AvgOrderMinor: 2500, Currency: "EGP"},
		})
	})

	t.Run("is closed to restaurant users", func(t *testing.T) {
		// Arrange — seeded above

		// Act
		res := app.get(t, "/platform/days"+octRange, &testkit.Owner)

		// Assert
		assert.Equal(t, res.Status, http.StatusForbidden)
	})
}

func TestAPI_Health(t *testing.T) {
	// Arrange — app booted in TestMain

	// Act
	res := testkit.GetURL(t, app.baseURL, "/health", nil)

	// Assert
	assert.Equal(t, res.Status, http.StatusOK)
	assert.Equal(t, string(res.Data), `{"status":"ok","mongo":"up","rabbit":"up"}`)
	assert.True(t, res.Header.Get("X-CorrelationId") != "")
}
