package dto_test

import (
	"encoding/json"
	"testing"

	"github.com/quickbite/analytics-service/app/analytics"
	"github.com/quickbite/analytics-service/app/analytics/dto"
	"github.com/quickbite/analytics-service/internal/testkit/assert"
)

func toJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	assert.NoErr(t, err)
	return string(b)
}

func TestMappers_EmptyListsRenderAsArrays(t *testing.T) {
	cases := map[string]any{
		"restaurant days":    dto.RestaurantDaysFrom(nil),
		"product ranks":      dto.ProductRanksFrom(nil),
		"platform days":      dto.PlatformDaysFrom(nil),
		"restaurant ranks":   dto.RestaurantRanksFrom(nil),
		"failure days":       dto.FailureReportFrom("a", "b", analytics.FailureReport{}).Days,
		"delivery days":      dto.DeliveryReportFrom("a", "b", analytics.DeliveryReport{}).Days,
		"payment days":       dto.PaymentsReportFrom("a", "b", analytics.PaymentsReport{}).Days,
		"active restaurants": dto.ActiveRestaurantsFrom("a", "b", analytics.ActiveRestaurantsReport{}).Days,
	}
	for name, v := range cases {
		t.Run(name, func(t *testing.T) {
			// Arrange — done above (nil / zero-value service output)

			// Act
			out := toJSON(t, v)

			// Assert
			assert.Equal(t, out, "[]")
		})
	}
}

func TestRestaurantDaysFrom(t *testing.T) {
	// Arrange
	rows := []analytics.RestaurantDayRow{{Date: "2026-10-01", OrdersCount: 2, RevenueMinor: 5000, Currency: "EGP", AvgOrderMinor: 2500}}

	// Act
	out := toJSON(t, dto.RestaurantDaysFrom(rows))

	// Assert
	assert.Equal(t, out, `[{"date":"2026-10-01","ordersCount":2,"revenueMinor":5000,"currency":"EGP","avgOrderMinor":2500}]`)
}

func TestFailureReportFrom(t *testing.T) {
	// Arrange
	rep := analytics.FailureReport{OrdersCount: 3, RejectedCount: 1, FailureRate: 0.3333,
		Days: []analytics.FailureDayRow{{Date: "2026-10-01", OrdersCount: 3, RejectedCount: 1, FailureRate: 0.3333}}}

	// Act
	out := toJSON(t, dto.FailureReportFrom("2026-10-01", "2026-10-31", rep))

	// Assert
	assert.Equal(t, out, `{"from":"2026-10-01","to":"2026-10-31","ordersCount":3,"rejectedCount":1,"failureRate":0.3333,`+
		`"days":[{"date":"2026-10-01","ordersCount":3,"rejectedCount":1,"failureRate":0.3333}]}`)
}

func TestDeliveryReportFrom(t *testing.T) {
	// Arrange
	rep := analytics.DeliveryReport{DeliveredCount: 2, AvgDeliveryMs: 900000,
		Days: []analytics.DeliveryDayRow{{Date: "2026-10-01", DeliveredCount: 2, AvgDeliveryMs: 900000}}}

	// Act
	out := toJSON(t, dto.DeliveryReportFrom("2026-10-01", "2026-10-01", rep))

	// Assert
	assert.Equal(t, out, `{"from":"2026-10-01","to":"2026-10-01","deliveredCount":2,"avgDeliveryMs":900000,`+
		`"days":[{"date":"2026-10-01","deliveredCount":2,"avgDeliveryMs":900000}]}`)
}

func TestPaymentsReportFrom(t *testing.T) {
	t.Run("renders the currency when there were payments", func(t *testing.T) {
		// Arrange
		rep := analytics.PaymentsReport{PaymentsCount: 1, PaymentsMinor: 4200, Currency: "EGP",
			Days: []analytics.PaymentDayRow{{Date: "2026-10-01", PaymentsCount: 1, PaymentsMinor: 4200, Currency: "EGP"}}}

		// Act
		out := toJSON(t, dto.PaymentsReportFrom("2026-10-01", "2026-10-01", rep))

		// Assert
		assert.Equal(t, out, `{"from":"2026-10-01","to":"2026-10-01","paymentsCount":1,"paymentsMinor":4200,"currency":"EGP",`+
			`"days":[{"date":"2026-10-01","paymentsCount":1,"paymentsMinor":4200,"currency":"EGP"}]}`)
	})

	t.Run("omits the currency when there were none", func(t *testing.T) {
		// Arrange
		rep := analytics.PaymentsReport{}

		// Act
		out := toJSON(t, dto.PaymentsReportFrom("2026-10-01", "2026-10-01", rep))

		// Assert
		assert.Equal(t, out, `{"from":"2026-10-01","to":"2026-10-01","paymentsCount":0,"paymentsMinor":0,"days":[]}`)
	})
}

func TestRankAndPlatformMappers(t *testing.T) {
	t.Run("product ranks", func(t *testing.T) {
		// Arrange
		rows := []analytics.ProductRankRow{{ProductID: 1, UnitsSold: 3, RevenueMinor: 3000, OrdersCount: 2, Currency: "EGP"}}

		// Act
		out := toJSON(t, dto.ProductRanksFrom(rows))

		// Assert
		assert.Equal(t, out, `[{"productId":1,"unitsSold":3,"revenueMinor":3000,"ordersCount":2,"currency":"EGP"}]`)
	})

	t.Run("restaurant ranks", func(t *testing.T) {
		// Arrange
		rows := []analytics.RestaurantRankRow{{RestaurantID: 42, OrdersCount: 2, RevenueMinor: 5000, AvgOrderMinor: 2500, Currency: "EGP"}}

		// Act
		out := toJSON(t, dto.RestaurantRanksFrom(rows))

		// Assert
		assert.Equal(t, out, `[{"restaurantId":42,"ordersCount":2,"revenueMinor":5000,"avgOrderMinor":2500,"currency":"EGP"}]`)
	})

	t.Run("platform days", func(t *testing.T) {
		// Arrange
		rows := []analytics.PlatformDayRow{{Date: "2026-10-01", Currency: "EGP", OrdersCount: 4, RevenueMinor: 10000, AvgOrderMinor: 2500,
			RejectedCount: 1, FailureRate: 0.25, DeliveredCount: 3, AvgDeliveryMs: 3000, PaymentsCount: 2, PaymentsMinor: 5000}}

		// Act
		out := toJSON(t, dto.PlatformDaysFrom(rows))

		// Assert
		assert.Equal(t, out, `[{"date":"2026-10-01","currency":"EGP","ordersCount":4,"revenueMinor":10000,"avgOrderMinor":2500,`+
			`"rejectedCount":1,"failureRate":0.25,"deliveredCount":3,"avgDeliveryMs":3000,"paymentsCount":2,"paymentsMinor":5000}]`)
	})

	t.Run("active restaurants", func(t *testing.T) {
		// Arrange
		rep := analytics.ActiveRestaurantsReport{ActiveRestaurants: 2, Days: []analytics.ActiveRestaurantsDay{{Date: "2026-10-01", ActiveRestaurants: 2}}}

		// Act
		out := toJSON(t, dto.ActiveRestaurantsFrom("2026-10-01", "2026-10-02", rep))

		// Assert
		assert.Equal(t, out, `{"from":"2026-10-01","to":"2026-10-02","activeRestaurants":2,"days":[{"date":"2026-10-01","activeRestaurants":2}]}`)
	})
}
