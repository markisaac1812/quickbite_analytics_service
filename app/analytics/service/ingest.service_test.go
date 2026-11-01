package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/quickbite/analytics-service/app/analytics"
	"github.com/quickbite/analytics-service/internal/testkit"
	"github.com/quickbite/analytics-service/internal/testkit/assert"
)

var (
	fixedNow = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	errStore = errors.New("store down")
)

func newTestService() (*Service, *testkit.FakeStores) {
	fakes := testkit.NewFakeStores()
	svc := New(fakes.Stores())
	svc.now = func() time.Time { return fixedNow }
	return svc, fakes
}

func placedInput(orderID string, placedAt time.Time) analytics.OnOrderPlacedInput {
	return analytics.OnOrderPlacedInput{
		EventID: "evt-" + orderID, OrderID: orderID,
		RestaurantID: testkit.RestaurantID, BranchID: testkit.BranchID,
		Currency: "EGP", TotalMinor: 2500, PlacedAt: placedAt,
		Lines: []analytics.OrderLine{{ProductID: 1, Quantity: 2, LineTotalMinor: 2000}, {ProductID: 2, Quantity: 1, LineTotalMinor: 500}},
	}
}

func TestOnOrderPlaced(t *testing.T) {
	t.Run("adds the order and its revenue to the restaurant, branch and platform day", func(t *testing.T) {
		// Arrange
		svc, fakes := newTestService()
		in := placedInput("o-1", time.Date(2026, 10, 29, 9, 30, 0, 0, time.UTC))

		// Act
		err := svc.OnOrderPlaced(context.Background(), in)

		// Assert
		assert.NoErr(t, err)
		want := analytics.DayDelta{Orders: 1, Revenue: 2500}
		assert.Equal(t, fakes.Restaurants.Incs, []testkit.DayInc{{ID: 42, Date: "2026-10-29", Currency: "EGP", Delta: want}})
		assert.Equal(t, fakes.Branches.Incs, []testkit.DayInc{{ID: 7, ParentID: 42, Date: "2026-10-29", Currency: "EGP", Delta: want}})
		assert.Equal(t, fakes.Platform.Incs, []testkit.DayInc{{Date: "2026-10-29", Currency: "EGP", Delta: want}})
	})

	t.Run("buckets by the UTC day of placedAt, not the producer's local day", func(t *testing.T) {
		// Arrange — 01:30 in Cairo (UTC+3) on the 30th is still the 29th in UTC
		svc, fakes := newTestService()
		cairo := time.FixedZone("EEST", 3*60*60)
		in := placedInput("o-1", time.Date(2026, 10, 30, 1, 30, 0, 0, cairo))

		// Act
		err := svc.OnOrderPlaced(context.Background(), in)

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, fakes.Restaurants.Incs[0].Date, "2026-10-29")
		assert.Equal(t, *fakes.Timelines.Get("o-1").PlacedAt, in.PlacedAt.UTC())
	})

	t.Run("skips the branch row when the order has no branch", func(t *testing.T) {
		// Arrange
		svc, fakes := newTestService()
		in := placedInput("o-1", fixedNow)
		in.BranchID = 0

		// Act
		err := svc.OnOrderPlaced(context.Background(), in)

		// Assert
		assert.NoErr(t, err)
		assert.Len(t, fakes.Branches.Incs, 0)
		assert.Len(t, fakes.Restaurants.Incs, 1)
	})

	t.Run("merges duplicate products into one sorted BulkWrite", func(t *testing.T) {
		// Arrange
		svc, fakes := newTestService()
		in := placedInput("o-1", fixedNow)
		in.Lines = []analytics.OrderLine{
			{ProductID: 9, Quantity: 1, LineTotalMinor: 300},
			{ProductID: 3, Quantity: 2, LineTotalMinor: 1000},
			{ProductID: 9, Quantity: 4, LineTotalMinor: 1200},
		}

		// Act
		err := svc.OnOrderPlaced(context.Background(), in)

		// Assert
		assert.NoErr(t, err)
		assert.Len(t, fakes.Products.Bulk, 1, "one round trip per order")
		assert.Equal(t, fakes.Products.Bulk[0], testkit.LinesCall{
			RestaurantID: 42, Date: "2026-10-30", Currency: "EGP",
			Lines: []analytics.OrderLine{{ProductID: 3, Quantity: 2, LineTotalMinor: 1000}, {ProductID: 9, Quantity: 5, LineTotalMinor: 1500}},
		})
	})

	t.Run("records the placed facts on the order timeline", func(t *testing.T) {
		// Arrange
		svc, fakes := newTestService()
		placedAt := time.Date(2026, 10, 29, 9, 30, 0, 0, time.UTC)

		// Act
		err := svc.OnOrderPlaced(context.Background(), placedInput("o-1", placedAt))

		// Assert
		assert.NoErr(t, err)
		tl := fakes.Timelines.Get("o-1")
		assert.Equal(t, tl.PlacedDate, "2026-10-29")
		assert.Equal(t, tl.TotalMinor, int64(2500))
		assert.Equal(t, tl.Currency, "EGP")
		assert.Equal(t, tl.UpdatedAt, fixedNow)
	})

	t.Run("settles a delivery that arrived before the order was placed", func(t *testing.T) {
		// Arrange
		svc, fakes := newTestService()
		placedAt := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
		err := svc.OnOrderDelivered(context.Background(), analytics.OnOrderDeliveredInput{
			OrderID: "o-1", RestaurantID: 42, BranchID: 7, DeliveredAt: placedAt.Add(30 * time.Minute),
		})
		assert.NoErr(t, err)
		assert.Len(t, fakes.Restaurants.Incs, 0, "delivery waits for placed")

		// Act
		err = svc.OnOrderPlaced(context.Background(), placedInput("o-1", placedAt))

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, fakes.Restaurants.Incs[1], testkit.DayInc{
			ID: 42, Date: "2026-10-29", Currency: "EGP",
			Delta: analytics.DayDelta{DeliveryMs: 30 * 60 * 1000, Deliveries: 1},
		})
	})

	t.Run("settles a rejection that arrived before the order was placed", func(t *testing.T) {
		// Arrange
		svc, fakes := newTestService()
		placedAt := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
		assert.NoErr(t, svc.OnOrderRejected(context.Background(), analytics.OnOrderRejectedInput{
			OrderID: "o-1", RestaurantID: 42, BranchID: 7, RejectedAt: placedAt.Add(time.Minute),
		}))

		// Act
		err := svc.OnOrderPlaced(context.Background(), placedInput("o-1", placedAt))

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, fakes.Platform.Incs[1].Delta, analytics.DayDelta{Rejected: 1, RejectedRevenue: 2500})
	})

	t.Run("rejects an invalid payload without writing anything", func(t *testing.T) {
		cases := map[string]func(in *analytics.OnOrderPlacedInput){
			"missing orderId":         func(in *analytics.OnOrderPlacedInput) { in.OrderID = "" },
			"non-positive restaurant": func(in *analytics.OnOrderPlacedInput) { in.RestaurantID = 0 },
			"missing currency":        func(in *analytics.OnOrderPlacedInput) { in.Currency = "" },
			"negative total":          func(in *analytics.OnOrderPlacedInput) { in.TotalMinor = -1 },
			"zero placedAt":           func(in *analytics.OnOrderPlacedInput) { in.PlacedAt = time.Time{} },
			"line without product":    func(in *analytics.OnOrderPlacedInput) { in.Lines[0].ProductID = 0 },
			"line with zero qty":      func(in *analytics.OnOrderPlacedInput) { in.Lines[0].Quantity = 0 },
			"line negative total":     func(in *analytics.OnOrderPlacedInput) { in.Lines[0].LineTotalMinor = -5 },
		}
		for name, mutate := range cases {
			t.Run(name, func(t *testing.T) {
				// Arrange
				svc, fakes := newTestService()
				in := placedInput("o-1", fixedNow)
				mutate(&in)

				// Act
				err := svc.OnOrderPlaced(context.Background(), in)

				// Assert
				assert.ErrIs(t, err, analytics.ErrInvalidEventPayload)
				assert.Len(t, fakes.Restaurants.Incs, 0)
				assert.Len(t, fakes.Products.Bulk, 0)
			})
		}
	})

	t.Run("propagates a failure of any store", func(t *testing.T) {
		cases := map[string]func(f *testkit.FakeStores){
			"restaurant": func(f *testkit.FakeStores) { f.Restaurants.Err = errStore },
			"branch":     func(f *testkit.FakeStores) { f.Branches.Err = errStore },
			"platform":   func(f *testkit.FakeStores) { f.Platform.Err = errStore },
			"products":   func(f *testkit.FakeStores) { f.Products.Err = errStore },
			"timeline":   func(f *testkit.FakeStores) { f.Timelines.Err = errStore },
			"claim":      func(f *testkit.FakeStores) { f.Timelines.ClaimErr = errStore },
		}
		for name, breakStore := range cases {
			t.Run(name, func(t *testing.T) {
				// Arrange
				svc, fakes := newTestService()
				breakStore(fakes)

				// Act
				err := svc.OnOrderPlaced(context.Background(), placedInput("o-1", fixedNow))

				// Assert
				assert.ErrIs(t, err, errStore)
			})
		}
	})
}

