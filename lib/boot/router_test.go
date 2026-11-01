package boot

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/quickbite/analytics-service/internal/testkit"
	"github.com/quickbite/analytics-service/internal/testkit/assert"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func healthy() healthDeps {
	return healthDeps{
		pingMongo:       func(context.Context) error { return nil },
		rabbitConnected: func() bool { return true },
	}
}

func TestHealth(t *testing.T) {
	cases := []struct {
		name    string
		deps    healthDeps
		status  int
		message string
	}{
		{"reports ok when mongo and rabbit are up", healthy(), http.StatusOK, ""},
		{"reports 503 when mongo is down", healthDeps{
			pingMongo:       func(context.Context) error { return errors.New("no primary") },
			rabbitConnected: func() bool { return true },
		}, http.StatusServiceUnavailable, "mongo=down rabbit=up"},
		{"reports 503 when rabbit is down", healthDeps{
			pingMongo:       func(context.Context) error { return nil },
			rabbitConnected: func() bool { return false },
		}, http.StatusServiceUnavailable, "mongo=up rabbit=down"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			r := newRouter(discard, c.deps, func(chi.Router) {})

			// Act
			res := testkit.Get(t, r, "/health", nil)

			// Assert
			assert.Equal(t, res.Status, c.status)
			if c.status == http.StatusOK {
				assert.Equal(t, string(res.Data), `{"status":"ok","mongo":"up","rabbit":"up"}`)
			} else {
				assert.Equal(t, res.Error.Code, "UNHEALTHY")
				assert.Equal(t, res.Error.Message, c.message)
			}
		})
	}
}

func TestRouter(t *testing.T) {
	t.Run("answers unknown routes with the NOT_FOUND envelope", func(t *testing.T) {
		// Arrange
		r := newRouter(discard, healthy(), func(chi.Router) {})

		// Act
		res := testkit.Get(t, r, "/nope", nil)

		// Assert
		assert.Equal(t, res.Status, http.StatusNotFound)
		assert.Equal(t, res.Error.Code, "NOT_FOUND")
	})

	t.Run("answers a wrong method with the METHOD_NOT_ALLOWED envelope", func(t *testing.T) {
		// Arrange
		r := newRouter(discard, healthy(), func(api chi.Router) {
			api.Post("/only-post", func(http.ResponseWriter, *http.Request) {})
		})

		// Act
		res := testkit.Get(t, r, "/api/v1/only-post", nil)

		// Assert
		assert.Equal(t, res.Status, http.StatusMethodNotAllowed)
		assert.Equal(t, res.Error.Code, "METHOD_NOT_ALLOWED")
	})

	t.Run("mounts modules under /api/v1, tags responses with a correlation id and recovers panics", func(t *testing.T) {
		// Arrange
		r := newRouter(discard, healthy(), func(api chi.Router) {
			api.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("bug") })
		})

		// Act
		res := testkit.Get(t, r, "/api/v1/boom", nil)

		// Assert
		assert.Equal(t, res.Status, http.StatusInternalServerError)
		assert.Equal(t, res.Error.Code, "INTERNAL_ERROR")
		assert.True(t, res.Header.Get("X-CorrelationId") != "")
	})
}

func TestTxRunner_Off(t *testing.T) {
	// Arrange — "off" never talks to the server, so no client is needed

	// Act
	tx, err := txRunner(context.Background(), "off", nil, discard)

	// Assert
	assert.NoErr(t, err)
	assert.True(t, tx == nil)
}
