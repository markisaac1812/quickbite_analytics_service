//go:build integration

package repository_test

import (
	"context"
	"testing"

	"go.mongodb.org/mongo-driver/bson"

	"github.com/quickbite/analytics-service/app/analytics"
	"github.com/quickbite/analytics-service/app/analytics/entity"
	"github.com/quickbite/analytics-service/app/analytics/repository"
	"github.com/quickbite/analytics-service/internal/testkit/assert"
)

func TestEnsureIndexes(t *testing.T) {
	t.Run("declares every named index and is idempotent", func(t *testing.T) {
		// Arrange
		ctx := context.Background()

		// Act
		err := repository.EnsureIndexes(ctx, tm.DB) // second run (TestMain ran the first)

		// Assert
		assert.NoErr(t, err)
		want := map[string][]string{
			repository.CollRestaurantDay: {"uq_agg_restaurant_day_restaurant_id_date", "idx_agg_restaurant_day_date_restaurant_id"},
			repository.CollBranchDay:     {"uq_agg_branch_day_branch_id_date"},
			repository.CollProductDay:    {"uq_agg_product_day_product_id_date", "idx_agg_product_day_restaurant_id_date"},
			repository.CollPlatformDay:   {"uq_agg_platform_day_date_currency"},
			repository.CollOrderTimeline: {"uq_order_timelines_order_id", "ttl_order_timelines_updated_at"},
			repository.CollEventIDs:      {"uq_event_ids_event_id", "ttl_event_ids_received_at"},
		}
		for coll, names := range want {
			specs, err := tm.DB.Collection(coll).Indexes().ListSpecifications(ctx)
			assert.NoErr(t, err)
			have := map[string]bool{}
			for _, s := range specs {
				have[s.Name] = true
			}
			for _, n := range names {
				assert.True(t, have[n], coll+"."+n)
			}
		}
	})

	t.Run("wraps a server error", func(t *testing.T) {
		// Arrange
		ctx := cancelled()

		// Act
		err := repository.EnsureIndexes(ctx, tm.DB)

		// Assert
		assert.Contains(t, err.Error(), "ensure indexes on")
	})
}

