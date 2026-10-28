// Package eventhandlers is the module's event-type → service-method table.
// Handlers are thin: decode the payload into a typed input, call the service.
// Go analogue of order-service's app/order/core-events.handlers.ts.
package eventhandlers

import (
	"context"
	"encoding/json"
	"time"

	"github.com/quickbite/analytics-service/app/analytics"
	"github.com/quickbite/analytics-service/app/analytics/service"
	"github.com/quickbite/analytics-service/lib/coreevents"
)

// Register wires every event this module consumes into the dispatcher.
// Events on the bindings without an entry here (order.accepted,
// order.cancelled, payment.failed, …) are logged and acked.
func Register(d *coreevents.Dispatcher, svc *service.Service) error {
	table := map[string]coreevents.EventHandler{
		analytics.EventOrderPlaced:      onOrderPlaced(svc),
		analytics.EventOrderDelivered:   onOrderDelivered(svc),
		analytics.EventOrderRejected:    onOrderRejected(svc),
		analytics.EventPaymentCompleted: onPaymentCompleted(svc),
	}
	for eventType, h := range table {
		if err := d.Register(eventType, h); err != nil {
			return err
		}
	}
	return nil
}

func onOrderPlaced(svc *service.Service) coreevents.EventHandler {
	return func(ctx context.Context, env coreevents.Envelope) error {
		var p analytics.OrderPlacedPayload
		if err := decode(env, &p); err != nil {
			return err
		}
		placedAt, err := firstTime(p.PlacedAt, env.OccurredAt)
		if err != nil {
			return analytics.ErrInvalidEventPayload.WithMessage("order.placed: placedAt/occurredAt missing or not RFC3339")
		}
		lines := make([]analytics.OrderLine, 0, len(p.Items))
		for _, it := range p.Items {
			lines = append(lines, analytics.OrderLine{ProductID: it.ProductID, Quantity: it.Quantity, LineTotalMinor: it.LineTotal})
		}
		return svc.OnOrderPlaced(ctx, analytics.OnOrderPlacedInput{
			EventID:      env.EventID,
			OrderID:      p.OrderID,
			RestaurantID: p.RestaurantID,
			BranchID:     p.BranchID,
			Currency:     p.Currency,
			TotalMinor:   p.Total,
			Lines:        lines,
			PlacedAt:     placedAt,
		})
	}
}

func onOrderDelivered(svc *service.Service) coreevents.EventHandler {
	return func(ctx context.Context, env coreevents.Envelope) error {
		var p analytics.OrderDeliveredPayload
		if err := decode(env, &p); err != nil {
			return err
		}
		deliveredAt, err := firstTime(p.DeliveredAt, env.OccurredAt)
		if err != nil {
			return analytics.ErrInvalidEventPayload.WithMessage("order.delivered: deliveredAt/occurredAt missing or not RFC3339")
		}
		return svc.OnOrderDelivered(ctx, analytics.OnOrderDeliveredInput{
			OrderID:      p.OrderID,
			RestaurantID: p.RestaurantID,
			BranchID:     p.BranchID,
			DeliveredAt:  deliveredAt,
		})
	}
}

func onOrderRejected(svc *service.Service) coreevents.EventHandler {
	return func(ctx context.Context, env coreevents.Envelope) error {
		var p analytics.OrderTransitionPayload
		if err := decode(env, &p); err != nil {
			return err
		}
		rejectedAt, err := firstTime(p.OccurredAt, env.OccurredAt)
		if err != nil {
			return analytics.ErrInvalidEventPayload.WithMessage("order.rejected: occurredAt missing or not RFC3339")
		}
		return svc.OnOrderRejected(ctx, analytics.OnOrderRejectedInput{
			OrderID:      p.OrderID,
			RestaurantID: p.RestaurantID,
			BranchID:     p.BranchID,
			RejectedAt:   rejectedAt,
		})
	}
}

func onPaymentCompleted(svc *service.Service) coreevents.EventHandler {
	return func(ctx context.Context, env coreevents.Envelope) error {
		var p analytics.PaymentCompletedPayload
		if err := decode(env, &p); err != nil {
			return err
		}
		completedAt, err := firstTime(p.CompletedAt, env.OccurredAt)
		if err != nil {
			return analytics.ErrInvalidEventPayload.WithMessage("payment.completed: completedAt/occurredAt missing or not RFC3339")
		}
		return svc.OnPaymentCompleted(ctx, analytics.OnPaymentCompletedInput{
			OrderID:      p.OrderID,
			RestaurantID: p.RestaurantID,
			BranchID:     p.BranchID,
			AmountMinor:  p.Amount,
			Currency:     p.Currency,
			CompletedAt:  completedAt,
		})
	}
}

func decode(env coreevents.Envelope, out any) error {
	if err := json.Unmarshal(env.Payload, out); err != nil {
		return analytics.ErrInvalidEventPayload.WithCause(err)
	}
	return nil
}

// firstTime parses the business timestamp, falling back to the envelope's occurredAt.
func firstTime(candidates ...string) (time.Time, error) {
	var err error
	for _, c := range candidates {
		var t time.Time
		if t, err = time.Parse(time.RFC3339Nano, c); err == nil {
			return t, nil
		}
	}
	return time.Time{}, err
}
