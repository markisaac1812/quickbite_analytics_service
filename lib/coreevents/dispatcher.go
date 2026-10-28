package coreevents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/quickbite/analytics-service/lib/appcontext"
	"github.com/quickbite/analytics-service/lib/logger"
)

// errDuplicate aborts the transaction when MarkSeen finds a replay.
var errDuplicate = errors.New("coreevents: duplicate event")

// Dispatcher owns the handler table and the apply pipeline:
// route by eventType → dedupe → handler, optionally inside one transaction.
// It knows nothing about brokers, so the live consumer and the backfill
// command share the exact same code path.
type Dispatcher struct {
	deduper EventDeduper
	tx      TxRunner // nil → no transactions (Forget-on-failure fallback)
	timeout time.Duration
	log     *slog.Logger

	handlers map[string]EventHandler
}

// NewDispatcher: tx may be nil. Register all handlers before the first Apply;
// the table is read-only afterwards, so it needs no lock.
func NewDispatcher(deduper EventDeduper, tx TxRunner, timeout time.Duration, log *slog.Logger) *Dispatcher {
	return &Dispatcher{deduper: deduper, tx: tx, timeout: timeout, log: log, handlers: map[string]EventHandler{}}
}

// Register maps an event type (= routing key) to a handler.
func (d *Dispatcher) Register(eventType string, h EventHandler) error {
	if _, dup := d.handlers[eventType]; dup {
		return fmt.Errorf("coreevents: handler already registered for %s", eventType)
	}
	d.handlers[eventType] = h
	return nil
}

// RegisteredTypes lists the event types with a handler (for boot logs).
func (d *Dispatcher) RegisteredTypes() []string {
	out := make([]string, 0, len(d.handlers))
	for t := range d.handlers {
		out = append(out, t)
	}
	return out
}

// Transactional reports whether applies run inside a transaction.
func (d *Dispatcher) Transactional() bool { return d.tx != nil }

// Decode parses and sanity-checks an envelope.
func Decode(body []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return Envelope{}, fmt.Errorf("malformed envelope: %w", err)
	}
	if env.EventID == "" || env.EventType == "" {
		return Envelope{}, errors.New("malformed envelope: missing eventId or eventType")
	}
	return env, nil
}

// Apply runs one event through the pipeline.
//
//  1. no handler for the type  → OutcomeNoHandler (caller acks; bindings are wide on purpose)
//  2. MarkSeen says duplicate  → OutcomeDuplicate (ack-and-skip)
//  3. handler ok               → OutcomeApplied
//  4. handler fails            → OutcomeFailed + error. In tx mode the marker and every
//     write roll back together; without a tx the marker is Forgotten so a DLQ replay
//     is not skipped (earlier writes of a partially applied event are NOT undone).
func (d *Dispatcher) Apply(ctx context.Context, env Envelope) (Outcome, error) {
	// Every log line for this event carries its id as the correlation id.
	ctx = appcontext.WithCorrelationID(ctx, env.EventID)
	log := d.log.With("correlation_id", env.EventID, "event_id", env.EventID, "event_type", env.EventType)
	ctx = logger.WithContext(ctx, log)

	handler, ok := d.handlers[env.EventType]
	if !ok {
		log.Info("event: no handler, acking")
		return OutcomeNoHandler, nil
	}

	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()

	run := func(ctx context.Context) error {
		fresh, err := d.deduper.MarkSeen(ctx, env.EventID, env.EventType)
		if err != nil {
			return fmt.Errorf("dedupe: %w", err)
		}
		if !fresh {
			return errDuplicate
		}
		return handler(ctx, env)
	}

	var err error
	if d.tx != nil {
		err = d.tx.InTx(ctx, run)
	} else {
		err = run(ctx)
		if err != nil && !errors.Is(err, errDuplicate) {
			if ferr := d.deduper.Forget(context.WithoutCancel(ctx), env.EventID); ferr != nil {
				log.Error("event: failed to forget dedupe marker", "error", ferr.Error())
			}
		}
	}

	switch {
	case errors.Is(err, errDuplicate):
		log.Info("event: duplicate, skipping")
		return OutcomeDuplicate, nil
	case err != nil:
		log.Error("event: handler failed", "error", err.Error(), "transactional", d.tx != nil)
		return OutcomeFailed, err
	default:
		log.Info("event: applied")
		return OutcomeApplied, nil
	}
}
