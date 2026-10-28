package apperr

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	httpx "github.com/quickbite/analytics-service/lib/http"
	"github.com/quickbite/analytics-service/lib/logger"
)

// HandlerFunc is an http handler that returns an error instead of writing it.
// Controllers implement this signature; Wrap adapts it to net/http.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// Wrap is the Go analogue of the Express errorHandler middleware: the
// controller returns an error, Wrap logs it and renders the error envelope.
//
//	r.Get("/x", apperr.Wrap(log, ctrl.GetX))
func Wrap(log *slog.Logger, h HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			Write(w, r, log, err)
		}
	}
}

// Write logs err and renders the envelope. Used by Wrap and by middleware
// (auth, rbac) that must short-circuit with an error.
//
//   - *AppError       → its Status/Code/Message (5xx logged at error, 4xx at warn)
//   - anything else   → 500 INTERNAL_ERROR, generic message, full error logged
func Write(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	l := requestLogger(r, log).With("method", r.Method, "path", r.URL.Path)

	var appErr *AppError
	if !errors.As(err, &appErr) {
		l.Error("unhandled error", "error", err.Error())
		_ = httpx.SendError(w, ErrInternal.Status, ErrInternal.Code, ErrInternal.Message)
		return
	}

	attrs := []any{"code", appErr.Code, "status", appErr.Status}
	if appErr.Err != nil {
		attrs = append(attrs, "cause", appErr.Err.Error())
	}
	if appErr.Status >= 500 {
		l.Error(appErr.Message, attrs...)
	} else {
		l.Warn(appErr.Message, attrs...)
	}
	_ = httpx.SendError(w, appErr.Status, appErr.Code, appErr.Message)
}

// Recoverer turns a panic in any handler into a logged 500 envelope instead
// of a dropped connection.
func Recoverer(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					if rec == http.ErrAbortHandler {
						panic(rec) // net/http's own sentinel — let it through
					}
					requestLogger(r, log).Error("panic", "panic", fmt.Sprint(rec), "stack", string(debug.Stack()))
					_ = httpx.SendError(w, ErrInternal.Status, ErrInternal.Code, ErrInternal.Message)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// NotFoundHandler / MethodNotAllowedHandler keep chi's fallbacks on-envelope.
func NotFoundHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		_ = httpx.SendError(w, ErrNotFound.Status, ErrNotFound.Code, ErrNotFound.Message)
	}
}

func MethodNotAllowedHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		_ = httpx.SendError(w, ErrMethodNotAllowed.Status, ErrMethodNotAllowed.Code, ErrMethodNotAllowed.Message)
	}
}

// requestLogger prefers the correlation-tagged logger the middleware put in
// ctx; falls back to the logger passed at wiring time.
func requestLogger(r *http.Request, fallback *slog.Logger) *slog.Logger {
	if l := logger.FromContext(r.Context()); l != slog.Default() {
		return l
	}
	return fallback
}