func TestRestaurantDayRepo(t *testing.T) {
	repo := repository.NewRestaurantDayRepo(tm.DB)
	ctx := context.Background()

	t.Run("upserts one row per (restaurant, day) and $incs every counter", func(t *testing.T) {
		// Arrange
		reset(t)

		// Act
		assert.NoErr(t, repo.Increment(ctx, 42, "2026-10-01", "EGP", analytics.DayDelta{Orders: 1, Revenue: 2500}, at))
		assert.NoErr(t, repo.Increment(ctx, 42, "2026-10-01", "EGP", analytics.DayDelta{Orders: 1, Revenue: 1500, Payments: 1, PaymentsSum: 1500}, at))

		// Assert
		rows, err := repo.FindRange(ctx, 42, "2026-10-01", "2026-10-01")
		assert.NoErr(t, err)
		assert.Equal(t, rows, []entity.RestaurantDay{{
			RestaurantID: 42, Date: "2026-10-01", Currency: "EGP", UpdatedAt: at,
			DayCounters: entity.DayCounters{OrdersCount: 2, RevenueSum: 4000, PaymentsCount: 1, PaymentsSum: 1500},
		}})
		assert.Equal(t, tm.Count(repository.CollRestaurantDay, nil), int64(1))
	})

	t.Run("stores every counter on first insert so documents share one shape", func(t *testing.T) {
		// Arrange
		reset(t)

		// Act
		assert.NoErr(t, repo.Increment(ctx, 42, "2026-10-01", "EGP", analytics.DayDelta{Payments: 1, PaymentsSum: 100}, at))

		// Assert
		var doc bson.M
		assert.NoErr(t, tm.DB.Collection(repository.CollRestaurantDay).FindOne(ctx, bson.D{}).Decode(&doc))
		for _, f := range []string{"orders_count", "revenue_sum", "delivery_ms_sum", "delivery_ms_count", "rejected_count", "rejected_revenue_sum"} {
			_, ok := doc[f]
			assert.True(t, ok, f)
		}
	})

	t.Run("returns the range oldest first, scoped to the restaurant", func(t *testing.T) {
		// Arrange
		reset(t)
		for _, d := range []string{"2026-10-03", "2026-09-30", "2026-10-01", "2026-11-01"} {
			assert.NoErr(t, repo.Increment(ctx, 42, d, "EGP", analytics.DayDelta{Orders: 1}, at))
		}
		assert.NoErr(t, repo.Increment(ctx, 99, "2026-10-02", "EGP", analytics.DayDelta{Orders: 1}, at))

		// Act
		rows, err := repo.FindRange(ctx, 42, "2026-10-01", "2026-10-31")

		// Assert
		assert.NoErr(t, err)
		assert.Len(t, rows, 2)
		assert.Equal(t, rows[0].Date, "2026-10-01")
		assert.Equal(t, rows[1].Date, "2026-10-03")
	})

	t.Run("counts active restaurants per day and distinct over the range", func(t *testing.T) {
		// Arrange
		reset(t)
		assert.NoErr(t, repo.Increment(ctx, 1, "2026-10-01", "EGP", analytics.DayDelta{Orders: 1}, at))
		assert.NoErr(t, repo.Increment(ctx, 2, "2026-10-01", "EGP", analytics.DayDelta{Orders: 2}, at))
		assert.NoErr(t, repo.Increment(ctx, 1, "2026-10-02", "EGP", analytics.DayDelta{Orders: 1}, at))
		assert.NoErr(t, repo.Increment(ctx, 3, "2026-10-02", "EGP", analytics.DayDelta{Payments: 1}, at)) // no orders → inactive

		// Act
		total, days, err := repo.ActiveRestaurants(ctx, "2026-10-01", "2026-10-31")

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, total, int64(2))
		assert.Equal(t, days, []entity.DateCount{{Date: "2026-10-01", Count: 2}, {Date: "2026-10-02", Count: 1}})
	})

	t.Run("returns zero and an empty list when nobody was active", func(t *testing.T) {
		// Arrange
		reset(t)

		// Act
		total, days, err := repo.ActiveRestaurants(ctx, "2026-10-01", "2026-10-31")

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, total, int64(0))
		assert.True(t, days != nil && len(days) == 0)
	})

	t.Run("ranks restaurants by revenue in one currency, ties by id, limited", func(t *testing.T) {
		// Arrange
		reset(t)
		assert.NoErr(t, repo.Increment(ctx, 1, "2026-10-01", "EGP", analytics.DayDelta{Orders: 1, Revenue: 1000}, at))
		assert.NoErr(t, repo.Increment(ctx, 1, "2026-10-02", "EGP", analytics.DayDelta{Orders: 1, Revenue: 4000}, at))
		assert.NoErr(t, repo.Increment(ctx, 2, "2026-10-01", "EGP", analytics.DayDelta{Orders: 2, Revenue: 5000}, at))
		assert.NoErr(t, repo.Increment(ctx, 3, "2026-10-01", "EGP", analytics.DayDelta{Orders: 1, Revenue: 100}, at))
		assert.NoErr(t, repo.Increment(ctx, 4, "2026-10-01", "SAR", analytics.DayDelta{Orders: 9, Revenue: 99999}, at))
		assert.NoErr(t, repo.Increment(ctx, 5, "2026-10-01", "EGP", analytics.DayDelta{Payments: 1, PaymentsSum: 9}, at))

		// Act
		top, err := repo.TopRestaurants(ctx, "2026-10-01", "2026-10-31", "EGP", 2)

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, top, []entity.RestaurantTotal{
			{RestaurantID: 1, Currency: "EGP", OrdersCount: 2, RevenueSum: 5000},
			{RestaurantID: 2, Currency: "EGP", OrdersCount: 2, RevenueSum: 5000},
		})
	})

	t.Run("wraps driver errors", func(t *testing.T) {
		// Arrange
		ctx := cancelled()

		// Act
		incErr := repo.Increment(ctx, 42, "2026-10-01", "EGP", analytics.DayDelta{}, at)
		_, findErr := repo.FindRange(ctx, 42, "2026-10-01", "2026-10-01")
		_, _, activeErr := repo.ActiveRestaurants(ctx, "2026-10-01", "2026-10-01")
		_, topErr := repo.TopRestaurants(ctx, "2026-10-01", "2026-10-01", "EGP", 1)

		// Assert
		assert.Contains(t, incErr.Error(), "agg_restaurant_day increment")
		assert.Contains(t, findErr.Error(), "agg_restaurant_day find")
		assert.Contains(t, activeErr.Error(), "agg_restaurant_day active")
		assert.Contains(t, topErr.Error(), "agg_restaurant_day top")
	})
}

