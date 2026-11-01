//go:build integration

package boot

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/quickbite/analytics-service/app/analytics"
	"github.com/quickbite/analytics-service/app/analytics/dto"
	"github.com/quickbite/analytics-service/app/analytics/repository"
	"github.com/quickbite/analytics-service/internal/testkit"
	"github.com/quickbite/analytics-service/internal/testkit/assert"
)

// history is one exported slice of order-service's events_outbox: two
// orders (one delivered after 30 min, one rejected) and an online payment.
func history() [][]byte {
	return [][]byte{
		testkit.JSON(testkit.Envelope(analytics.EventOrderPlaced, testkit.Placed("o-1", oct5))),
		testkit.JSON(testkit.Envelope(analytics.EventOrderDelivered, testkit.Delivered("o-1", oct5.Add(30*time.Minute)))),
		testkit.JSON(testkit.Envelope(analytics.EventOrderPlaced, testkit.Placed("o-2", oct5))),
		testkit.JSON(testkit.Envelope(analytics.EventOrderRejected, testkit.Rejected("o-2", oct5.Add(time.Minute)))),
		testkit.JSON(testkit.Envelope(analytics.EventPaymentCompleted, testkit.PaymentCompleted("o-3", 4200, oct5))),
		testkit.JSON(testkit.Envelope("order.accepted", map[string]string{"orderId": "o-1"})),
	}
}

func ndjson(t *testing.T, lines ...[]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.ndjson")
	assert.NoErr(t, os.WriteFile(path, append(bytes.Join(lines, []byte("\n")), '\n'), 0o600))
	return path
}

// report reads back what the backfill built, over the running API.
type report struct {
	days     []dto.RestaurantDayResponse
	delivery dto.DeliveryReportResponse
	failures dto.FailureReportResponse
	payments dto.PaymentsReportResponse
}

func readReport(t *testing.T) report {
	t.Helper()
	var r report
	app.get(t, "/restaurants/42/days"+octRange, &testkit.Owner).DataAs(t, &r.days)
	app.get(t, "/restaurants/42/delivery-avg"+octRange, &testkit.Owner).DataAs(t, &r.delivery)
	app.get(t, "/restaurants/42/failures"+octRange, &testkit.Owner).DataAs(t, &r.failures)
	app.get(t, "/restaurants/42/payments"+octRange, &testkit.Owner).DataAs(t, &r.payments)
	return r
}

func assertHistoryApplied(t *testing.T, r report) {
	t.Helper()
	assert.Equal(t, r.days, []dto.RestaurantDayResponse{{Date: "2026-10-05", OrdersCount: 2, RevenueMinor: 5000, Currency: "EGP", AvgOrderMinor: 2500}})
	assert.Equal(t, r.delivery.AvgDeliveryMs, int64(30*60*1000))
	assert.Equal(t, r.failures.RejectedCount, int64(1))
	assert.Equal(t, r.payments.PaymentsMinor, int64(4200))
}

