package coreevents

import (
	"context"
	"encoding/json"
)

// Envelope is the wire shape every QuickBite outbox drainer publishes
// (see order-service lib/events/outbox-drain.ts). Payload stays raw: each
// handler decodes it into its own payload struct.
type Envelope struct {
	EventID       string          `json:"eventId"`
	EventType     string          `json:"eventType"`
	OccurredAt    string          `json:"occurredAt"`
	AggregateType string          `json:"aggregateType,omitempty"`
	AggregateID   string          `json:"aggregateId,omitempty"`
	Region        string          `json:"region,omitempty"`
	Payload       json.RawMessage `json:"payload"`
}

// EventHandler handles one event type. Return nil when the event is fully
// applied; an error rolls back (tx mode) and dead-letters the message.
type EventHandler func(ctx context.Context, env Envelope) error

// EventDeduper answers "have I processed this eventId before?".
//
// Defined here (the consumer side) so lib/ never imports app/: the Mongo
// implementation lives in app/analytics/repository and satisfies this
// interface implicitly.
type EventDeduper interface {
	// MarkSeen atomically records eventID. fresh=false means it was already recorded.
	MarkSeen(ctx context.Context, eventID, eventType string) (fresh bool, err error)
	// Forget removes the marker so a failed event can be replayed from the DLQ.
	// Only used when there is no transaction to roll the marker back.
	Forget(ctx context.Context, eventID string) error
}

// TxRunner runs fn atomically (pkg/mongo.TxRunner satisfies it). When the
// dispatcher has one, MarkSeen and the handler's writes commit or roll back
// together: exactly-once per eventId.
type TxRunner interface {
	InTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Outcome is what happened to one event.
type Outcome string

const (
	OutcomeApplied   Outcome = "applied"
	OutcomeDuplicate Outcome = "duplicate"
	OutcomeNoHandler Outcome = "no_handler"
	OutcomeFailed    Outcome = "failed"
)
