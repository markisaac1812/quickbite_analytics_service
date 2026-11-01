package auth_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/quickbite/analytics-service/internal/testkit"
	"github.com/quickbite/analytics-service/internal/testkit/assert"
	"github.com/quickbite/analytics-service/lib/appcontext"
	"github.com/quickbite/analytics-service/lib/auth"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

// whoAmI echoes the authenticated user id, proving the claims reached ctx.
func whoAmI() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := appcontext.ClaimsFrom(r.Context())
		if !ok {
			w.WriteHeader(http.StatusTeapot)
			return
		}
		_, _ = w.Write([]byte(c.Role))
	})
}

func serve(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAuthenticate(t *testing.T) {
	mw := auth.NewMiddleware(auth.NewVerifier(testkit.AccessSecret), discard).Authenticate(whoAmI())

	t.Run("accepts the access_token cookie and stores the claims in ctx", func(t *testing.T) {
		// Arrange
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(testkit.AuthCookie(testkit.Admin))

		// Act
		rec := serve(mw, req)

		// Assert
		assert.Equal(t, rec.Code, http.StatusOK)
		assert.Equal(t, rec.Body.String(), "system_admin")
	})

	t.Run("accepts a Bearer token (case-insensitive scheme)", func(t *testing.T) {
		// Arrange
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "bearer "+testkit.SignToken(testkit.Owner, time.Hour))

		// Act
		rec := serve(mw, req)

		// Assert
		assert.Equal(t, rec.Code, http.StatusOK)
		assert.Equal(t, rec.Body.String(), "restaurant_user")
	})

	t.Run("prefers the cookie over the Authorization header", func(t *testing.T) {
		// Arrange
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(testkit.AuthCookie(testkit.Admin))
		req.Header.Set("Authorization", "Bearer garbage")

		// Act
		rec := serve(mw, req)

		// Assert
		assert.Equal(t, rec.Body.String(), "system_admin")
	})

	t.Run("answers 401 without a token", func(t *testing.T) {
		// Arrange
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")

		// Act
		rec := serve(mw, req)

		// Assert
		assert.Equal(t, rec.Code, http.StatusUnauthorized)
		assert.Contains(t, rec.Body.String(), `"code":"UNAUTHENTICATED"`)
	})

	t.Run("answers 401 for an invalid token", func(t *testing.T) {
		// Arrange
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+testkit.SignToken(testkit.Owner, -time.Minute))

		// Act
		rec := serve(mw, req)

		// Assert
		assert.Equal(t, rec.Code, http.StatusUnauthorized)
		assert.Contains(t, rec.Body.String(), "Invalid or expired token")
	})
}

func TestRequireInternalAPIKey(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	t.Run("lets a request with the right key through", func(t *testing.T) {
		// Arrange
		h := auth.RequireInternalAPIKey("k3y", discard)(ok)
		req := httptest.NewRequest(http.MethodGet, "/internal/x", nil)
		req.Header.Set(auth.APIKeyHeader, "k3y")

		// Act
		rec := serve(h, req)

		// Assert
		assert.Equal(t, rec.Code, http.StatusNoContent)
	})

	t.Run("answers 401 for a wrong or missing key", func(t *testing.T) {
		// Arrange
		h := auth.RequireInternalAPIKey("k3y", discard)(ok)
		req := httptest.NewRequest(http.MethodGet, "/internal/x", nil)
		req.Header.Set(auth.APIKeyHeader, "nope")

		// Act
		rec := serve(h, req)

		// Assert
		assert.Equal(t, rec.Code, http.StatusUnauthorized)
		assert.Contains(t, rec.Body.String(), "Invalid api key")
	})

	t.Run("answers 500 when the key is not configured", func(t *testing.T) {
		// Arrange
		h := auth.RequireInternalAPIKey("", discard)(ok)
		req := httptest.NewRequest(http.MethodGet, "/internal/x", nil)

		// Act
		rec := serve(h, req)

		// Assert
		assert.Equal(t, rec.Code, http.StatusInternalServerError)
		assert.Contains(t, rec.Body.String(), `"code":"INTERNAL_ERROR"`)
	})
}
