//go:build integration

package boot

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/quickbite/analytics-service/internal/testkit"
	"github.com/quickbite/analytics-service/internal/testkit/assert"
	"github.com/quickbite/analytics-service/lib/config"
	"github.com/quickbite/analytics-service/pkg/messaging"
)

// Boot and shutdown paths of run(), each on its own namespaced topology and
// database so they never disturb the shared app.

// bootOnce runs run() for a config that is expected to fail during boot;
// the timeout only guards against a boot that unexpectedly succeeds.
func bootOnce(t *testing.T, suffix string, mutate func(c *config.Config)) error {
	t.Helper()
	port, err := freePort()
	assert.NoErr(t, err)
	cfg := testkit.Config(suffix, app.core.URL(), port)
	// Reuse the app's database: its indexes already exist, so EnsureIndexes is
	// a no-op (building them in a fresh database costs seconds), and a boot
	// that fails never writes a document.
	cfg.Mongo.Database = app.cfg.Mongo.Database
	mutate(&cfg)
	t.Cleanup(func() { cleanupTopology(cfg) })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return run(ctx, cfg, quietLogger())
}

func cleanupTopology(cfg config.Config) {
	r, c := cfg.Rabbit, cfg.CoreEvents
	app.amqp.DeleteQueues(r.Queue, r.DLQ, c.Queue, c.DLQ)
	app.amqp.DeleteExchanges(r.Exchange, r.DLX, c.Exchange, c.DLX)
}

// predeclarePlain declares queue WITHOUT a dead-letter exchange, so run()'s
// declaration (with one) fails with PRECONDITION_FAILED.
func predeclarePlain(t *testing.T, exchange, queue string) {
	t.Helper()
	b := messaging.NewAMQPBroker(messaging.Config{URL: testkit.RabbitURL()}, nil)
	assert.NoErr(t, b.Connect(context.Background()))
	defer b.Close(context.Background())
	assert.NoErr(t, b.DeclareTopology(messaging.Topology{Exchange: exchange, Queue: queue}))
}

func TestRun_BootFailures(t *testing.T) {
	t.Run("fails when MongoDB is unreachable", func(t *testing.T) {
		// Arrange
		mutate := func(c *config.Config) {
			c.Mongo.URI = "mongodb://127.0.0.1:1/?directConnection=true"
			c.Mongo.ConnectTimeout = 300 * time.Millisecond
		}

		// Act
		err := bootOnce(t, "run_nomongo", mutate)

		// Assert
		assert.Contains(t, err.Error(), "mongo ping")
	})

	t.Run("fails when RabbitMQ is unreachable", func(t *testing.T) {
		// Arrange
		mutate := func(c *config.Config) {
			c.Rabbit.URL = "amqp://guest:guest@127.0.0.1:1"
			c.Rabbit.ConnectAttempts = 1
		}

		// Act
		err := bootOnce(t, "run_norabbit", mutate)

		// Assert
		assert.Contains(t, err.Error(), "rabbit dial")
	})

	t.Run("fails on an invalid core-service URL", func(t *testing.T) {
		// Arrange
		mutate := func(c *config.Config) { c.Core.BaseURL = "core-service" }

		// Act
		err := bootOnce(t, "run_badcore", mutate)

		// Assert
		assert.Contains(t, err.Error(), "core client")
	})

	t.Run("fails when the order-events queue exists with other arguments", func(t *testing.T) {
		// Arrange
		cfg := testkit.Config("run_orderq", "", 0)
		predeclarePlain(t, cfg.Rabbit.Exchange, cfg.Rabbit.Queue)

		// Act
		err := bootOnce(t, "run_orderq", func(*config.Config) {})

		// Assert
		assert.Contains(t, err.Error(), "declare topology")
	})

	t.Run("fails when the core-events queue exists with other arguments", func(t *testing.T) {
		// Arrange
		cfg := testkit.Config("run_coreq", "", 0)
		predeclarePlain(t, cfg.CoreEvents.Exchange, cfg.CoreEvents.Queue)

		// Act
		err := bootOnce(t, "run_coreq", func(*config.Config) {})

		// Assert
		assert.Contains(t, err.Error(), "declare topology")
	})

	t.Run("shuts down and reports an HTTP port that is already taken", func(t *testing.T) {
		// Arrange
		taken, err := net.Listen("tcp", ":0")
		assert.NoErr(t, err)
		defer taken.Close()
		port := taken.Addr().(*net.TCPAddr).Port

		// Act
		err = bootOnce(t, "run_porttaken", func(c *config.Config) { c.Port = port })

		// Assert
		assert.Contains(t, err.Error(), "http server")
	})
}

func TestRun_Modes(t *testing.T) {
	t.Run("boots without transactions and without the core-events consumer", func(t *testing.T) {
		// Arrange
		a, err := startApp("run_minimal", app.mongo, func(c *config.Config) {
			c.Mongo.Transactions = "off"
			c.CoreEvents.Enabled = false
		})
		assert.NoErr(t, err)

		// Act
		res := testkit.GetURL(t, a.baseURL, "/health", nil)
		stopErr := a.stop()

		// Assert
		assert.Equal(t, res.Status, http.StatusOK)
		assert.NoErr(t, stopErr)
		assert.Equal(t, app.amqp.MessageCount(a.cfg.CoreEvents.Queue), 0)
	})

	t.Run("boots with MONGO_TRANSACTIONS=on against a replica set and stops on cancel", func(t *testing.T) {
		// Arrange
		a, err := startApp("run_txon", app.mongo, func(c *config.Config) { c.Mongo.Transactions = "on" })
		assert.NoErr(t, err)

		// Act
		stopErr := a.stop()

		// Assert
		assert.NoErr(t, stopErr)
		_, dialErr := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(a.cfg.Port), 200*time.Millisecond)
		assert.Err(t, dialErr, "listener closed after shutdown")
	})
}
