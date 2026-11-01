package apperr_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/quickbite/analytics-service/internal/testkit/assert"
	apperr "github.com/quickbite/analytics-service/lib/errors"
)

func TestAppError(t *testing.T) {
	sentinel := apperr.New("THING_BROKEN", http.StatusConflict, "thing is broken")

	t.Run("renders code and message, plus the cause when present", func(t *testing.T) {
		// Arrange
		withCause := sentinel.WithCause(errors.New("disk full"))

		// Act
		plain, caused := sentinel.Error(), withCause.Error()

		// Assert
		assert.Equal(t, plain, "THING_BROKEN: thing is broken")
		assert.Equal(t, caused, "THING_BROKEN: thing is broken: disk full")
	})

	t.Run("copies match their sentinel by code, through wrapping", func(t *testing.T) {
		// Arrange
		cp := sentinel.WithMessage("thing 42 is broken")
		wrapped := fmt.Errorf("service: %w", cp)

		// Act
		matches := errors.Is(wrapped, sentinel)
		other := errors.Is(cp, apperr.ErrNotFound)

		// Assert
		assert.True(t, matches)
		assert.False(t, other)
		assert.False(t, cp.Is(errors.New("THING_BROKEN")), "non-AppError targets never match")
	})

	t.Run("never mutates the shared sentinel", func(t *testing.T) {
		// Arrange
		cause := errors.New("root")

		// Act
		cp := sentinel.WithMessage("changed").WithCause(cause)

		// Assert
		assert.Equal(t, sentinel.Message, "thing is broken")
		assert.True(t, sentinel.Err == nil)
		assert.Equal(t, cp.Status, http.StatusConflict)
		assert.ErrIs(t, cp, cause, "Unwrap exposes the cause")
	})
}
