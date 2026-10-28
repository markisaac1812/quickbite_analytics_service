// Package validation runs go-playground/validator over request DTOs and turns
// failures into a 400 VALIDATION_ERROR. Go analogue of lib/validation/validate.ts
// (validateBody with class-validator): struct tags replace decorators.
package validation

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"

	apperr "github.com/quickbite/analytics-service/lib/errors"
)

// One validator per process: it caches struct metadata and is concurrency-safe.
var v = newValidator()

func newValidator() *validator.Validate {
	val := validator.New(validator.WithRequiredStructEnabled())
	// Report fields by their JSON name ("restaurantId"), not the Go name.
	val.RegisterTagNameFunc(func(f reflect.StructField) string {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			return f.Name
		}
		return name
	})
	return val
}

// Struct validates s. On failure it returns ErrValidation with a message
// listing every failing field, e.g. "from is required; to must be a date (YYYY-MM-DD)".
func Struct(s any) error {
	err := v.Struct(s)
	if err == nil {
		return nil
	}
	var fieldErrs validator.ValidationErrors
	if !errors.As(err, &fieldErrs) {
		return apperr.ErrValidation.WithCause(err)
	}
	msgs := make([]string, 0, len(fieldErrs))
	for _, fe := range fieldErrs {
		msgs = append(msgs, describe(fe))
	}
	return apperr.ErrValidation.WithMessage(strings.Join(msgs, "; "))
}

func describe(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return fe.Field() + " is required"
	case "gt":
		if fe.Param() == "0" {
			return fe.Field() + " must be a positive integer"
		}
		return fmt.Sprintf("%s must be greater than %s", fe.Field(), fe.Param())
	case "gte":
		return fmt.Sprintf("%s must be >= %s", fe.Field(), fe.Param())
	case "lte":
		return fmt.Sprintf("%s must be <= %s", fe.Field(), fe.Param())
	case "len":
		return fmt.Sprintf("%s must be exactly %s characters", fe.Field(), fe.Param())
	case "uppercase":
		return fe.Field() + " must be uppercase (ISO 4217, e.g. EGP)"
	case "datetime":
		if fe.Param() == "2006-01-02" {
			return fe.Field() + " must be a date (YYYY-MM-DD)"
		}
		return fmt.Sprintf("%s must match layout %s", fe.Field(), fe.Param())
	default:
		return fmt.Sprintf("%s failed %q", fe.Field(), fe.Tag())
	}
}
