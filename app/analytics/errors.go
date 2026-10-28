package analytics

import (
	"net/http"

	apperr "github.com/quickbite/analytics-service/lib/errors"
)

// Module-level errors: stable code, stable status, stable wording.
// Services return these by name — no inline apperr.New at call sites.
var (
	ErrInvalidDateRange = apperr.New(
		"ANALYTICS_INVALID_DATE_RANGE", http.StatusBadRequest,
		"from must be on or before to",
	)
	ErrDateRangeTooLarge = apperr.New(
		"ANALYTICS_DATE_RANGE_TOO_LARGE", http.StatusBadRequest,
		"date range must not exceed 366 days",
	)
	// Event-side: a payload we can't apply. Dead-lettered, never retried.
	ErrInvalidEventPayload = apperr.New(
		"ANALYTICS_INVALID_EVENT_PAYLOAD", http.StatusUnprocessableEntity,
		"event payload is invalid",
	)
)
