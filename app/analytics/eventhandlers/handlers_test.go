package eventhandlers_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/quickbite/analytics-service/app/analytics"
	"github.com/quickbite/analytics-service/app/analytics/eventhandlers"
	"github.com/quickbite/analytics-service/app/analytics/service"
	"github.com/quickbite/analytics-service/internal/testkit"
	"github.com/quickbite/analytics-service/internal/testkit/assert"
	"github.com/quickbite/analytics-service/lib/coreevents"
)

var placedAt = time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)

func newDispatcher(t *testing.T) (*coreevents.Dispatcher, *testkit.FakeStores) {
	t.Helper()
	fakes := testkit.NewFakeStores()
	d := coreevents.NewDispatcher(coreevents.NoDedupe{}, nil, 5*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	assert.NoErr(t, eventhandlers.Register(d, service.New(fakes.Stores())))
	return d, fakes
}

func TestRegister(t *testing.T) {
	t.Run("registers the four consumed event types", func(t *testing.T) {
		// Arrange
		d, _ := newDispatcher(t)

		// Act
		types := d.RegisteredTypes()

		// Assert
		assert.Len(t, types, 4)
		for _, want := range []string{analytics.EventOrderPlaced, analytics.EventOrderDelivered, analytics.EventOrderRejected, analytics.EventPaymentCompleted} {
			found := false
			for _, got := range types {
				found = found || got == want
			}
			assert.True(t, found, want+" registered")
		}
	})

	t.Run("refuses to register the module twice on one dispatcher", func(t *testing.T) {
		// Arrange
		d, fakes := newDispatcher(t)

		// Act
		err := eventhandlers.Register(d, service.New(fakes.Stores()))

		// Assert
		assert.Err(t, err)
	})
}

func TestOrderPlacedHandler(t *testing.T) {
	t.Run("decodes the payload and applies the order with its lines", func(t *testing.T) {
		// Arrange
		d, fakes := newDispatcher(t)
		env := testkit.Envelope(analytics.EventOrderPlaced, testkit.Placed("o-1", placedAt))

		// Act
		outcome, err := d.Apply(context.Background(), env)

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, outcome, coreevents.OutcomeApplied)
		assert.Equal(t, fakes.Restaurants.Incs[0], testkit.DayInc{
			ID: 42, Date: "2026-10-29", Currency: "EGP", Delta: analytics.DayDelta{Orders: 1, Revenue: 2500},
		})
		assert.Equal(t, fakes.Products.Bulk[0].Lines, []analytics.OrderLine{
			{ProductID: 1, Quantity: 2, LineTotalMinor: 2000}, {ProductID: 2, Quantity: 1, LineTotalMinor: 500},
		})
	})

	t.Run("falls back to the envelope's occurredAt when placedAt is missing", func(t *testing.T) {
		// Arrange
		d, fakes := newDispatcher(t)
		p := testkit.Placed("o-1", placedAt)
		p.PlacedAt = ""
		env := testkit.Envelope(analytics.EventOrderPlaced, p)
		env.OccurredAt = "2026-10-20T08:00:00Z"

		// Act
		_, err := d.Apply(context.Background(), env)

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, fakes.Restaurants.Incs[0].Date, "2026-10-20")
	})

	t.Run("fails with an invalid-payload error when no timestamp parses", func(t *testing.T) {
		// Arrange
		d, _ := newDispatcher(t)
		p := testkit.Placed("o-1", placedAt)
		p.PlacedAt = "yesterday"
		env := testkit.Envelope(analytics.EventOrderPlaced, p)
		env.OccurredAt = ""

		// Act
		outcome, err := d.Apply(context.Background(), env)

		// Assert
		assert.Equal(t, outcome, coreevents.OutcomeFailed)
		assert.ErrIs(t, err, analytics.ErrInvalidEventPayload)
	})
}

func TestTransitionHandlers(t *testing.T) {
	t.Run("order.delivered records the delivery and settles the delivery time", func(t *testing.T) {
		// Arrange
		d, fakes := newDispatcher(t)
		_, err := d.Apply(context.Background(), testkit.Envelope(analytics.EventOrderPlaced, testkit.Placed("o-1", placedAt)))
		assert.NoErr(t, err)
		env := testkit.Envelope(analytics.EventOrderDelivered, testkit.Delivered("o-1", placedAt.Add(20*time.Minute)))

		// Act
		_, err = d.Apply(context.Background(), env)

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, fakes.Restaurants.Incs[1].Delta, analytics.DayDelta{DeliveryMs: 20 * 60 * 1000, Deliveries: 1})
	})

	t.Run("order.rejected uses the payload's occurredAt", func(t *testing.T) {
		// Arrange
		d, fakes := newDispatcher(t)
		env := testkit.Envelope(analytics.EventOrderRejected, testkit.Rejected("o-1", placedAt.Add(time.Minute)))

		// Act
		_, err := d.Apply(context.Background(), env)

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, *fakes.Timelines.Get("o-1").RejectedAt, placedAt.Add(time.Minute))
	})

	t.Run("payment.completed applies the captured amount", func(t *testing.T) {
		// Arrange
		d, fakes := newDispatcher(t)
		env := testkit.Envelope(analytics.EventPaymentCompleted, testkit.PaymentCompleted("o-1", 4200, placedAt))

		// Act
		_, err := d.Apply(context.Background(), env)

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, fakes.Platform.Incs[0].Delta, analytics.DayDelta{Payments: 1, PaymentsSum: 4200})
	})
}

func TestHandlers_InvalidPayloads(t *testing.T) {
	types := []string{analytics.EventOrderPlaced, analytics.EventOrderDelivered, analytics.EventOrderRejected, analytics.EventPaymentCompleted}

	for _, eventType := range types {
		t.Run(eventType+" dead-letters a payload that is not an object", func(t *testing.T) {
			// Arrange
			d, _ := newDispatcher(t)
			env := testkit.Envelope(eventType, "just a string")

			// Act
			outcome, err := d.Apply(context.Background(), env)

			// Assert
			assert.Equal(t, outcome, coreevents.OutcomeFailed)
			assert.ErrIs(t, err, analytics.ErrInvalidEventPayload)
		})

		t.Run(eventType+" dead-letters a payload without any usable timestamp", func(t *testing.T) {
			// Arrange
			d, _ := newDispatcher(t)
			env := testkit.Envelope(eventType, json.RawMessage(`{"orderId":"o-1","restaurantId":42}`))
			env.OccurredAt = "not-a-time"

			// Act
			_, err := d.Apply(context.Background(), env)

			// Assert
			assert.ErrIs(t, err, analytics.ErrInvalidEventPayload)
		})
	}
}