func TestBackfill(t *testing.T) {
	t.Run("-reset -yes wipes stale aggregates and rebuilds them from the export", func(t *testing.T) {
		// Arrange
		app.reset(t)
		stale := repository.NewRestaurantDayRepo(app.mongo.DB)
		assert.NoErr(t, stale.Increment(context.Background(), 42, "2026-10-05", "EGP", analytics.DayDelta{Orders: 99}, oct5))
		input := ndjson(t, history()...)

		// Act
		err := runBackfill(app.cfg, BackfillOptions{Input: input, Reset: true, Yes: true}, quietLogger())

		// Assert
		assert.NoErr(t, err)
		assertHistoryApplied(t, readReport(t))
	})

	t.Run("gives the same result whatever order the lines are in", func(t *testing.T) {
		// Arrange
		app.reset(t)
		lines := history()
		slices.Reverse(lines)
		input := ndjson(t, lines...)

		// Act
		err := runBackfill(app.cfg, BackfillOptions{Input: input}, quietLogger())

		// Assert
		assert.NoErr(t, err)
		assertHistoryApplied(t, readReport(t))
	})

	t.Run("catch-up mode skips events that were already applied", func(t *testing.T) {
		// Arrange
		app.reset(t)
		input := ndjson(t, history()...)
		assert.NoErr(t, runBackfill(app.cfg, BackfillOptions{Input: input}, quietLogger()))

		// Act
		err := runBackfill(app.cfg, BackfillOptions{Input: input}, quietLogger())

		// Assert
		assert.NoErr(t, err)
		assertHistoryApplied(t, readReport(t))
	})

	t.Run("-dry-run parses everything and writes nothing", func(t *testing.T) {
		// Arrange
		app.reset(t)
		input := ndjson(t, history()...)

		// Act
		err := runBackfill(app.cfg, BackfillOptions{Input: input, DryRun: true, Reset: true, Yes: true}, quietLogger())

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, app.mongo.Count(repository.CollEventIDs, nil), int64(0))
		assert.Equal(t, app.mongo.Count(repository.CollRestaurantDay, nil), int64(0))
	})

	t.Run("applies the good lines, skips blank ones and reports the bad ones", func(t *testing.T) {
		// Arrange
		app.reset(t)
		input := ndjson(t, history()[0], []byte(""), []byte("not json"))

		// Act
		err := runBackfill(app.cfg, BackfillOptions{Input: input}, quietLogger())

		// Assert
		assert.Contains(t, err.Error(), "1 line(s) failed")
		assert.Equal(t, readReport(t).days[0].OrdersCount, int64(1))
	})

	t.Run("-stop-on-error aborts at the first malformed line", func(t *testing.T) {
		// Arrange
		app.reset(t)
		input := ndjson(t, history()[0], []byte(`{"eventType":"order.placed"}`), history()[2])

		// Act
		err := runBackfill(app.cfg, BackfillOptions{Input: input, StopOnError: true}, quietLogger())

		// Assert
		assert.Contains(t, err.Error(), "line 2: malformed envelope")
		assert.Equal(t, readReport(t).days[0].OrdersCount, int64(1), "line 3 never ran")
	})

	t.Run("-stop-on-error aborts at the first failed event", func(t *testing.T) {
		// Arrange
		app.reset(t)
		bad := testkit.Placed("o-1", oct5)
		bad.RestaurantID = 0
		env := testkit.Envelope(analytics.EventOrderPlaced, bad)
		input := ndjson(t, testkit.JSON(env))

		// Act
		err := runBackfill(app.cfg, BackfillOptions{Input: input, StopOnError: true}, quietLogger())

		// Assert
		assert.Contains(t, err.Error(), "line 1 ("+env.EventID+")")
	})

	t.Run("without transactions, forgets the marker of a failed event so it can be replayed", func(t *testing.T) {
		// Arrange
		app.reset(t)
		cfg := app.cfg
		cfg.Mongo.Transactions = "off"
		bad := testkit.Placed("o-1", oct5)
		bad.Items[0].Quantity = 0
		env := testkit.Envelope(analytics.EventOrderPlaced, bad)
		input := ndjson(t, testkit.JSON(env))

		// Act
		err := runBackfill(cfg, BackfillOptions{Input: input}, quietLogger())

		// Assert
		assert.Contains(t, err.Error(), "1 line(s) failed")
		assert.Equal(t, app.mongo.Count(repository.CollEventIDs, map[string]any{"event_id": env.EventID}), int64(0))
	})

	t.Run("MONGO_TRANSACTIONS=on runs on a replica set", func(t *testing.T) {
		// Arrange
		app.reset(t)
		cfg := app.cfg
		cfg.Mongo.Transactions = "on"

		// Act
		err := runBackfill(cfg, BackfillOptions{Input: ndjson(t, history()...)}, quietLogger())

		// Assert
		assert.NoErr(t, err)
	})

	t.Run("fails when MongoDB is unreachable", func(t *testing.T) {
		// Arrange
		cfg := app.cfg
		cfg.Mongo.URI = "mongodb://127.0.0.1:1/?directConnection=true"
		cfg.Mongo.ConnectTimeout = 300 * time.Millisecond

		// Act
		err := runBackfill(cfg, BackfillOptions{Input: ndjson(t, history()...)}, quietLogger())

		// Assert
		assert.Contains(t, err.Error(), "mongo ping")
	})
}

func TestRunBackfill_FromEnvironment(t *testing.T) {
	// Arrange — the exported entrypoint reads config from env like cmd/backfill-aggs
	app.reset(t)
	c := app.cfg
	for k, v := range map[string]string{
		"ACCESS_SECRET": c.JWT.AccessSecret, "RABBITMQ_URL": c.Rabbit.URL,
		"CORE_SERVICE_BASE_URL": c.Core.BaseURL, "CORE_INTERNAL_API_KEY": c.Core.InternalAPIKey,
		"MONGO_URI": c.Mongo.URI, "MONGO_DB": c.Mongo.Database,
		"EVENT_HANDLER_TIMEOUT": "5s",
	} {
		t.Setenv(k, v)
	}
	input := ndjson(t, history()...)

	// Act
	ok := RunBackfill(BackfillOptions{Input: input, LogLevel: "error"})
	bad := RunBackfill(BackfillOptions{Input: ndjson(t, []byte("garbage")), LogLevel: "error"})

	// Assert
	assert.NoErr(t, ok)
	assert.Err(t, bad)
	assertHistoryApplied(t, readReport(t))
}
