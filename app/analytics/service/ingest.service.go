package service

import (
	"context"
	"fmt"
	"sort"

	"github.com/quickbite/analytics-service/app/analytics"
	"github.com/quickbite/analytics-service/app/analytics/entity"
	"github.com/quickbite/analytics-service/lib/logger"
)

// Idempotency is NOT these methods' job: the dispatcher has already deduped
// the eventId (and, in tx mode, wraps the whole method in one transaction).
//
// Day bucket rule: order-scoped metrics (orders, revenue, delivery time,
// rejections) land on the UTC day the order was PLACED, so "orders on day X"
// and "rejections of day X's orders" share a denominator. Payments land on
// the UTC day they were captured.

// OnOrderPlaced: orders_count += 1, revenue_sum += total on the restaurant,
// branch and platform day; per-product units/revenue via one BulkWrite; then
// records the placed facts and settles any metric waiting for them
// (delivered/rejected that arrived first).
func (s *Service) OnOrderPlaced(ctx context.Context, in analytics.OnOrderPlacedInput) error {
	if in.OrderID == "" || in.RestaurantID <= 0 || in.Currency == "" || in.TotalMinor < 0 || in.PlacedAt.IsZero() {
		return analytics.ErrInvalidEventPayload.WithMessage(
			fmt.Sprintf("order.placed %s: orderId, restaurantId, currency, total>=0 and placedAt are required", in.OrderID))
	}
	lines, err := mergeLines(in.Lines)
	if err != nil {
		return analytics.ErrInvalidEventPayload.WithMessage(fmt.Sprintf("order.placed %s: %s", in.OrderID, err))
	}

	date := dateKey(in.PlacedAt)
	if err := s.incDay(ctx, in.RestaurantID, in.BranchID, date, in.Currency,
		analytics.DayDelta{Orders: 1, Revenue: in.TotalMinor}); err != nil {
		return err
	}
	if err := s.st.Products.IncrementLines(ctx, in.RestaurantID, date, in.Currency, lines, s.now().UTC()); err != nil {
		return err
	}
	if err := s.st.Timelines.RecordPlaced(ctx, analytics.TimelinePlaced{
		OrderID:      in.OrderID,
		RestaurantID: in.RestaurantID,
		BranchID:     in.BranchID,
		Currency:     in.Currency,
		TotalMinor:   in.TotalMinor,
		PlacedAt:     in.PlacedAt.UTC(),
		PlacedDate:   date,
		At:           s.now().UTC(),
	}); err != nil {
		return err
	}

	logger.FromContext(ctx).Info("order.placed applied",
		"restaurant_id", in.RestaurantID, "branch_id", in.BranchID, "date", date,
		"order_id", in.OrderID, "revenue_delta", in.TotalMinor, "product_lines", len(lines))
	return s.settle(ctx, in.OrderID)
}

// OnOrderDelivered records deliveredAt. Delivery time needs placedAt, which
// this event does not carry — if order.placed hasn't been seen yet the metric
// waits on the timeline and order.placed settles it later.
func (s *Service) OnOrderDelivered(ctx context.Context, in analytics.OnOrderDeliveredInput) error {
	if in.OrderID == "" || in.RestaurantID <= 0 || in.DeliveredAt.IsZero() {
		return analytics.ErrInvalidEventPayload.WithMessage(
			fmt.Sprintf("order.delivered %s: orderId, restaurantId and deliveredAt are required", in.OrderID))
	}
	if err := s.st.Timelines.RecordDelivered(ctx, in.OrderID, in.RestaurantID, in.BranchID, in.DeliveredAt.UTC(), s.now().UTC()); err != nil {
		return err
	}
	return s.settle(ctx, in.OrderID)
}

// OnOrderRejected records rejectedAt; the rejection is counted on the placed
// day (needs the placed facts, same out-of-order handling as delivery).
func (s *Service) OnOrderRejected(ctx context.Context, in analytics.OnOrderRejectedInput) error {
	if in.OrderID == "" || in.RestaurantID <= 0 || in.RejectedAt.IsZero() {
		return analytics.ErrInvalidEventPayload.WithMessage(
			fmt.Sprintf("order.rejected %s: orderId, restaurantId and occurredAt are required", in.OrderID))
	}
	if err := s.st.Timelines.RecordRejected(ctx, in.OrderID, in.RestaurantID, in.BranchID, in.RejectedAt.UTC(), s.now().UTC()); err != nil {
		return err
	}
	return s.settle(ctx, in.OrderID)
}

