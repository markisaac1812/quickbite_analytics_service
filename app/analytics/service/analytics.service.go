// Package service holds the analytics business logic. Types, errors and enums
// it uses live in the parent `analytics` package.
//
// Files: analytics.service.go (struct + shared helpers), ingest.service.go
// (event → aggregates), reports.service.go (aggregates → read models).
package service

import (
	"context"
	"math"
	"time"

	"github.com/quickbite/analytics-service/app/analytics"
)

// Service is constructed once in lib/boot and shared by the controller
// (reads), the event handlers (writes) and the backfill command (writes).
type Service struct {
	st  analytics.Stores
	now func() time.Time
}

func New(st analytics.Stores) *Service {
	return &Service{st: st, now: time.Now}
}

// incDay applies the same delta to the restaurant, branch and platform rows
// of one UTC day. Inside a transaction (tx mode) all three commit together.
func (s *Service) incDay(ctx context.Context, restaurantID, branchID int64, date, currency string, d analytics.DayDelta) error {
	at := s.now().UTC()
	if err := s.st.Restaurants.Increment(ctx, restaurantID, date, currency, d, at); err != nil {
		return err
	}
	if branchID > 0 {
		if err := s.st.Branches.Increment(ctx, branchID, restaurantID, date, currency, d, at); err != nil {
			return err
		}
	}
	return s.st.Platform.Increment(ctx, date, currency, d, at)
}

// checkRange enforces from <= to and the MaxRangeDays cap; returns the
// inclusive "YYYY-MM-DD" bounds.
func checkRange(from, to time.Time) (string, string, error) {
	if from.After(to) {
		return "", "", analytics.ErrInvalidDateRange
	}
	if days := int(to.Sub(from).Hours()/24) + 1; days > analytics.MaxRangeDays {
		return "", "", analytics.ErrDateRangeTooLarge
	}
	return dateKey(from), dateKey(to), nil
}

func dateKey(t time.Time) string { return t.UTC().Format(analytics.DateLayout) }

// avg is integer division in minor units / ms (truncates); 0 when count is 0.
// Derived on read so stored rows stay associative (sum + count).
func avg(sum, count int64) int64 {
	if count == 0 {
		return 0
	}
	return sum / count
}

// rate is n/d rounded to 4 decimals; 0 when d is 0.
func rate(n, d int64) float64 {
	if d == 0 {
		return 0
	}
	return math.Round(float64(n)/float64(d)*10000) / 10000
}

func limitOrDefault(n int) int {
	if n <= 0 {
		return analytics.DefaultTopLimit
	}
	return min(n, analytics.MaxTopLimit)
}