func TestBranchDayRepo(t *testing.T) {
	repo := repository.NewBranchDayRepo(tm.DB)
	ctx := context.Background()

	t.Run("upserts per (branch, day) and keeps the tenant", func(t *testing.T) {
		// Arrange
		reset(t)

		// Act
		assert.NoErr(t, repo.Increment(ctx, 7, 42, "2026-10-01", "EGP", analytics.DayDelta{Orders: 1, Revenue: 900}, at))
		assert.NoErr(t, repo.Increment(ctx, 7, 42, "2026-10-01", "EGP", analytics.DayDelta{Orders: 1, Revenue: 100}, at))

		// Assert
		rows, err := repo.FindRange(ctx, 42, 7, "2026-10-01", "2026-10-31")
		assert.NoErr(t, err)
		assert.Equal(t, rows, []entity.BranchDay{{
			BranchID: 7, RestaurantID: 42, Date: "2026-10-01", Currency: "EGP", UpdatedAt: at,
			DayCounters: entity.DayCounters{OrdersCount: 2, RevenueSum: 1000},
		}})
	})

	t.Run("returns nothing for a branch id under another tenant", func(t *testing.T) {
		// Arrange
		reset(t)
		assert.NoErr(t, repo.Increment(ctx, 7, 42, "2026-10-01", "EGP", analytics.DayDelta{Orders: 1}, at))

		// Act
		rows, err := repo.FindRange(ctx, 99, 7, "2026-10-01", "2026-10-31")

		// Assert
		assert.NoErr(t, err)
		assert.Len(t, rows, 0)
	})

	t.Run("wraps driver errors", func(t *testing.T) {
		// Arrange
		ctx := cancelled()

		// Act
		incErr := repo.Increment(ctx, 7, 42, "2026-10-01", "EGP", analytics.DayDelta{}, at)
		_, findErr := repo.FindRange(ctx, 42, 7, "2026-10-01", "2026-10-01")

		// Assert
		assert.Contains(t, incErr.Error(), "agg_branch_day increment")
		assert.Contains(t, findErr.Error(), "agg_branch_day find")
	})
}

