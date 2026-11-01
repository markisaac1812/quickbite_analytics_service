package validation_test

import (
	"testing"

	"github.com/quickbite/analytics-service/internal/testkit/assert"
	apperr "github.com/quickbite/analytics-service/lib/errors"
	"github.com/quickbite/analytics-service/lib/validation"
)

// sample exercises every tag describe() has a message for.
type sample struct {
	ID       int64  `json:"id" validate:"gt=0"`
	Min      int    `json:"min" validate:"gt=5"`
	Limit    int    `json:"limit" validate:"gte=0,lte=100"`
	Currency string `json:"currency" validate:"required,len=3,uppercase"`
	Date     string `json:"date" validate:"required,datetime=2006-01-02"`
	Clock    string `json:"clock" validate:"omitempty,datetime=15:04"`
	Kind     string `json:"kind" validate:"omitempty,oneof=a b"`
	NoTag    string `validate:"omitempty,email"`
	Skipped  string `json:"-" validate:"omitempty,email"`
}

func valid() sample {
	return sample{ID: 1, Min: 6, Limit: 10, Currency: "EGP", Date: "2026-10-01"}
}

func TestStruct(t *testing.T) {
	t.Run("accepts a valid struct", func(t *testing.T) {
		// Arrange
		s := valid()

		// Act
		err := validation.Struct(s)

		// Assert
		assert.NoErr(t, err)
	})

	cases := []struct {
		name    string
		mutate  func(s *sample)
		message string
	}{
		{"required", func(s *sample) { s.Date = "" }, "date is required"},
		{"gt=0", func(s *sample) { s.ID = 0 }, "id must be a positive integer"},
		{"gt=N", func(s *sample) { s.Min = 5 }, "min must be greater than 5"},
		{"gte", func(s *sample) { s.Limit = -1 }, "limit must be >= 0"},
		{"lte", func(s *sample) { s.Limit = 101 }, "limit must be <= 100"},
		{"len", func(s *sample) { s.Currency = "EG" }, "currency must be exactly 3 characters"},
		{"uppercase", func(s *sample) { s.Currency = "egp" }, "currency must be uppercase (ISO 4217, e.g. EGP)"},
		{"date layout", func(s *sample) { s.Date = "01/10/2026" }, "date must be a date (YYYY-MM-DD)"},
		{"other layout", func(s *sample) { s.Clock = "noon" }, "clock must match layout 15:04"},
		{"unknown tag", func(s *sample) { s.Kind = "c" }, `kind failed "oneof"`},
		{"Go field name without a json tag", func(s *sample) { s.NoTag = "x" }, `NoTag failed "email"`},
		{"Go field name for json:\"-\"", func(s *sample) { s.Skipped = "x" }, `Skipped failed "email"`},
	}
	for _, c := range cases {
		t.Run("describes "+c.name, func(t *testing.T) {
			// Arrange
			s := valid()
			c.mutate(&s)

			// Act
			err := validation.Struct(s)

			// Assert
			assert.ErrIs(t, err, apperr.ErrValidation)
			assert.Equal(t, err.(*apperr.AppError).Message, c.message)
		})
	}

	t.Run("joins every failing field", func(t *testing.T) {
		// Arrange
		s := sample{Min: 6}

		// Act
		err := validation.Struct(s)

		// Assert
		assert.Equal(t, err.(*apperr.AppError).Message, "id must be a positive integer; currency is required; date is required")
	})

	t.Run("reports a non-struct as a validation error with the cause", func(t *testing.T) {
		// Arrange
		notAStruct := 42

		// Act
		err := validation.Struct(notAStruct)

		// Assert
		assert.ErrIs(t, err, apperr.ErrValidation)
		assert.True(t, err.(*apperr.AppError).Err != nil)
	})
}
