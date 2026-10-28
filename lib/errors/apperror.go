// Package apperr is the service's error model. Go analogue of
// lib/error/AppError.ts — but with a stable machine-readable Code on every
// error, which the envelope renders as error.code.
//
// The folder is lib/errors; the package is named apperr so it never shadows
// the stdlib "errors" package at call sites.
package apperr

import "net/http"

// AppError is an operational (expected, user-facing) error.
// Services return these; Wrap renders them. Anything that is NOT an AppError
// is treated as a bug: logged with detail, rendered as a generic 500.
type AppError struct {
	Code    string // stable, UPPER_SNAKE, part of the API contract
	Status  int    // HTTP status
	Message string // human-readable, safe to show the client
	Err     error  // optional cause, logged but never rendered
}

// New declares an error. Use it at package level:
//
//	var ErrInvalidDateRange = apperr.New("ANALYTICS_INVALID_DATE_RANGE", 400, "...")
func New(code string, status int, message string) *AppError {
	return &AppError{Code: code, Status: status, Message: message}
}

func (e *AppError) Error() string {
	if e.Err != nil {
		return e.Code + ": " + e.Message + ": " + e.Err.Error()
	}
	return e.Code + ": " + e.Message
}

func (e *AppError) Unwrap() error { return e.Err }

// Is makes errors.Is match on Code, so a copy made by WithMessage/WithCause
// still matches its package-level sentinel.
func (e *AppError) Is(target error) bool {
	t, ok := target.(*AppError)
	return ok && t.Code == e.Code
}

// WithMessage returns a copy with a more specific message (same code/status).
// Never mutate a package-level sentinel — it is shared by every request.
func (e *AppError) WithMessage(msg string) *AppError {
	cp := *e
	cp.Message = msg
	return &cp
}

// WithCause returns a copy that wraps an underlying error for logging.
func (e *AppError) WithCause(err error) *AppError {
	cp := *e
	cp.Err = err
	return &cp
}

// Cross-cutting errors shared by every module. Module-specific errors live in
// app/<module>/errors.go.
var (
	ErrValidation          = New("VALIDATION_ERROR", http.StatusBadRequest, "Request validation failed")
	ErrUnauthenticated     = New("UNAUTHENTICATED", http.StatusUnauthorized, "Authentication required")
	ErrForbidden           = New("FORBIDDEN", http.StatusForbidden, "You are not allowed to perform this action")
	ErrNotFound            = New("NOT_FOUND", http.StatusNotFound, "Resource not found")
	ErrMethodNotAllowed    = New("METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed, "Method not allowed")
	ErrUpstreamUnavailable = New("UPSTREAM_UNAVAILABLE", http.StatusServiceUnavailable, "A dependency is unavailable, try again later")
	ErrUnhealthy           = New("UNHEALTHY", http.StatusServiceUnavailable, "Service is unhealthy")
	ErrInternal            = New("INTERNAL_ERROR", http.StatusInternalServerError, "Something went wrong")
)
