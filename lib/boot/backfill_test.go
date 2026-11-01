package boot

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/quickbite/analytics-service/internal/testkit"
	"github.com/quickbite/analytics-service/internal/testkit/assert"
)

func TestOpenInput(t *testing.T) {
	t.Run("requires a path", func(t *testing.T) {
		// Arrange
		path := ""

		// Act
		_, _, err := openInput(path)

		// Assert
		assert.Contains(t, err.Error(), "-input is required")
	})

	t.Run("reads stdin for -", func(t *testing.T) {
		// Arrange
		path := "-"

		// Act
		r, closeIn, err := openInput(path)

		// Assert
		assert.NoErr(t, err)
		assert.True(t, r == os.Stdin)
		closeIn()
	})

	t.Run("opens a file", func(t *testing.T) {
		// Arrange
		path := filepath.Join(t.TempDir(), "events.ndjson")
		assert.NoErr(t, os.WriteFile(path, []byte("{}\n"), 0o600))

		// Act
		r, closeIn, err := openInput(path)

		// Assert
		assert.NoErr(t, err)
		assert.True(t, r != nil)
		closeIn()
	})

	t.Run("fails on a missing file", func(t *testing.T) {
		// Arrange
		path := filepath.Join(t.TempDir(), "missing.ndjson")

		// Act
		_, _, err := openInput(path)

		// Assert
		assert.Contains(t, err.Error(), "open input")
	})
}

func TestRunBackfill_Guards(t *testing.T) {
	t.Run("refuses -reset without -yes before touching anything", func(t *testing.T) {
		// Arrange
		cfg := testkit.Config("guard", "http://127.0.0.1:1", 0)

		// Act
		err := runBackfill(cfg, BackfillOptions{Input: "whatever", Reset: true}, discard)

		// Assert
		assert.Contains(t, err.Error(), "refusing to -reset without -yes")
	})

	t.Run("fails before connecting when the input is missing", func(t *testing.T) {
		// Arrange
		cfg := testkit.Config("guard", "http://127.0.0.1:1", 0)

		// Act
		err := runBackfill(cfg, BackfillOptions{Input: filepath.Join(t.TempDir(), "nope.ndjson")}, discard)

		// Assert
		assert.Contains(t, err.Error(), "open input")
	})
}

func TestEntrypoints_ConfigErrors(t *testing.T) {
	// Arrange — an empty ACCESS_SECRET makes config.Load fail (lib/boot has no .env)
	t.Setenv("ACCESS_SECRET", "")
	t.Setenv("RABBITMQ_URL", "amqp://localhost")
	t.Setenv("CORE_SERVICE_BASE_URL", "http://localhost:3000")
	t.Setenv("CORE_INTERNAL_API_KEY", "k")

	// Act
	runErr := Run()
	backfillErr := RunBackfill(BackfillOptions{Input: "x"})

	// Assert
	assert.Contains(t, runErr.Error(), "ACCESS_SECRET")
	assert.Contains(t, backfillErr.Error(), "ACCESS_SECRET")
}
