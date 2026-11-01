package testkit

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/quickbite/analytics-service/app/analytics"
	"github.com/quickbite/analytics-service/lib/coreevents"
)

// Event factories (≈ tests/helpers/factories.ts). Payloads default to
// restaurant 42 / branch 7 / EGP; tests override the fields they care about.

const Currency = "EGP"

// Placed is an order.placed payload: total 2500 = 2×1000 (product 1) + 1×500 (product 2).
func Placed(orderID string, placedAt time.Time) analytics.OrderPlacedPayload {
	return analytics.OrderPlacedPayload{
		OrderID: orderID, Region: "eg", CountryCode: "EG",
		RestaurantID: RestaurantID, BranchID: BranchID, CustomerID: 30,
		Status: "placed", PaymentMethod: "cod",
		Subtotal: 2500, Total: 2500, Currency: Currency,
		Items: []analytics.OrderPlacedItem{
			{ProductID: 1, Quantity: 2, UnitPrice: 1000, LineTotal: 2000},
			{ProductID: 2, Quantity: 1, UnitPrice: 500, LineTotal: 500},
		},
		PlacedAt: placedAt.UTC().Format(time.RFC3339Nano),
	}
}

func Delivered(orderID string, deliveredAt time.Time) analytics.OrderDeliveredPayload {
	return analytics.OrderDeliveredPayload{
		OrderID: orderID, Region: "eg", RestaurantID: RestaurantID, BranchID: BranchID,
		CustomerID: 30, DeliveryAgentID: 5, Total: 2500, Currency: Currency,
		DeliveredAt: deliveredAt.UTC().Format(time.RFC3339Nano),
	}
}

func Rejected(orderID string, occurredAt time.Time) analytics.OrderTransitionPayload {
	reason := "out of stock"
	return analytics.OrderTransitionPayload{
		OrderID: orderID, Region: "eg", RestaurantID: RestaurantID, BranchID: BranchID,
		CustomerID: 30, Status: "rejected", Reason: &reason, Actor: "restaurant",
		OccurredAt: occurredAt.UTC().Format(time.RFC3339Nano),
	}
}

func PaymentCompleted(orderID string, amount int64, completedAt time.Time) analytics.PaymentCompletedPayload {
	return analytics.PaymentCompletedPayload{
		OrderID: orderID, Region: "eg", RestaurantID: RestaurantID, BranchID: BranchID,
		CustomerID: 30, Provider: "kashier", ProviderReferenceID: "ref-" + orderID,
		Amount: amount, Currency: Currency, Method: "card",
		CompletedAt: completedAt.UTC().Format(time.RFC3339Nano),
	}
}

// Envelope wraps payload the way order-service's outbox drainer does, with a
// fresh eventId and occurredAt = now.
func Envelope(eventType string, payload any) coreevents.Envelope {
	raw, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return coreevents.Envelope{
		EventID:       uuid.NewString(),
		EventType:     eventType,
		OccurredAt:    time.Now().UTC().Format(time.RFC3339Nano),
		AggregateType: "order",
		Region:        "eg",
		Payload:       raw,
	}
}

// JSON encodes an envelope as it travels on the wire / in a backfill file.
func JSON(env coreevents.Envelope) []byte {
	b, err := json.Marshal(env)
	if err != nil {
		panic(err)
	}
	return b
}