func TestOnOrderDelivered(t *testing.T) {
	deliveredInput := func(orderID string, at time.Time) analytics.OnOrderDeliveredInput {
		return analytics.OnOrderDeliveredInput{OrderID: orderID, RestaurantID: 42, BranchID: 7, DeliveredAt: at}
	}

	t.Run("applies delivery time on the placed day once both facts are known", func(t *testing.T) {
		// Arrange
		svc, fakes := newTestService()
		placedAt := time.Date(2026, 10, 29, 23, 50, 0, 0, time.UTC)
		assert.NoErr(t, svc.OnOrderPlaced(context.Background(), placedInput("o-1", placedAt)))

		// Act — delivered after midnight: still counted on the placed day
		err := svc.OnOrderDelivered(context.Background(), deliveredInput("o-1", placedAt.Add(25*time.Minute)))

		// Assert
		assert.NoErr(t, err)
		last := fakes.Branches.Incs[len(fakes.Branches.Incs)-1]
		assert.Equal(t, last, testkit.DayInc{
			ID: 7, ParentID: 42, Date: "2026-10-29", Currency: "EGP",
			Delta: analytics.DayDelta{DeliveryMs: 25 * 60 * 1000, Deliveries: 1},
		})
	})

	t.Run("applies the delivery metric only once on a replayed delivery", func(t *testing.T) {
		// Arrange
		svc, fakes := newTestService()
		assert.NoErr(t, svc.OnOrderPlaced(context.Background(), placedInput("o-1", fixedNow)))
		assert.NoErr(t, svc.OnOrderDelivered(context.Background(), deliveredInput("o-1", fixedNow.Add(time.Hour))))

		// Act
		err := svc.OnOrderDelivered(context.Background(), deliveredInput("o-1", fixedNow.Add(time.Hour)))

		// Assert
		assert.NoErr(t, err)
		assert.Len(t, fakes.Restaurants.Incs, 2, "placed + one delivery")
	})

	t.Run("waits on the timeline when the order was not placed yet", func(t *testing.T) {
		// Arrange
		svc, fakes := newTestService()

		// Act
		err := svc.OnOrderDelivered(context.Background(), deliveredInput("o-1", fixedNow))

		// Assert
		assert.NoErr(t, err)
		assert.Len(t, fakes.Restaurants.Incs, 0)
		assert.True(t, fakes.Timelines.Get("o-1").DeliveredAt != nil)
	})

	t.Run("skips a negative delivery time caused by clock skew", func(t *testing.T) {
		// Arrange
		svc, fakes := newTestService()
		assert.NoErr(t, svc.OnOrderPlaced(context.Background(), placedInput("o-1", fixedNow)))

		// Act
		err := svc.OnOrderDelivered(context.Background(), deliveredInput("o-1", fixedNow.Add(-time.Minute)))

		// Assert
		assert.NoErr(t, err)
		assert.Len(t, fakes.Restaurants.Incs, 1, "only the placed increment")
		assert.True(t, fakes.Timelines.Get("o-1").DeliveryApplied, "claim is consumed for good")
	})

	t.Run("rejects an invalid payload", func(t *testing.T) {
		cases := map[string]analytics.OnOrderDeliveredInput{
			"missing orderId":     {RestaurantID: 42, DeliveredAt: fixedNow},
			"missing restaurant":  {OrderID: "o-1", DeliveredAt: fixedNow},
			"missing deliveredAt": {OrderID: "o-1", RestaurantID: 42},
		}
		for name, in := range cases {
			t.Run(name, func(t *testing.T) {
				// Arrange
				svc, _ := newTestService()

				// Act
				err := svc.OnOrderDelivered(context.Background(), in)

				// Assert
				assert.ErrIs(t, err, analytics.ErrInvalidEventPayload)
			})
		}
	})

	t.Run("propagates store failures", func(t *testing.T) {
		// Arrange
		svc, fakes := newTestService()
		assert.NoErr(t, svc.OnOrderPlaced(context.Background(), placedInput("o-1", fixedNow)))
		fakes.Restaurants.Err = errStore

		// Act
		errApply := svc.OnOrderDelivered(context.Background(), deliveredInput("o-1", fixedNow.Add(time.Minute)))
		fakes.Timelines.Err = errStore
		errRecord := svc.OnOrderDelivered(context.Background(), deliveredInput("o-2", fixedNow))

		// Assert
		assert.ErrIs(t, errApply, errStore)
		assert.ErrIs(t, errRecord, errStore)
	})
}

