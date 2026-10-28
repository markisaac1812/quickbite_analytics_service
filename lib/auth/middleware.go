package auth

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/quickbite/analytics-service/lib/appcontext"
	apperr "github.com/quickbite/analytics-service/lib/errors"
)

// AccessTokenCookie is the cookie core-service sets on login.
const AccessTokenCookie = "access_token"

// Middleware authenticates requests. Go analogue of lib/auth/guard.ts.
type Middleware struct {
	verifier *Verifier
	log      *slog.Logger
}

func NewMiddleware(v *Verifier, log *slog.Logger) *Middleware {
	return &Middleware{verifier: v, log: log}
}

// Authenticate reads the token from the `access_token` cookie first, then
// `Authorization: Bearer <token>`. On success the claims go into ctx
// (appcontext.ClaimsFrom); on failure it renders 401 UNAUTHENTICATED.
func (m *Middleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := tokenFrom(r)
		if token == "" {
			apperr.Write(w, r, m.log, apperr.ErrUnauthenticated)
			return
		}
		claims, err := m.verifier.Verify(token)
		if err != nil {
			apperr.Write(w, r, m.log, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(appcontext.WithClaims(r.Context(), claims)))
	})
}

func tokenFrom(r *http.Request) string {
	if c, err := r.Cookie(AccessTokenCookie); err == nil && c.Value != "" {
		return c.Value
	}
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}
