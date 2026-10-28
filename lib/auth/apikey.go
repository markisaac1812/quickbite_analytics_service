package auth

import (
	"crypto/subtle"
	"log/slog"
	"net/http"

	apperr "github.com/quickbite/analytics-service/lib/errors"
)

// APIKeyHeader is the shared-secret header for service-to-service calls.
const APIKeyHeader = "api-key"

// RequireInternalAPIKey guards /internal routes with the shared `api-key`
// header. Go analogue of lib/auth/api-key.ts. Constant-time compare so the
// key can't be recovered byte-by-byte from response timings.
//
// Not mounted in the video slice (analytics exposes no internal routes yet);
// it is here so the first internal endpoint doesn't invent its own guard.
func RequireInternalAPIKey(expected string, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if expected == "" {
				apperr.Write(w, r, log, apperr.ErrInternal.WithMessage("Internal api key not configured"))
				return
			}
			got := r.Header.Get(APIKeyHeader)
			if subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
				apperr.Write(w, r, log, apperr.ErrUnauthenticated.WithMessage("Invalid api key"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