// OnPaymentCompleted: payments_count += 1, payments_sum += amount on the
// capture day. Self-contained (the payload has everything), so it needs no
// timeline even though it is written to the outbox BEFORE order.placed.
func (s *Service) OnPaymentCompleted(ctx context.Context, in analytics.OnPaymentCompletedInput) error {
	if in.RestaurantID <= 0 || in.Currency == "" || in.AmountMinor < 0 || in.CompletedAt.IsZero() {
		return analytics.ErrInvalidEventPayload.WithMessage(
			fmt.Sprintf("payment.completed %s: restaurantId, currency, amount>=0 and completedAt are required", in.OrderID))
	}
	date := dateKey(in.CompletedAt)
	if err := s.incDay(ctx, in.RestaurantID, in.BranchID, date, in.Currency,
		analytics.DayDelta{Payments: 1, PaymentsSum: in.AmountMinor}); err != nil {
		return err
	}
	logger.FromContext(ctx).Info("payment.completed applied",
		"restaurant_id", in.RestaurantID, "date", date, "order_id", in.OrderID, "amount", in.AmountMinor)
	return nil
}

// settle applies every two-fact metric whose facts are now both on the
// order's timeline. Each Claim is an atomic flip of an *_applied flag, so the
// metric is applied once even when placed and delivered race on two workers.
func (s *Service) settle(ctx context.Context, orderID string) error {
	t, err := s.st.Timelines.ClaimDelivery(ctx, orderID)
	if err != nil {
		return err
	}
	if t != nil {
		if err := s.applyDelivery(ctx, t); err != nil {
			return err
		}
	}

	t, err = s.st.Timelines.ClaimRejection(ctx, orderID)
	if err != nil {
		return err
	}
	if t != nil {
		return s.applyRejection(ctx, t)
	}
	return nil
}

func (s *Service) applyDelivery(ctx context.Context, t *entity.OrderTimeline) error {
	ms := t.DeliveredAt.Sub(*t.PlacedAt).Milliseconds()
	if ms < 0 {
		// Clock skew between producers. Counting it would drag the average
		// below zero; the claim is already taken, so it is skipped for good.
		logger.FromContext(ctx).Warn("order.delivered before placedAt, delivery time skipped",
			"order_id", t.OrderID, "delta_ms", ms)
		return nil
	}
	if err := s.incDay(ctx, t.RestaurantID, t.BranchID, t.PlacedDate, t.Currency,
		analytics.DayDelta{DeliveryMs: ms, Deliveries: 1}); err != nil {
		return err
	}
	logger.FromContext(ctx).Info("delivery time applied",
		"order_id", t.OrderID, "restaurant_id", t.RestaurantID, "date", t.PlacedDate, "delivery_ms", ms)
	return nil
}

func (s *Service) applyRejection(ctx context.Context, t *entity.OrderTimeline) error {
	if err := s.incDay(ctx, t.RestaurantID, t.BranchID, t.PlacedDate, t.Currency,
		analytics.DayDelta{Rejected: 1, RejectedRevenue: t.TotalMinor}); err != nil {
		return err
	}
	logger.FromContext(ctx).Info("rejection applied",
		"order_id", t.OrderID, "restaurant_id", t.RestaurantID, "date", t.PlacedDate)
	return nil
}

// mergeLines folds duplicate productIds into one line, so every BulkWrite
// model targets a distinct (product_id, date) row. Sorted for determinism.
func mergeLines(lines []analytics.OrderLine) ([]analytics.OrderLine, error) {
	byProduct := make(map[int64]analytics.OrderLine, len(lines))
	for _, l := range lines {
		if l.ProductID <= 0 || l.Quantity <= 0 || l.LineTotalMinor < 0 {
			return nil, fmt.Errorf("item productId>0, quantity>0, lineTotal>=0 required (got %d/%d/%d)",
				l.ProductID, l.Quantity, l.LineTotalMinor)
		}
		m := byProduct[l.ProductID]
		m.ProductID = l.ProductID
		m.Quantity += l.Quantity
		m.LineTotalMinor += l.LineTotalMinor
		byProduct[l.ProductID] = m
	}
	out := make([]analytics.OrderLine, 0, len(byProduct))
	for _, l := range byProduct {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ProductID < out[j].ProductID })
	return out, nil
}
