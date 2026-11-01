package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/quickbite/analytics-service/internal/testkit/assert"
)

// requiredEnv sets the four variables Load cannot default.
func requiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ACCESS_SECRET", "s3cret")
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672")
	t.Setenv("CORE_SERVICE_BASE_URL", "http://localhost:3000")
	t.Setenv("CORE_INTERNAL_API_KEY", "k3y")
}

// inEmptyDir runs the test from a temp dir so no developer .env leaks in.
func inEmptyDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

func TestLoad(t *testing.T) {
	t.Run("applies the documented defaults", func(t *testing.T) {
		// Arrange
		inEmptyDir(t)
		requiredEnv(t)

		// Act
		cfg, err := Load()

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, cfg.Port, 4002)
		assert.Equal(t, cfg.Mongo.Transactions, "auto")
		assert.Equal(t, cfg.Rabbit.Bindings, []string{"order.#", "payment.#"})
		assert.Equal(t, cfg.Rabbit.DLX, "analytics-service.order-events.dlx")
		assert.Equal(t, cfg.CoreEvents.Bindings, []string{"rbac.#"})
		assert.Equal(t, cfg.RBAC.CacheTTL, 5*time.Minute)
		assert.Equal(t, cfg.Events.HandlerTimeout, 10*time.Second)
		assert.False(t, cfg.IsProduction())
	})

	t.Run("reads overrides from the environment", func(t *testing.T) {
		// Arrange
		inEmptyDir(t)
		requiredEnv(t)
		t.Setenv("APP_ENV", "production")
		t.Setenv("PORT", "9000")
		t.Setenv("RABBITMQ_ORDER_EVENTS_BINDINGS", "order.placed,payment.completed")
		t.Setenv("MONGO_TRANSACTIONS", "off")

		// Act
		cfg, err := Load()

		// Assert
		assert.NoErr(t, err)
		assert.True(t, cfg.IsProduction())
		assert.Equal(t, cfg.Port, 9000)
		assert.Equal(t, cfg.Rabbit.Bindings, []string{"order.placed", "payment.completed"})
		assert.Equal(t, cfg.Mongo.Transactions, "off")
	})

	t.Run("fails when a required secret is missing", func(t *testing.T) {
		// Arrange
		inEmptyDir(t)
		requiredEnv(t)
		t.Setenv("ACCESS_SECRET", "")

		// Act
		_, err := Load()

		// Assert
		assert.Err(t, err)
		assert.Contains(t, err.Error(), "ACCESS_SECRET")
	})

	t.Run("fails on a prefetch below 1", func(t *testing.T) {
		// Arrange
		inEmptyDir(t)
		requiredEnv(t)
		t.Setenv("RABBITMQ_CORE_EVENTS_PREFETCH", "0")

		// Act
		_, err := Load()

		// Assert
		assert.Contains(t, err.Error(), "PREFETCH must be >= 1")
	})

	t.Run("fails on an unknown MONGO_TRANSACTIONS mode", func(t *testing.T) {
		// Arrange
		inEmptyDir(t)
		requiredEnv(t)
		t.Setenv("MONGO_TRANSACTIONS", "maybe")

		// Act
		_, err := Load()

		// Assert
		assert.Contains(t, err.Error(), `MONGO_TRANSACTIONS must be auto, on or off (got "maybe")`)
	})

	t.Run("fails on an unparsable duration", func(t *testing.T) {
		// Arrange
		inEmptyDir(t)
		requiredEnv(t)
		t.Setenv("RBAC_CACHE_TTL", "five minutes")

		// Act
		_, err := Load()

		// Assert
		assert.Err(t, err)
	})
}

func TestLoad_DotEnv(t *testing.T) {
	t.Run("reads .env but lets real environment variables win", func(t *testing.T) {
		// Arrange
		dir := inEmptyDir(t)
		requiredEnv(t)
		t.Setenv("LOG_LEVEL", "warn") // real env
		content := "# comment\n\nexport PORT=4555\nLOG_LEVEL=debug\nMONGO_DB=\"quoted_db\"\nAPP_ENV='staging'\n"
		assert.NoErr(t, os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0o600))
		// variables set by loadDotEnv are not restored by t.Setenv — unset them after
		t.Cleanup(func() {
			for _, k := range []string{"PORT", "MONGO_DB", "APP_ENV"} {
				_ = os.Unsetenv(k)
			}
		})

		// Act
		cfg, err := Load()

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, cfg.Port, 4555)
		assert.Equal(t, cfg.LogLevel, "warn")
		assert.Equal(t, cfg.Mongo.Database, "quoted_db")
		assert.Equal(t, cfg.AppEnv, "staging")
	})

	t.Run("fails on a line that is not KEY=VALUE", func(t *testing.T) {
		// Arrange
		dir := inEmptyDir(t)
		requiredEnv(t)
		assert.NoErr(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("JUST_A_KEY\n"), 0o600))

		// Act
		_, err := Load()

		// Assert
		assert.Contains(t, err.Error(), ".env:1: expected KEY=VALUE")
	})

	t.Run("fails when .env exists but cannot be opened", func(t *testing.T) {
		// Arrange — a directory named .env cannot be read as a file
		dir := inEmptyDir(t)
		requiredEnv(t)
		assert.NoErr(t, os.Mkdir(filepath.Join(dir, ".env"), 0o700))

		// Act
		_, err := Load()

		// Assert
		assert.Err(t, err)
	})
}
