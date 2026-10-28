// Package coreevents is the generic inbound event pipeline: parse envelope →
// route by eventType → dedupe → dispatch. It knows nothing about orders or
// aggregates; modules register handlers at boot.
//
// Go analogue of order-service's lib/core-events/consumer.ts. The name is
// kept for symmetry with the Node services; this service consumes both
// order.events (order-service) and core.events (core-service).
package coreevents

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/quickbite/analytics-service/pkg/messaging"
)

// Consumer binds a Dispatcher to one broker queue.
type Consumer struct {
	broker     messaging.Broker
	dispatcher *Dispatcher
	topology   messaging.Topology
	log        *slog.Logger
}

func NewConsumer(broker messaging.Broker, dispatcher *Dispatcher, topology messaging.Topology, log *slog.Logger) *Consumer {
	return &Consumer{broker: broker, dispatcher: dispatcher, topology: topology, log: log}
}

// Start declares the topology (queue, bindings, DLX/DLQ) and begins consuming.
func (c *Consumer) Start(ctx context.Context) error {
	if err := c.broker.DeclareTopology(c.topology); err != nil {
		return fmt.Errorf("declare topology: %w", err)
	}
	if err := c.broker.Consume(ctx, c.topology, c.handle); err != nil {
		return fmt.Errorf("consume: %w", err)
	}
	c.log.Info("event consumer started",
		"exchange", c.topology.Exchange,
		"queue", c.topology.Queue,
		"bindings", c.topology.BindingKeys,
		"dlq", c.topology.DeadLetterQueue,
		"handlers", c.dispatcher.RegisteredTypes(),
		"transactional", c.dispatcher.Transactional(),
	)
	return nil
}

// handle is the messaging.Handler. Contract with the broker:
// nil → ack, error → nack without requeue (→ DLQ).
func (c *Consumer) handle(ctx context.Context, msg messaging.Message) error {
	env, err := Decode(msg.Body)
	if err != nil {
		c.log.Error("event: malformed envelope, dead-lettering", "routing_key", msg.RoutingKey, "error", err.Error())
		return err
	}
	_, err = c.dispatcher.Apply(ctx, env)
	return err
}
