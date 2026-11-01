//go:build integration

package repository_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quickbite/analytics-service/app/analytics"
	"github.com/quickbite/analytics-service/app/analytics/repository"
	"github.com/quickbite/analytics-service/internal/testkit/assert"
)

func placed(orderID string, placedAt time.Time) analytics.TimelinePlaced {
	return analytics.TimelinePlaced{
		OrderID: orderID, RestaurantID: 42, BranchID: 7, Currency: "EGP", TotalMinor: 2500,
		PlacedAt: placedAt, PlacedDate: placedAt.Format("2006-01-02"), At: at,
	}
}

func TestOrderTimelineRepo(t *testing.T) {
	repo := repository.NewOrderTimelineRepo(tm.DB)
	ctx := context.Background()
	placedAt := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)

	t.Run("does not claim a delivery until the order is placed", func(t *testing.T) {
		// Arrange
		reset(t)
		assert.NoErr(t, repo.RecordDelivered(ctx, "o-1", 42, 7, placedAt.Add(time.Hour), at))

		// Act
		claimed, err := repo.ClaimDelivery(ctx, "o-1")

		// Assert
		assert.NoErr(t, err)
		assert.True(t, claimed == nil)
	})

	t.Run("claims a delivery exactly once when both facts are known", func(t *testing.T) {
		// Arrange
		reset(t)
		assert.NoErr(t, repo.RecordDelivered(ctx, "o-1", 1, 1, placedAt.Add(time.Hour), at)) // tenant ids from a later fact…
		assert.NoErr(t, repo.RecordPlaced(ctx, placed("o-1", placedAt)))                     // …are overwritten by placed

		// Act
		first, err1 := repo.ClaimDelivery(ctx, "o-1")
		second, err2 := repo.ClaimDelivery(ctx, "o-1")

		// Assert
		assert.NoErr(t, err1)
		assert.NoErr(t, err2)
		assert.True(t, second == nil, "already applied")
		assert.Equal(t, first.OrderID, "o-1")
		assert.Equal(t, first.RestaurantID, int64(42))
		assert.Equal(t, first.BranchID, int64(7))
		assert.Equal(t, first.PlacedDate, "2026-10-29")
		assert.Equal(t, first.TotalMinor, int64(2500))
		assert.Equal(t, *first.PlacedAt, placedAt)
		assert.Equal(t, *first.DeliveredAt, placedAt.Add(time.Hour))
		assert.True(t, first.DeliveryApplied)
		assert.False(t, first.RejectionApplied)
	})

	t.Run("hands the claim to exactly one of many racing workers", func(t *testing.T) {
		// Arrange
		reset(t)
		assert.NoErr(t, repo.RecordPlaced(ctx, placed("o-1", placedAt)))
		assert.NoErr(t, repo.RecordRejected(ctx, "o-1", 42, 7, placedAt.Add(time.Minute), at))
		var wins atomic.Int32
		var wg sync.WaitGroup

		// Act
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if tl, err := repo.ClaimRejection(ctx, "o-1"); err == nil && tl != nil {
					wins.Add(1)
				}
			}()
		}
		wg.Wait()

		// Assert
		assert.Equal(t, wins.Load(), int32(1))
	})

	t.Run("keeps one timeline per order however many facts arrive", func(t *testing.T) {
		// Arrange
		reset(t)

		// Act
		assert.NoErr(t, repo.RecordPlaced(ctx, placed("o-1", placedAt)))
		assert.NoErr(t, repo.RecordPlaced(ctx, placed("o-1", placedAt)))
		assert.NoErr(t, repo.RecordRejected(ctx, "o-1", 42, 7, placedAt, at))
		assert.NoErr(t, repo.RecordDelivered(ctx, "o-1", 42, 7, placedAt, at))

		// Assert
		assert.Equal(t, tm.Count(repository.CollOrderTimeline, nil), int64(1))
	})

	t.Run("wraps driver errors", func(t *testing.T) {
		// Arrange
		ctx := cancelled()

		// Act
		placedErr := repo.RecordPlaced(ctx, placed("o-1", placedAt))
		deliveredErr := repo.RecordDelivered(ctx, "o-1", 42, 7, placedAt, at)
		_, claimErr := repo.ClaimDelivery(ctx, "o-1")

		// Assert
		assert.Contains(t, placedErr.Error(), "order_timelines record placed")
		assert.Contains(t, deliveredErr.Error(), "order_timelines record delivered")
		assert.Contains(t, claimErr.Error(), "order_timelines claim delivery_applied")
	})
}

func TestEventIDsRepo(t *testing.T) {
	repo := repository.NewEventIDsRepo(tm.DB)
	ctx := context.Background()

	t.Run("marks an event fresh once, then reports the replay", func(t *testing.T) {
		// Arrange
		reset(t)

		// Act
		first, err1 := repo.MarkSeen(ctx, "e-1", "order.placed")
		second, err2 := repo.MarkSeen(ctx, "e-1", "order.placed")

		// Assert
		assert.NoErr(t, err1)
		assert.NoErr(t, err2)
		assert.True(t, first)
		assert.False(t, second)
	})

	t.Run("forgets a marker so the event can be replayed", func(t *testing.T) {
		// Arrange
		reset(t)
		_, _ = repo.MarkSeen(ctx, "e-1", "order.placed")

		// Act
		err := repo.Forget(ctx, "e-1")
		again, _ := repo.MarkSeen(ctx, "e-1", "order.placed")

		// Assert
		assert.NoErr(t, err)
		assert.True(t, again)
	})

	t.Run("wraps driver errors", func(t *testing.T) {
		// Arrange
		ctx := cancelled()

		// Act
		_, markErr := repo.MarkSeen(ctx, "e-1", "order.placed")
		forgetErr := repo.Forget(ctx, "e-1")

		// Assert
		assert.Contains(t, markErr.Error(), "event_ids insert")
		assert.Contains(t, forgetErr.Error(), "event_ids delete")
	})
}

func TestResetAll(t *testing.T) {
	t.Run("empties every module collection and reports the counts", func(t *testing.T) {
		// Arrange
		reset(t)
		ctx := context.Background()
		_, _ = repository.NewEventIDsRepo(tm.DB).MarkSeen(ctx, "e-1", "order.placed")
		_ = repository.NewPlatformDayRepo(tm.DB).Increment(ctx, "2026-10-01", "EGP", analytics.DayDelta{Orders: 1}, at)

		// Act
		deleted, err := repository.ResetAll(ctx, tm.DB)

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, deleted[repository.CollEventIDs], int64(1))
		assert.Equal(t, deleted[repository.CollPlatformDay], int64(1))
		assert.Equal(t, len(deleted), len(repository.AllCollections))
	})

	t.Run("wraps driver errors", func(t *testing.T) {
		// Arrange
		ctx := cancelled()

		// Act
		_, err := repository.ResetAll(ctx, tm.DB)

		// Assert
		assert.Contains(t, err.Error(), "reset agg_restaurant_day")
	})
}
