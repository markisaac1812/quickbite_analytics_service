//go:build integration

package boot

import (
	"net/http"
	"testing"
	"time"

	"github.com/quickbite/analytics-service/app/analytics"
	"github.com/quickbite/analytics-service/app/analytics/dto"
	"github.com/quickbite/analytics-service/app/analytics/repository"
	"github.com/quickbite/analytics-service/internal/testkit"
	"github.com/quickbite/analytics-service/internal/testkit/assert"
)

// Events travel the real path: RabbitMQ → consumer → dispatcher (dedupe +
// transaction) → handler → service → Mongo, and are read back over HTTP.

const octRange = "?from=2026-10-01&to=2026-10-31"

var oct5 = time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)

func restaurantDays(t *testing.T) []dto.RestaurantDayResponse {
	t.Helper()
	res := app.get(t, "/restaurants/42/days"+octRange, &testkit.Owner)
	assert.Equal(t, res.Status, http.StatusOK)
	var days []dto.RestaurantDayResponse
	res.DataAs(t, &days)
	return days
}

func TestEvents_OrderPlaced(t *testing.T) {
	t.Run("lands on the restaurant, branch, product and platform day", func(t *testing.T) {
		// Arrange
		app.reset(t)
		env := testkit.Envelope(analytics.EventOrderPlaced, testkit.Placed("o-1", oct5))

		// Act
		app.publish(t, env)
		app.waitApplied(t, env.EventID)

		// Assert
		assert.Equal(t, restaurantDays(t), []dto.RestaurantDayResponse{{Date: "2026-10-05", OrdersCount: 1, RevenueMinor: 2500, Currency: "EGP", AvgOrderMinor: 2500}})
		assert.Equal(t, app.mongo.Count(repository.CollBranchDay, map[string]any{"branch_id": 7, "orders_count": 1}), int64(1))
		assert.Equal(t, app.mongo.Count(repository.CollProductDay, map[string]any{"restaurant_id": 42}), int64(2))
		assert.Equal(t, app.mongo.Count(repository.CollPlatformDay, map[string]any{"date": "2026-10-05", "currency": "EGP"}), int64(1))
	})

	t.Run("counts a redelivered event once", func(t *testing.T) {
		// Arrange
		app.reset(t)
		dup := testkit.Envelope(analytics.EventOrderPlaced, testkit.Placed("o-1", oct5))
		sentinel := testkit.Envelope(analytics.EventOrderPlaced, testkit.Placed("o-2", oct5))

		// Act
		app.publish(t, dup)
		app.publish(t, dup)
		app.publish(t, sentinel)
		app.waitApplied(t, sentinel.EventID)

		// Assert
		assert.Equal(t, restaurantDays(t)[0].OrdersCount, int64(2))
		assert.Equal(t, app.amqp.MessageCount(app.cfg.Rabbit.DLQ), 0)
	})
}

func TestEvents_TwoEventMetrics(t *testing.T) {
	t.Run("settles a delivery that arrives before its order", func(t *testing.T) {
		// Arrange
		app.reset(t)
		delivered := testkit.Envelope(analytics.EventOrderDelivered, testkit.Delivered("o-1", oct5.Add(42*time.Minute)))
		placed := testkit.Envelope(analytics.EventOrderPlaced, testkit.Placed("o-1", oct5))

		// Act
		app.publish(t, delivered)
		app.publish(t, placed)
		app.waitApplied(t, placed.EventID)

		// Assert
		res := app.get(t, "/restaurants/42/delivery-avg"+octRange, &testkit.Owner)
		var rep dto.DeliveryReportResponse
		res.DataAs(t, &rep)
		assert.Equal(t, rep.DeliveredCount, int64(1))
		assert.Equal(t, rep.AvgDeliveryMs, int64(42*60*1000))
	})

	t.Run("counts a rejection on the day the order was placed", func(t *testing.T) {
		// Arrange
		app.reset(t)
		placed := testkit.Envelope(analytics.EventOrderPlaced, testkit.Placed("o-1", oct5))
		rejected := testkit.Envelope(analytics.EventOrderRejected, testkit.Rejected("o-1", oct5.Add(30*time.Hour)))

		// Act
		app.publish(t, placed)
		app.publish(t, rejected)
		app.waitApplied(t, rejected.EventID)

		// Assert
		res := app.get(t, "/restaurants/42/failures"+octRange, &testkit.Owner)
		var rep dto.FailureReportResponse
		res.DataAs(t, &rep)
		assert.Equal(t, rep.Days, []dto.FailureDayResponse{{Date: "2026-10-05", OrdersCount: 1, RejectedCount: 1, FailureRate: 1}})
	})

	t.Run("applies a payment that arrives before its order on the capture day", func(t *testing.T) {
		// Arrange
		app.reset(t)
		paid := testkit.Envelope(analytics.EventPaymentCompleted, testkit.PaymentCompleted("o-1", 2500, oct5.Add(-time.Minute)))

		// Act
		app.publish(t, paid)
		app.waitApplied(t, paid.EventID)

		// Assert
		res := app.get(t, "/restaurants/42/payments"+octRange, &testkit.Owner)
		var rep dto.PaymentsReportResponse
		res.DataAs(t, &rep)
		assert.Equal(t, rep.Days, []dto.PaymentDayResponse{{Date: "2026-10-05", PaymentsCount: 1, PaymentsMinor: 2500, Currency: "EGP"}})
	})
}

