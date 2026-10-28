// Package messaging is a broker-agnostic interface plus one AMQP (RabbitMQ)
// implementation. No app types, no env, no globals.
package messaging

import (
	"context"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Message is the broker-agnostic shape handed to a Handler. The handler never
// acks/nacks by hand: returning nil acks, returning an error nacks without
// requeue (→ dead-letter queue, when one is configured).
type Message struct {
	RoutingKey  string
	Body        []byte
	MessageID   string
	Redelivered bool
}

// Handler processes one message. Return nil to ack, an error to dead-letter.
type Handler func(ctx context.Context, msg Message) error

// Topology describes an exchange → queue binding set, with an optional DLQ.
// Declaring it is idempotent ("assert" semantics).
type Topology struct {
	Exchange           string
	Queue              string
	BindingKeys        []string
	DeadLetterExchange string
	DeadLetterQueue    string
	// Prefetch is the max unacked messages in flight on the consumer channel.
	// It is also the number of worker goroutines draining the channel.
	Prefetch int
}

// Config for the AMQP broker. Reconnect backoff doubles from
// ReconnectInitial up to ReconnectMax.
type Config struct {
	URL              string
	ReconnectInitial time.Duration
	ReconnectMax     time.Duration
	// ConnectAttempts bounds the dial loop inside Connect. 0 means one attempt.
	ConnectAttempts int
}

// Broker is the interface the rest of the app depends on.
type Broker interface {
	// Connect dials the broker and opens the publisher channel. Idempotent.
	Connect(ctx context.Context) error
	// Close cancels consumers, waits (bounded by ctx) for in-flight handlers,
	// then closes the connection. Idempotent.
	Close(ctx context.Context) error
	// DeclareTopology asserts exchanges, queues and bindings.
	DeclareTopology(t Topology) error
	// Consume starts consuming in the background. The registration survives
	// reconnects: topology and the consumer are re-established automatically.
	// ctx only carries values into handlers; stopping is done via Close, so a
	// shutdown never cancels a handler halfway through its write.
	Consume(ctx context.Context, t Topology, h Handler) error
	// Publish sends a persistent message and waits for the broker's confirm.
	Publish(ctx context.Context, exchange, routingKey string, body []byte) error
	// IsConnected reports whether the underlying connection is currently open.
	IsConnected() bool
}

// consumerReg is a Consume() registration, replayed after every reconnect.
type consumerReg struct {
	ctx      context.Context
	topology Topology
	handler  Handler
}

// activeConsumer is a registration bound to a live channel.
type activeConsumer struct {
	ch  *amqp.Channel
	tag string
}
