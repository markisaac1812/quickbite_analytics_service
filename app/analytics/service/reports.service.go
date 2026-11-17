package service

import (
	"context"

	"github.com/quickbite/analytics-service/app/analytics"
)

// Every report is sparse: a day appears only if the reported metric has data
// that day. Every slice is non-nil, so empty results render as [].

// ListRestaurantDays: one row per day with orders, oldest first.
func (s *Service) ListRestaurantDays(ctx context.Context, in analytics.ListRestaurantDaysInput) ([]analytics.RestaurantDayRow, error) {
	from, to, err := checkRange(in.From, in.To)
	if err != nil {
		return nil, err
	}
	docs, err := s.st.Restaurants.FindRange(ctx, in.RestaurantID, from, to)
	if err != nil {
		return nil, err
	}
	rows := make([]analytics.RestaurantDayRow, 0, len(docs))
	for _, d := range docs {
		if d.OrdersCount == 0 {
			continue
		}
		rows = append(rows, analytics.RestaurantDayRow{
			Date:          d.Date,
			OrdersCount:   d.OrdersCount,
			RevenueMinor:  d.RevenueSum,
			Currency:      d.Currency,
			AvgOrderMinor: avg(d.RevenueSum, d.OrdersCount),
		})
	}
	return rows, nil
}

// ListBranchDays: same shape as ListRestaurantDays, for one branch.
func (s *Service) ListBranchDays(ctx context.Context, in analytics.ListBranchDaysInput) ([]analytics.RestaurantDayRow, error) {
	from, to, err := checkRange(in.From, in.To)
	if err != nil {
		return nil, err
	}
	docs, err := s.st.Branches.FindRange(ctx, in.RestaurantID, in.BranchID, from, to)
	if err != nil {
		return nil, err
	}
	rows := make([]analytics.RestaurantDayRow, 0, len(docs))
	for _, d := range docs {
		if d.OrdersCount == 0 {
			continue
		}
		rows = append(rows, analytics.RestaurantDayRow{
			Date:          d.Date,
			OrdersCount:   d.OrdersCount,
			RevenueMinor:  d.RevenueSum,
			Currency:      d.Currency,
			AvgOrderMinor: avg(d.RevenueSum, d.OrdersCount),
		})
	}
	return rows, nil
}

// RestaurantFailures derives rejection/cancellation rates from orders_count,
// rejected_count and cancelled_count — no extra collection.
func (s *Service) RestaurantFailures(ctx context.Context, in analytics.ListRestaurantDaysInput) (analytics.FailureReport, error) {
	from, to, err := checkRange(in.From, in.To)
	if err != nil {
		return analytics.FailureReport{}, err
	}
	docs, err := s.st.Restaurants.FindRange(ctx, in.RestaurantID, from, to)
	if err != nil {
		return analytics.FailureReport{}, err
	}
	days := make([]failureCounts, 0, len(docs))
	for _, d := range docs {
		days = append(days, failureCounts{date: d.Date, orders: d.OrdersCount, rejected: d.RejectedCount, cancelled: d.CancelledCount})
	}
	return buildFailureReport(days), nil
}

// PlatformFailures is RestaurantFailures for the whole platform. agg_platform_day
// has one row per (date, currency); counts are currency-free, so the rows of a
// day are summed into one.
func (s *Service) PlatformFailures(ctx context.Context, in analytics.RangeInput) (analytics.FailureReport, error) {
	from, to, err := checkRange(in.From, in.To)
	if err != nil {
		return analytics.FailureReport{}, err
	}
	docs, err := s.st.Platform.FindRange(ctx, from, to)
	if err != nil {
		return analytics.FailureReport{}, err
	}
	days := make([]failureCounts, 0, len(docs))
	for _, d := range docs { // sorted by date, then currency
		if n := len(days); n > 0 && days[n-1].date == d.Date {
			days[n-1].orders += d.OrdersCount
			days[n-1].rejected += d.RejectedCount
			days[n-1].cancelled += d.CancelledCount
			continue
		}
		days = append(days, failureCounts{date: d.Date, orders: d.OrdersCount, rejected: d.RejectedCount, cancelled: d.CancelledCount})
	}
	return buildFailureReport(days), nil
}

type failureCounts struct {
	date                        string
	orders, rejected, cancelled int64
}