func TestOnOrderRejected(t *testing.T) {
	rejectedInput := func(orderID string) analytics.OnOrderRejectedInput {
		return analytics.OnOrderRejectedInput{OrderID: orderID, RestaurantID: 42, BranchID: 7, RejectedAt: fixedNow.Add(time.Minute)}
	}

	t.Run("counts the rejection and its revenue on the placed day", func(t *testing.T) {
		// Arrange
		svc, fakes := newTestService()
		placedAt := time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)
		assert.NoErr(t, svc.OnOrderPlaced(context.Background(), placedInput("o-1", placedAt)))

		// Act
		err := svc.OnOrderRejected(context.Background(), rejectedInput("o-1"))

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, fakes.Restaurants.Incs[1], testkit.DayInc{
			ID: 42, Date: "2026-10-28", Currency: "EGP",
			Delta: analytics.DayDelta{Rejected: 1, RejectedRevenue: 2500},
		})
	})

	t.Run("rejects an invalid payload", func(t *testing.T) {
		// Arrange
		svc, _ := newTestService()
		in := rejectedInput("o-1")
		in.RejectedAt = time.Time{}

		// Act
		err := svc.OnOrderRejected(context.Background(), in)

		// Assert
		assert.ErrIs(t, err, analytics.ErrInvalidEventPayload)
	})

	t.Run("propagates store failures", func(t *testing.T) {
		// Arrange
		svc, fakes := newTestService()
		assert.NoErr(t, svc.OnOrderPlaced(context.Background(), placedInput("o-1", fixedNow)))
		fakes.Platform.Err = errStore

		// Act
		errApply := svc.OnOrderRejected(context.Background(), rejectedInput("o-1"))
		fakes.Timelines.Err = errStore
		errRecord := svc.OnOrderRejected(context.Background(), rejectedInput("o-2"))

		// Assert
		assert.ErrIs(t, errApply, errStore)
		assert.ErrIs(t, errRecord, errStore)
	})
}

