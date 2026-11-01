package service

import (
	"context"
	"testing"
	"time"

	"github.com/quickbite/analytics-service/app/analytics"
	"github.com/quickbite/analytics-service/app/analytics/entity"
	"github.com/quickbite/analytics-service/internal/testkit"
	"github.com/quickbite/analytics-service/internal/testkit/assert"
)

var (
	oct1  = testkit.Day(2026, time.October, 1)
	oct31 = testkit.Day(2026, time.October, 31)
)

func restaurantRange() analytics.ListRestaurantDaysInput {
	return analytics.ListRestaurantDaysInput{RestaurantID: 42, From: oct1, To: oct31}
}

func restaurantDay(date string, c entity.DayCounters) entity.RestaurantDay {
	return entity.RestaurantDay{RestaurantID: 42, Date: date, Currency: "EGP", DayCounters: c}
}

// Every read endpoint shares the range rules; checked once per method.
func TestReports_RangeRules(t *testing.T) {
	reports := map[string]func(s *Service, from, to time.Time) error{
		"ListRestaurantDays": func(s *Service, from, to time.Time) error {
			_, err := s.ListRestaurantDays(context.Background(), analytics.ListRestaurantDaysInput{RestaurantID: 42, From: from, To: to})
			return err
		},
		"ListBranchDays": func(s *Service, from, to time.Time) error {
			_, err := s.ListBranchDays(context.Background(), analytics.ListBranchDaysInput{RestaurantID: 42, BranchID: 7, From: from, To: to})
			return err
		},
		"RestaurantFailures": func(s *Service, from, to time.Time) error {
			_, err := s.RestaurantFailures(context.Background(), analytics.ListRestaurantDaysInput{RestaurantID: 42, From: from, To: to})
			return err
		},
		"RestaurantDeliveryAvg": func(s *Service, from, to time.Time) error {
			_, err := s.RestaurantDeliveryAvg(context.Background(), analytics.ListRestaurantDaysInput{RestaurantID: 42, From: from, To: to})
			return err
		},
		"RestaurantPayments": func(s *Service, from, to time.Time) error {
			_, err := s.RestaurantPayments(context.Background(), analytics.ListRestaurantDaysInput{RestaurantID: 42, From: from, To: to})
			return err
		},
		"TopProducts": func(s *Service, from, to time.Time) error {
			_, err := s.TopProducts(context.Background(), analytics.TopProductsInput{RestaurantID: 42, From: from, To: to})
			return err
		},
		"PlatformDays": func(s *Service, from, to time.Time) error {
			_, err := s.PlatformDays(context.Background(), analytics.RangeInput{From: from, To: to})
			return err
		},
		"ActiveRestaurants": func(s *Service, from, to time.Time) error {
			_, err := s.ActiveRestaurants(context.Background(), analytics.RangeInput{From: from, To: to})
			return err
		},
		"TopRestaurants": func(s *Service, from, to time.Time) error {
			_, err := s.TopRestaurants(context.Background(), analytics.TopRestaurantsInput{From: from, To: to, Currency: "EGP"})
			return err
		},
	}

	for name, call := range reports {
		t.Run(name+" rejects from after to", func(t *testing.T) {
			// Arrange
			svc, _ := newTestService()

			// Act
			err := call(svc, oct31, oct1)

			// Assert
			assert.ErrIs(t, err, analytics.ErrInvalidDateRange)
		})

		t.Run(name+" rejects a range longer than 366 days", func(t *testing.T) {
			// Arrange
			svc, _ := newTestService()
			from := testkit.Day(2025, time.January, 1)

			// Act
			err := call(svc, from, from.AddDate(0, 0, 366))

			// Assert
			assert.ErrIs(t, err, analytics.ErrDateRangeTooLarge)
		})

		t.Run(name+" accepts exactly 366 days", func(t *testing.T) {
			// Arrange
			svc, _ := newTestService()
			from := testkit.Day(2024, time.January, 1)

			// Act
			err := call(svc, from, from.AddDate(0, 0, 365))

			// Assert
			assert.NoErr(t, err)
		})

		t.Run(name+" propagates a store failure", func(t *testing.T) {
			// Arrange
			svc, fakes := newTestService()
			fakes.Restaurants.Err, fakes.Branches.Err, fakes.Products.Err, fakes.Platform.Err = errStore, errStore, errStore, errStore

			// Act
			err := call(svc, oct1, oct31)

			// Assert
			assert.ErrIs(t, err, errStore)
		})
	}
}