// buildFailureReport: range rates come from range totals, not from averaging
// daily rates. Days with no orders and no failures are skipped (sparse).
func buildFailureReport(days []failureCounts) analytics.FailureReport {
	rep := analytics.FailureReport{Days: make([]analytics.FailureDayRow, 0, len(days))}
	for _, d := range days {
		if d.orders == 0 && d.rejected == 0 && d.cancelled == 0 {
			continue
		}
		rep.OrdersCount += d.orders
		rep.RejectedCount += d.rejected
		rep.CancelledCount += d.cancelled
		rep.Days = append(rep.Days, analytics.FailureDayRow{
			Date:             d.date,
			OrdersCount:      d.orders,
			RejectedCount:    d.rejected,
			CancelledCount:   d.cancelled,
			FailedCount:      d.rejected + d.cancelled,
			FailureRate:      rate(d.rejected, d.orders),
			CancellationRate: rate(d.cancelled, d.orders),
		})
	}
	rep.FailedCount = rep.RejectedCount + rep.CancelledCount
	rep.FailureRate = rate(rep.RejectedCount, rep.OrdersCount)
	rep.CancellationRate = rate(rep.CancelledCount, rep.OrdersCount)
	return rep
}

// RestaurantDeliveryAvg derives average delivery time from
// delivery_ms_sum / delivery_ms_count — no extra collection. The range
// average is sum/count over the range, NOT the average of daily averages.
func (s *Service) RestaurantDeliveryAvg(ctx context.Context, in analytics.ListRestaurantDaysInput) (analytics.DeliveryReport, error) {
	from, to, err := checkRange(in.From, in.To)
	if err != nil {
		return analytics.DeliveryReport{}, err
	}
	docs, err := s.st.Restaurants.FindRange(ctx, in.RestaurantID, from, to)
	if err != nil {
		return analytics.DeliveryReport{}, err
	}
	rep := analytics.DeliveryReport{Days: make([]analytics.DeliveryDayRow, 0, len(docs))}
	var msSum int64
	for _, d := range docs {
		if d.DeliveryMsCount == 0 {
			continue
		}
		rep.DeliveredCount += d.DeliveryMsCount
		msSum += d.DeliveryMsSum
		rep.Days = append(rep.Days, analytics.DeliveryDayRow{
			Date:           d.Date,
			DeliveredCount: d.DeliveryMsCount,
			AvgDeliveryMs:  avg(d.DeliveryMsSum, d.DeliveryMsCount),
		})
	}
	rep.AvgDeliveryMs = avg(msSum, rep.DeliveredCount)
	return rep, nil
}

// RestaurantPayments: captured online payments per capture day.
func (s *Service) RestaurantPayments(ctx context.Context, in analytics.ListRestaurantDaysInput) (analytics.PaymentsReport, error) {
	from, to, err := checkRange(in.From, in.To)
	if err != nil {
		return analytics.PaymentsReport{}, err
	}
	docs, err := s.st.Restaurants.FindRange(ctx, in.RestaurantID, from, to)
	if err != nil {
		return analytics.PaymentsReport{}, err
	}
	rep := analytics.PaymentsReport{Days: make([]analytics.PaymentDayRow, 0, len(docs))}
	for _, d := range docs {
		if d.PaymentsCount == 0 {
			continue
		}
		rep.PaymentsCount += d.PaymentsCount
		rep.PaymentsMinor += d.PaymentsSum
		rep.Currency = d.Currency
		rep.Days = append(rep.Days, analytics.PaymentDayRow{
			Date:          d.Date,
			PaymentsCount: d.PaymentsCount,
			PaymentsMinor: d.PaymentsSum,
			Currency:      d.Currency,
		})
	}
	return rep, nil
}

// TopProducts ranks a restaurant's products by revenue over the range.
func (s *Service) TopProducts(ctx context.Context, in analytics.TopProductsInput) ([]analytics.ProductRankRow, error) {
	from, to, err := checkRange(in.From, in.To)
	if err != nil {
		return nil, err
	}
	totals, err := s.st.Products.TopProducts(ctx, in.RestaurantID, from, to, limitOrDefault(in.Limit))
	if err != nil {
		return nil, err
	}
	rows := make([]analytics.ProductRankRow, 0, len(totals))
	for _, t := range totals {
		rows = append(rows, analytics.ProductRankRow{
			ProductID:    t.ProductID,
			UnitsSold:    t.UnitsSold,
			RevenueMinor: t.RevenueSum,
			OrdersCount:  t.OrdersCount,
			Currency:     t.Currency,
		})
	}
	return rows, nil
}

