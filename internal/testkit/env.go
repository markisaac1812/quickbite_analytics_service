// Package testkit is the shared test helper kit: the Go analogue of
// order-service's tests/helpers (fixtures, factories, stubs, probes, wait).
//
// Test-only: nothing outside *_test.go files imports it. It may import any
// package of the module, so a test that uses it must live in a package
// testkit does not import (use the external `<pkg>_test` package when in
// doubt) — otherwise Go reports an import cycle.
//
// Unit tests need nothing running. Integration tests (build tag
// `integration`) need MongoDB (replica set for the transaction tests) and
// RabbitMQ; point them elsewhere with TEST_MONGO_URI / TEST_RABBITMQ_URL.
package testkit

import (
	"fmt"
	"os"
	"time"

	"github.com/quickbite/analytics-service/lib/config"
)

// Shared secrets used by every test (never real values).
const (
	AccessSecret   = "test-access-secret-not-for-production"
	InternalAPIKey = "test-internal-api-key"
)

// MongoURI is the test MongoDB (≈ .env.test on the Node side).
func MongoURI() string {
	return envOr("TEST_MONGO_URI", "mongodb://localhost:27017/?directConnection=true")
}

// RabbitURL is the test RabbitMQ broker.
func RabbitURL() string {
	return envOr("TEST_RABBITMQ_URL", "amqp://guest:guest@localhost:5672")
}

// DBName is a per-package test database, so packages can run in parallel
// (`go test ./...` runs packages concurrently) without wiping each other.
func DBName(suffix string) string {
	return "quickbite_analytics_test_" + suffix
}

// Config returns a full service config pointed at test infrastructure, with
// every queue/exchange name namespaced by suffix so the test never touches
// the dev topology (order.events, analytics-service.order-events, …).
func Config(suffix, coreBaseURL string, port int) config.Config {
	ns := "analytics-test-" + suffix
	return config.Config{
		Port:            port,
		AppEnv:          "test",
		LogLevel:        "error",
		ShutdownTimeout: 5 * time.Second,
		JWT:             config.JWTConfig{AccessSecret: AccessSecret},
		Internal:        config.InternalConfig{APIKey: InternalAPIKey},
		Mongo: config.MongoConfig{
			URI:            MongoURI(),
			Database:       DBName(suffix),
			ConnectTimeout: 5 * time.Second,
			MaxPoolSize:    20,
			Transactions:   "auto",
		},
		Rabbit: config.RabbitConfig{
			URL:             RabbitURL(),
			Exchange:        ns + ".order.events",
			Queue:           ns + ".order-events",
			Bindings:        []string{"order.#", "payment.#"},
			DLX:             ns + ".order-events.dlx",
			DLQ:             ns + ".order-events.dlq",
			Prefetch:        4,
			ConnectAttempts: 2,
			ReconnectMax:    time.Second,
		},
		CoreEvents: config.CoreEventsConfig{
			Enabled:  true,
			Exchange: ns + ".core.events",
			Queue:    ns + ".core-events",
			Bindings: []string{"rbac.#"},
			DLX:      ns + ".core-events.dlx",
			DLQ:      ns + ".core-events.dlq",
			Prefetch: 2,
		},
		Core: config.CoreConfig{
			BaseURL:        coreBaseURL,
			InternalAPIKey: InternalAPIKey,
			Timeout:        time.Second,
			Attempts:       2,
		},
		RBAC:   config.RBACConfig{CacheTTL: 5 * time.Minute},
		Events: config.EventsConfig{HandlerTimeout: 5 * time.Second},
	}
}

// Day returns the UTC midnight of y-m-d.
func Day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// DateKey formats t as the "YYYY-MM-DD" UTC day key.
func DateKey(t time.Time) string { return t.UTC().Format("2006-01-02") }

// Must panics on err; for TestMain setup where there is no *testing.T.
func Must[T any](v T, err error) T {
	if err != nil {
		panic(fmt.Sprintf("testkit: %v", err))
	}
	return v
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