func TestListRestaurantDays(t *testing.T) {
	t.Run("returns days with orders only, oldest first, with the derived average", func(t *testing.T) {
		// Arrange
		svc, fakes := newTestService()
		fakes.Restaurants.Rows = []entity.RestaurantDay{
			restaurantDay("2026-10-02", entity.DayCounters{OrdersCount: 3, RevenueSum: 10000}),
			restaurantDay("2026-10-03", entity.DayCounters{PaymentsCount: 1, PaymentsSum: 500}), // payment-only day
		}

		// Act
		rows, err := svc.ListRestaurantDays(context.Background(), restaurantRange())

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, rows, []analytics.RestaurantDayRow{
			{Date: "2026-10-02", OrdersCount: 3, RevenueMinor: 10000, Currency: "EGP", AvgOrderMinor: 3333},
		})
		assert.Equal(t, fakes.Restaurants.Reads[0], testkit.RangeCall{ID: 42, From: "2026-10-01", To: "2026-10-31"})
	})

	t.Run("returns an empty, non-nil list when nothing matches", func(t *testing.T) {
		// Arrange
		svc, _ := newTestService()

		// Act
		rows, err := svc.ListRestaurantDays(context.Background(), restaurantRange())

		// Assert
		assert.NoErr(t, err)
		assert.True(t, rows != nil && len(rows) == 0)
	})
}

func TestListBranchDays(t *testing.T) {
	t.Run("returns the branch's days scoped to its restaurant", func(t *testing.T) {
		// Arrange
		svc, fakes := newTestService()
		fakes.Branches.Rows = []entity.BranchDay{
			{BranchID: 7, RestaurantID: 42, Date: "2026-10-05", Currency: "EGP", DayCounters: entity.DayCounters{OrdersCount: 2, RevenueSum: 3001}},
			{BranchID: 7, RestaurantID: 42, Date: "2026-10-06", Currency: "EGP"},
		}

		// Act
		rows, err := svc.ListBranchDays(context.Background(), analytics.ListBranchDaysInput{RestaurantID: 42, BranchID: 7, From: oct1, To: oct31})

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, rows, []analytics.RestaurantDayRow{
			{Date: "2026-10-05", OrdersCount: 2, RevenueMinor: 3001, Currency: "EGP", AvgOrderMinor: 1500},
		})
		assert.Equal(t, fakes.Branches.Reads[0], testkit.RangeCall{ID: 7, ParentID: 42, From: "2026-10-01", To: "2026-10-31"})
	})
}

func TestRestaurantFailures(t *testing.T) {
	t.Run("derives daily and range failure rates from order and rejection counts", func(t *testing.T) {
		// Arrange
		svc, fakes := newTestService()
		fakes.Restaurants.Rows = []entity.RestaurantDay{
			restaurantDay("2026-10-01", entity.DayCounters{OrdersCount: 3, RejectedCount: 1}),
			restaurantDay("2026-10-02", entity.DayCounters{PaymentsCount: 2}), // no orders, no rejections → skipped
			restaurantDay("2026-10-03", entity.DayCounters{OrdersCount: 4}),
		}

		// Act
		rep, err := svc.RestaurantFailures(context.Background(), restaurantRange())

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, rep, analytics.FailureReport{
			OrdersCount: 7, RejectedCount: 1, FailureRate: 0.1429,
			Days: []analytics.FailureDayRow{
				{Date: "2026-10-01", OrdersCount: 3, RejectedCount: 1, FailureRate: 0.3333},
				{Date: "2026-10-03", OrdersCount: 4, RejectedCount: 0, FailureRate: 0},
			},
		})
	})

	t.Run("reports a zero rate when there are no orders", func(t *testing.T) {
		// Arrange
		svc, _ := newTestService()

		// Act
		rep, err := svc.RestaurantFailures(context.Background(), restaurantRange())

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, rep.FailureRate, 0.0)
		assert.Len(t, rep.Days, 0)
	})
}