func TestEvents_Failures(t *testing.T) {
	t.Run("dead-letters an invalid payload and rolls its dedupe marker back", func(t *testing.T) {
		// Arrange
		app.reset(t)
		p := testkit.Placed("o-1", oct5)
		p.Currency = ""
		env := testkit.Envelope(analytics.EventOrderPlaced, p)

		// Act
		app.publish(t, env)
		app.waitDeadLettered(t, app.cfg.Rabbit.DLQ, 1)

		// Assert — the transaction rolled back the marker, so a DLQ replay is not skipped
		assert.Equal(t, app.mongo.Count(repository.CollEventIDs, map[string]any{"event_id": env.EventID}), int64(0))
		assert.Equal(t, app.mongo.Count(repository.CollRestaurantDay, nil), int64(0))
	})

	t.Run("dead-letters a malformed envelope", func(t *testing.T) {
		// Arrange
		app.reset(t)

		// Act
		app.publishRaw(t, app.cfg.Rabbit.Exchange, "order.placed", []byte(`{"eventType":"order.placed"}`))

		// Assert
		app.waitDeadLettered(t, app.cfg.Rabbit.DLQ, 1)
	})

	t.Run("acks an event type it has no handler for", func(t *testing.T) {
		// Arrange
		app.reset(t)
		accepted := testkit.Envelope("order.accepted", map[string]any{"orderId": "o-1"})
		sentinel := testkit.Envelope(analytics.EventOrderPlaced, testkit.Placed("o-2", oct5))

		// Act
		app.publish(t, accepted)
		app.publish(t, sentinel)
		app.waitApplied(t, sentinel.EventID)

		// Assert
		assert.Equal(t, app.amqp.MessageCount(app.cfg.Rabbit.DLQ), 0)
		assert.Equal(t, app.amqp.MessageCount(app.cfg.Rabbit.Queue), 0)
	})
}

func TestEvents_PermissionsChanged(t *testing.T) {
	t.Run("rbac.permissions_changed drops the cached role so the next request sees core's new grant", func(t *testing.T) {
		// Arrange
		app.reset(t)
		days := "/restaurants/42/days" + octRange
		assert.Equal(t, app.get(t, days, &testkit.Staff).Status, http.StatusForbidden) // caches staff = [orders:read]
		app.core.SetPermissions("staff", "orders:read", "analytics:read")
		assert.Equal(t, app.get(t, days, &testkit.Staff).Status, http.StatusForbidden, "still cached")
		t.Cleanup(func() {
			app.core.Reset()
			app.publishRaw(t, app.cfg.CoreEvents.Exchange, "rbac.permissions_changed", []byte(`{"eventId":"cleanup","eventType":"rbac.permissions_changed","payload":{}}`))
			testkit.Eventually(t, 10*time.Second, func() bool { return app.get(t, days, &testkit.Staff).Status == http.StatusForbidden }, "staff revoked again")
		})

		// Act
		app.publishRaw(t, app.cfg.CoreEvents.Exchange, "rbac.permissions_changed",
			[]byte(`{"eventId":"perm-1","eventType":"rbac.permissions_changed","payload":{"role":"staff"}}`))

		// Assert
		testkit.Eventually(t, 10*time.Second, func() bool { return app.get(t, days, &testkit.Staff).Status == http.StatusOK }, "staff granted")
	})

	t.Run("dead-letters a malformed core event", func(t *testing.T) {
		// Arrange
		app.reset(t)

		// Act
		app.publishRaw(t, app.cfg.CoreEvents.Exchange, "rbac.permissions_changed",
			[]byte(`{"eventId":"perm-2","eventType":"rbac.permissions_changed","payload":"not an object"}`))

		// Assert
		app.waitDeadLettered(t, app.cfg.CoreEvents.DLQ, 1)
	})
}