// ProductDays: per-day rollup rows of a restaurant's products (optionally one
// product), oldest first. TopProducts is the same data summed over the range.
func (s *Service) ProductDays(ctx context.Context, in analytics.ProductDaysInput) ([]analytics.ProductDayRow, error) {
	from, to, err := checkRange(in.From, in.To)
	if err != nil {
		return nil, err
	}
	docs, err := s.st.Products.FindRange(ctx, in.RestaurantID, in.ProductID, from, to)
	if err != nil {
		return nil, err
	}
	rows := make([]analytics.ProductDayRow, 0, len(docs))
	for _, d := range docs {
		rows = append(rows, analytics.ProductDayRow{
			Date:         d.Date,
			ProductID:    d.ProductID,
			UnitsSold:    d.UnitsSold,
			RevenueMinor: d.RevenueSum,
			OrdersCount:  d.OrdersCount,
			Currency:     d.Currency,
		})
	}
	return rows, nil
}

// PlatformDays: platform-wide totals per (day, currency), all derived values included.
func (s *Service) PlatformDays(ctx context.Context, in analytics.RangeInput) ([]analytics.PlatformDayRow, error) {
	from, to, err := checkRange(in.From, in.To)
	if err != nil {
		return nil, err
	}
	docs, err := s.st.Platform.FindRange(ctx, from, to)
	if err != nil {
		return nil, err
	}
	rows := make([]analytics.PlatformDayRow, 0, len(docs))
	for _, d := range docs {
		rows = append(rows, analytics.PlatformDayRow{
			Date:           d.Date,
			Currency:       d.Currency,
			OrdersCount:    d.OrdersCount,
			RevenueMinor:   d.RevenueSum,
			AvgOrderMinor:  avg(d.RevenueSum, d.OrdersCount),
			RejectedCount:  d.RejectedCount,
			CancelledCount: d.CancelledCount,
			FailureRate:    rate(d.RejectedCount, d.OrdersCount),
			DeliveredCount: d.DeliveryMsCount,
			AvgDeliveryMs:  avg(d.DeliveryMsSum, d.DeliveryMsCount),
			PaymentsCount:  d.PaymentsCount,
			PaymentsMinor:  d.PaymentsSum,
		})
	}
	return rows, nil
}

// ActiveRestaurants: restaurants with ≥1 order, per day and distinct over the range.
func (s *Service) ActiveRestaurants(ctx context.Context, in analytics.RangeInput) (analytics.ActiveRestaurantsReport, error) {
	from, to, err := checkRange(in.From, in.To)
	if err != nil {
		return analytics.ActiveRestaurantsReport{}, err
	}
	total, days, err := s.st.Restaurants.ActiveRestaurants(ctx, from, to)
	if err != nil {
		return analytics.ActiveRestaurantsReport{}, err
	}
	rep := analytics.ActiveRestaurantsReport{ActiveRestaurants: total, Days: make([]analytics.ActiveRestaurantsDay, 0, len(days))}
	for _, d := range days {
		rep.Days = append(rep.Days, analytics.ActiveRestaurantsDay{Date: d.Date, ActiveRestaurants: d.Count})
	}
	return rep, nil
}

// TopRestaurants ranks restaurants by revenue in one currency.
func (s *Service) TopRestaurants(ctx context.Context, in analytics.TopRestaurantsInput) ([]analytics.RestaurantRankRow, error) {
	from, to, err := checkRange(in.From, in.To)
	if err != nil {
		return nil, err
	}
	totals, err := s.st.Restaurants.TopRestaurants(ctx, from, to, in.Currency, limitOrDefault(in.Limit))
	if err != nil {
		return nil, err
	}
	rows := make([]analytics.RestaurantRankRow, 0, len(totals))
	for _, t := range totals {
		rows = append(rows, analytics.RestaurantRankRow{
			RestaurantID:  t.RestaurantID,
			OrdersCount:   t.OrdersCount,
			RevenueMinor:  t.RevenueSum,
			AvgOrderMinor: avg(t.RevenueSum, t.OrdersCount),
			Currency:      t.Currency,
		})
	}
	return rows, nil
}