func TestRestaurantDeliveryAvg(t *testing.T) {
	t.Run("reports days with deliveries only, each with its own average", func(t *testing.T) {
		// Arrange — day 1: 1 delivery of 10 minute; day 3: 3 deliveries totalling 30 minute
		svc, fakes := newTestService()
		minute := int64(60 * 1000)
		fakes.Restaurants.Rows = []entity.RestaurantDay{
			restaurantDay("2026-10-01", entity.DayCounters{DeliveryMsSum: 10 * minute, DeliveryMsCount: 1}),
			restaurantDay("2026-10-02", entity.DayCounters{OrdersCount: 1}),
			restaurantDay("2026-10-03", entity.DayCounters{DeliveryMsSum: 30 * minute, DeliveryMsCount: 3}),
		}

		// Act
		rep, err := svc.RestaurantDeliveryAvg(context.Background(), restaurantRange())

		// Assert — (10+30)/4 = 10 minute
		assert.NoErr(t, err)
		assert.Equal(t, rep.DeliveredCount, int64(4))
		assert.Equal(t, rep.AvgDeliveryMs, 10*minute)
		assert.Equal(t, rep.Days, []analytics.DeliveryDayRow{
			{Date: "2026-10-01", DeliveredCount: 1, AvgDeliveryMs: 10 * minute},
			{Date: "2026-10-03", DeliveredCount: 3, AvgDeliveryMs: 10 * minute},
		})
	})

	t.Run("weights each delivery equally across days", func(t *testing.T) {
		// Arrange — 1×60s on day 1, 3×20s on day 2: Σ/Σ = 120/4 = 30s (mean of means would be 40s)
		svc, fakes := newTestService()
		fakes.Restaurants.Rows = []entity.RestaurantDay{
			restaurantDay("2026-10-01", entity.DayCounters{DeliveryMsSum: 60000, DeliveryMsCount: 1}),
			restaurantDay("2026-10-02", entity.DayCounters{DeliveryMsSum: 60000, DeliveryMsCount: 3}),
		}

		// Act
		rep, err := svc.RestaurantDeliveryAvg(context.Background(), restaurantRange())

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, rep.AvgDeliveryMs, int64(30000))
	})
}

func TestRestaurantPayments(t *testing.T) {
	t.Run("sums captured payments over the days that have any", func(t *testing.T) {
		// Arrange
		svc, fakes := newTestService()
		fakes.Restaurants.Rows = []entity.RestaurantDay{
			restaurantDay("2026-10-01", entity.DayCounters{OrdersCount: 5}),
			restaurantDay("2026-10-02", entity.DayCounters{PaymentsCount: 2, PaymentsSum: 9000}),
			restaurantDay("2026-10-04", entity.DayCounters{PaymentsCount: 1, PaymentsSum: 1000}),
		}

		// Act
		rep, err := svc.RestaurantPayments(context.Background(), restaurantRange())

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, rep, analytics.PaymentsReport{
			PaymentsCount: 3, PaymentsMinor: 10000, Currency: "EGP",
			Days: []analytics.PaymentDayRow{
				{Date: "2026-10-02", PaymentsCount: 2, PaymentsMinor: 9000, Currency: "EGP"},
				{Date: "2026-10-04", PaymentsCount: 1, PaymentsMinor: 1000, Currency: "EGP"},
			},
		})
	})
}

