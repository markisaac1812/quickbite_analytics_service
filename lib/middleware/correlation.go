// Package middleware holds cross-cutting HTTP middleware.
package middleware

import (
	"log/slog"
	"net/http"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/quickbite/analytics-service/lib/appcontext"
	"github.com/quickbite/analytics-service/lib/logger"
)

// CorrelationHeader is the header name used across QuickBite services.
const CorrelationHeader = "X-CorrelationId"

// Correlation reuses an incoming X-CorrelationId (or X-Correlation-Id),
// otherwise mints a UUID; echoes it on the response; stores it in ctx along
// with a child logger tagged correlation_id. Mirrors order-service's
// lib/correlation/correlationId.ts.
func Correlation(base *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(CorrelationHeader)
			if id == "" {
				id = r.Header.Get("X-Correlation-Id")
			}
			if id == "" || len(id) > 128 {
				id = uuid.NewString()
			}
			w.Header().Set(CorrelationHeader, id)

			ctx := appcontext.WithCorrelationID(r.Context(), id)
			ctx = logger.WithContext(ctx, base.With("correlation_id", id))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// AccessLog writes one structured line per request after it completes.
// Must run after Correlation so the line carries correlation_id.
func AccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)

		status := ww.Status()
		if status == 0 {
			status = http.StatusOK
		}
		logger.FromContext(r.Context()).Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"bytes", ww.BytesWritten(),
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}
