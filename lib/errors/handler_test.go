package apperr_test

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/quickbite/analytics-service/internal/testkit/assert"
	apperr "github.com/quickbite/analytics-service/lib/errors"
	"github.com/quickbite/analytics-service/lib/logger"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func do(h http.Handler) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	return rec
}

func TestWrap(t *testing.T) {
	t.Run("leaves a successful handler's response alone", func(t *testing.T) {
		// Arrange
		h := apperr.Wrap(discard, func(w http.ResponseWriter, _ *http.Request) error {
			w.WriteHeader(http.StatusAccepted)
			return nil
		})

		// Act
		rec := do(h)

		// Assert
		assert.Equal(t, rec.Code, http.StatusAccepted)
	})

	t.Run("renders an AppError with its status, code and message", func(t *testing.T) {
		// Arrange
		h := apperr.Wrap(discard, func(http.ResponseWriter, *http.Request) error {
			return apperr.ErrForbidden.WithCause(errors.New("not your restaurant"))
		})

		// Act
		rec := do(h)

		// Assert
		assert.Equal(t, rec.Code, http.StatusForbidden)
		assert.Equal(t, rec.Body.String(), `{"success":false,"error":{"code":"FORBIDDEN","message":"You are not allowed to perform this action"}}`+"\n")
	})

	t.Run("logs a 5xx AppError at error level", func(t *testing.T) {
		// Arrange
		var buf bytes.Buffer
		log := slog.New(slog.NewJSONHandler(&buf, nil))
		h := apperr.Wrap(log, func(http.ResponseWriter, *http.Request) error { return apperr.ErrUpstreamUnavailable })

		// Act
		rec := do(h)

		// Assert
		assert.Equal(t, rec.Code, http.StatusServiceUnavailable)
		assert.Contains(t, buf.String(), `"level":"ERROR"`)
	})

	t.Run("hides a non-AppError behind a generic 500 but logs it", func(t *testing.T) {
		// Arrange
		var buf bytes.Buffer
		log := slog.New(slog.NewJSONHandler(&buf, nil))
		h := apperr.Wrap(log, func(http.ResponseWriter, *http.Request) error { return errors.New("sql: secret detail") })

		// Act
		rec := do(h)

		// Assert
		assert.Equal(t, rec.Code, http.StatusInternalServerError)
		assert.Contains(t, rec.Body.String(), `"message":"Something went wrong"`)
		assert.False(t, bytes.Contains(rec.Body.Bytes(), []byte("secret detail")))
		assert.Contains(t, buf.String(), "secret detail")
	})

	t.Run("logs through the request-scoped logger when one is in ctx", func(t *testing.T) {
		// Arrange
		var buf bytes.Buffer
		reqLog := slog.New(slog.NewJSONHandler(&buf, nil)).With("correlation_id", "c-1")
		h := apperr.Wrap(discard, func(http.ResponseWriter, *http.Request) error { return apperr.ErrNotFound })
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req = req.WithContext(logger.WithContext(req.Context(), reqLog))

		// Act
		h.ServeHTTP(httptest.NewRecorder(), req)

		// Assert
		assert.Contains(t, buf.String(), `"correlation_id":"c-1"`)
	})
}

func TestRecoverer(t *testing.T) {
	t.Run("turns a panic into a 500 envelope", func(t *testing.T) {
		// Arrange
		h := apperr.Recoverer(discard)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("nil map") }))

		// Act
		rec := do(h)

		// Assert
		assert.Equal(t, rec.Code, http.StatusInternalServerError)
		assert.Contains(t, rec.Body.String(), `"code":"INTERNAL_ERROR"`)
	})

	t.Run("re-panics net/http's ErrAbortHandler", func(t *testing.T) {
		// Arrange
		h := apperr.Recoverer(discard)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }))
		var recovered any

		// Act
		func() {
			defer func() { recovered = recover() }()
			do(h)
		}()

		// Assert
		assert.Equal(t, recovered, any(http.ErrAbortHandler))
	})
}

func TestFallbackHandlers(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		// Arrange
		h := apperr.NotFoundHandler()

		// Act
		rec := do(h)

		// Assert
		assert.Equal(t, rec.Code, http.StatusNotFound)
		assert.Contains(t, rec.Body.String(), `"code":"NOT_FOUND"`)
	})

	t.Run("method not allowed", func(t *testing.T) {
		// Arrange
		h := apperr.MethodNotAllowedHandler()

		// Act
		rec := do(h)

		// Assert
		assert.Equal(t, rec.Code, http.StatusMethodNotAllowed)
		assert.Contains(t, rec.Body.String(), `"code":"METHOD_NOT_ALLOWED"`)
	})
}