func TestOnPaymentCompleted(t *testing.T) {
	paymentInput := func() analytics.OnPaymentCompletedInput {
		return analytics.OnPaymentCompletedInput{
			OrderID: "o-1", RestaurantID: 42, BranchID: 7, AmountMinor: 4200, Currency: "EGP",
			CompletedAt: time.Date(2026, 10, 31, 0, 5, 0, 0, time.UTC),
		}
	}

	t.Run("adds the captured payment on the capture day without a timeline", func(t *testing.T) {
		// Arrange
		svc, fakes := newTestService()

		// Act
		err := svc.OnPaymentCompleted(context.Background(), paymentInput())

		// Assert
		assert.NoErr(t, err)
		want := analytics.DayDelta{Payments: 1, PaymentsSum: 4200}
		assert.Equal(t, fakes.Restaurants.Incs, []testkit.DayInc{{ID: 42, Date: "2026-10-31", Currency: "EGP", Delta: want}})
		assert.Equal(t, fakes.Platform.Incs[0].Delta, want)
		assert.True(t, fakes.Timelines.Get("o-1") == nil, "no timeline needed")
	})

	t.Run("rejects an invalid payload", func(t *testing.T) {
		cases := map[string]func(in *analytics.OnPaymentCompletedInput){
			"missing restaurant": func(in *analytics.OnPaymentCompletedInput) { in.RestaurantID = 0 },
			"missing currency":   func(in *analytics.OnPaymentCompletedInput) { in.Currency = "" },
			"negative amount":    func(in *analytics.OnPaymentCompletedInput) { in.AmountMinor = -1 },
			"zero completedAt":   func(in *analytics.OnPaymentCompletedInput) { in.CompletedAt = time.Time{} },
		}
		for name, mutate := range cases {
			t.Run(name, func(t *testing.T) {
				// Arrange
				svc, _ := newTestService()
				in := paymentInput()
				mutate(&in)

				// Act
				err := svc.OnPaymentCompleted(context.Background(), in)

				// Assert
				assert.ErrIs(t, err, analytics.ErrInvalidEventPayload)
			})
		}
	})

	t.Run("propagates store failures", func(t *testing.T) {
		// Arrange
		svc, fakes := newTestService()
		fakes.Restaurants.Err = errStore

		// Act
		err := svc.OnPaymentCompleted(context.Background(), paymentInput())

		// Assert
		assert.ErrIs(t, err, errStore)
	})
}