func TestProductDayRepo(t *testing.T) {
	repo := repository.NewProductDayRepo(tm.DB)
	ctx := context.Background()

	t.Run("applies every line of an order in one bulk upsert", func(t *testing.T) {
		// Arrange
		reset(t)
		lines := []analytics.OrderLine{{ProductID: 1, Quantity: 2, LineTotalMinor: 2000}, {ProductID: 2, Quantity: 1, LineTotalMinor: 500}}

		// Act
		assert.NoErr(t, repo.IncrementLines(ctx, 42, "2026-10-01", "EGP", lines, at))
		assert.NoErr(t, repo.IncrementLines(ctx, 42, "2026-10-01", "EGP", lines[:1], at))

		// Assert
		var p1 entity.ProductDay
		assert.NoErr(t, tm.DB.Collection(repository.CollProductDay).FindOne(ctx, bson.D{{Key: "product_id", Value: 1}}).Decode(&p1))
		assert.Equal(t, p1, entity.ProductDay{
			ProductID: 1, RestaurantID: 42, Date: "2026-10-01", Currency: "EGP",
			UnitsSold: 4, RevenueSum: 4000, OrdersCount: 2, UpdatedAt: at,
		})
		assert.Equal(t, tm.Count(repository.CollProductDay, nil), int64(2))
	})

	t.Run("is a no-op for an order without lines", func(t *testing.T) {
		// Arrange
		reset(t)

		// Act
		err := repo.IncrementLines(ctx, 42, "2026-10-01", "EGP", nil, at)

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, tm.Count(repository.CollProductDay, nil), int64(0))
	})

	t.Run("ranks a restaurant's products by revenue over the range", func(t *testing.T) {
		// Arrange
		reset(t)
		assert.NoErr(t, repo.IncrementLines(ctx, 42, "2026-10-01", "EGP", []analytics.OrderLine{{ProductID: 1, Quantity: 1, LineTotalMinor: 1000}, {ProductID: 2, Quantity: 3, LineTotalMinor: 3000}}, at))
		assert.NoErr(t, repo.IncrementLines(ctx, 42, "2026-10-02", "EGP", []analytics.OrderLine{{ProductID: 1, Quantity: 3, LineTotalMinor: 3000}, {ProductID: 3, Quantity: 1, LineTotalMinor: 10}}, at))
		assert.NoErr(t, repo.IncrementLines(ctx, 99, "2026-10-02", "EGP", []analytics.OrderLine{{ProductID: 50, Quantity: 1, LineTotalMinor: 99999}}, at))

		// Act
		top, err := repo.TopProducts(ctx, 42, "2026-10-01", "2026-10-31", 2)

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, top, []entity.ProductTotal{
			{ProductID: 1, Currency: "EGP", UnitsSold: 4, RevenueSum: 4000, OrdersCount: 2},
			{ProductID: 2, Currency: "EGP", UnitsSold: 3, RevenueSum: 3000, OrdersCount: 1},
		})
	})

	t.Run("wraps driver errors", func(t *testing.T) {
		// Arrange
		ctx := cancelled()

		// Act
		incErr := repo.IncrementLines(ctx, 42, "2026-10-01", "EGP", []analytics.OrderLine{{ProductID: 1, Quantity: 1}}, at)
		_, topErr := repo.TopProducts(ctx, 42, "2026-10-01", "2026-10-01", 1)

		// Assert
		assert.Contains(t, incErr.Error(), "agg_product_day bulk increment (1 lines)")
		assert.Contains(t, topErr.Error(), "agg_product_day top")
	})
}

func TestPlatformDayRepo(t *testing.T) {
	repo := repository.NewPlatformDayRepo(tm.DB)
	ctx := context.Background()

	t.Run("keeps one row per (day, currency), sorted by day then currency", func(t *testing.T) {
		// Arrange
		reset(t)
		assert.NoErr(t, repo.Increment(ctx, "2026-10-02", "EGP", analytics.DayDelta{Orders: 1, Revenue: 100}, at))
		assert.NoErr(t, repo.Increment(ctx, "2026-10-01", "SAR", analytics.DayDelta{Orders: 1, Revenue: 50}, at))
		assert.NoErr(t, repo.Increment(ctx, "2026-10-01", "EGP", analytics.DayDelta{Orders: 1, Revenue: 200}, at))
		assert.NoErr(t, repo.Increment(ctx, "2026-10-01", "EGP", analytics.DayDelta{Rejected: 1, RejectedRevenue: 200}, at))

		// Act
		rows, err := repo.FindRange(ctx, "2026-10-01", "2026-10-31")

		// Assert
		assert.NoErr(t, err)
		assert.Len(t, rows, 3)
		assert.Equal(t, []string{rows[0].Date + "/" + rows[0].Currency, rows[1].Date + "/" + rows[1].Currency, rows[2].Date + "/" + rows[2].Currency},
			[]string{"2026-10-01/EGP", "2026-10-01/SAR", "2026-10-02/EGP"})
		assert.Equal(t, rows[0].DayCounters, entity.DayCounters{OrdersCount: 1, RevenueSum: 200, RejectedCount: 1, RejectedRevenueSum: 200})
	})

	t.Run("wraps driver errors", func(t *testing.T) {
		// Arrange
		ctx := cancelled()

		// Act
		incErr := repo.Increment(ctx, "2026-10-01", "EGP", analytics.DayDelta{}, at)
		_, findErr := repo.FindRange(ctx, "2026-10-01", "2026-10-01")

		// Assert
		assert.Contains(t, incErr.Error(), "agg_platform_day increment")
		assert.Contains(t, findErr.Error(), "agg_platform_day find")
	})
}