func TestTopProducts(t *testing.T) {
	limits := map[string]struct{ in, want int }{
		"defaults to 10 when unset": {0, 10},
		"keeps an explicit limit":   {3, 3},
		"caps at 100":               {500, 100},
	}
	for name, l := range limits {
		t.Run(name, func(t *testing.T) {
			// Arrange
			svc, fakes := newTestService()

			// Act
			_, err := svc.TopProducts(context.Background(), analytics.TopProductsInput{RestaurantID: 42, From: oct1, To: oct31, Limit: l.in})

			// Assert
			assert.NoErr(t, err)
			assert.Equal(t, fakes.Products.Reads[0].Limit, l.want)
		})
	}

	t.Run("maps the ranked totals", func(t *testing.T) {
		// Arrange
		svc, fakes := newTestService()
		fakes.Products.Top = []entity.ProductTotal{{ProductID: 1, Currency: "EGP", UnitsSold: 4, RevenueSum: 4000, OrdersCount: 2}}

		// Act
		rows, err := svc.TopProducts(context.Background(), analytics.TopProductsInput{RestaurantID: 42, From: oct1, To: oct31})

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, rows, []analytics.ProductRankRow{{ProductID: 1, UnitsSold: 4, RevenueMinor: 4000, OrdersCount: 2, Currency: "EGP"}})
	})
}

func TestPlatformDays(t *testing.T) {
	t.Run("derives every average and rate per (day, currency)", func(t *testing.T) {
		// Arrange
		svc, fakes := newTestService()
		fakes.Platform.Rows = []entity.PlatformDay{
			{Date: "2026-10-01", Currency: "EGP", DayCounters: entity.DayCounters{
				OrdersCount: 4, RevenueSum: 10000, RejectedCount: 1, DeliveryMsSum: 9000, DeliveryMsCount: 3,
				PaymentsCount: 2, PaymentsSum: 5000,
			}},
			{Date: "2026-10-01", Currency: "SAR"},
		}

		// Act
		rows, err := svc.PlatformDays(context.Background(), analytics.RangeInput{From: oct1, To: oct31})

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, rows, []analytics.PlatformDayRow{
			{Date: "2026-10-01", Currency: "EGP", OrdersCount: 4, RevenueMinor: 10000, AvgOrderMinor: 2500,
				RejectedCount: 1, FailureRate: 0.25, DeliveredCount: 3, AvgDeliveryMs: 3000, PaymentsCount: 2, PaymentsMinor: 5000},
			{Date: "2026-10-01", Currency: "SAR"},
		})
	})
}

func TestActiveRestaurants(t *testing.T) {
	t.Run("returns the distinct total and the per-day counts", func(t *testing.T) {
		// Arrange
		svc, fakes := newTestService()
		fakes.Restaurants.ActiveTotal = 3
		fakes.Restaurants.ActiveDays = []entity.DateCount{{Date: "2026-10-01", Count: 2}, {Date: "2026-10-02", Count: 2}}

		// Act
		rep, err := svc.ActiveRestaurants(context.Background(), analytics.RangeInput{From: oct1, To: oct31})

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, rep, analytics.ActiveRestaurantsReport{
			ActiveRestaurants: 3,
			Days:              []analytics.ActiveRestaurantsDay{{Date: "2026-10-01", ActiveRestaurants: 2}, {Date: "2026-10-02", ActiveRestaurants: 2}},
		})
	})
}

func TestTopRestaurants(t *testing.T) {
	t.Run("ranks in one currency with the derived average order", func(t *testing.T) {
		// Arrange
		svc, fakes := newTestService()
		fakes.Restaurants.Top = []entity.RestaurantTotal{{RestaurantID: 42, Currency: "EGP", OrdersCount: 4, RevenueSum: 10000}}

		// Act
		rows, err := svc.TopRestaurants(context.Background(), analytics.TopRestaurantsInput{From: oct1, To: oct31, Currency: "EGP", Limit: 5})

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, rows, []analytics.RestaurantRankRow{{RestaurantID: 42, OrdersCount: 4, RevenueMinor: 10000, AvgOrderMinor: 2500, Currency: "EGP"}})
		assert.Equal(t, fakes.Restaurants.Reads[0], testkit.RangeCall{From: "2026-10-01", To: "2026-10-31", Currency: "EGP", Limit: 5})
	})
}
