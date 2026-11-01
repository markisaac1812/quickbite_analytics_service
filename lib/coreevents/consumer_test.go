package coreevents

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/quickbite/analytics-service/internal/testkit/assert"
	"github.com/quickbite/analytics-service/pkg/messaging"
)

// fakeBroker records the topology and keeps the handler so the test can
// deliver messages to it by hand.
type fakeBroker struct {
	declareErr error
	consumeErr error
	declared   []messaging.Topology
	handler    messaging.Handler
}

func (b *fakeBroker) Connect(context.Context) error { return nil }
func (b *fakeBroker) Close(context.Context) error   { return nil }
func (b *fakeBroker) DeclareTopology(t messaging.Topology) error {
	b.declared = append(b.declared, t)
	return b.declareErr
}
func (b *fakeBroker) Consume(_ context.Context, _ messaging.Topology, h messaging.Handler) error {
	b.handler = h
	return b.consumeErr
}
func (b *fakeBroker) Publish(context.Context, string, string, []byte) error { return nil }
func (b *fakeBroker) IsConnected() bool                                     { return true }

var topology = messaging.Topology{Exchange: "order.events", Queue: "q", BindingKeys: []string{"order.#"}, DeadLetterExchange: "q.dlx", DeadLetterQueue: "q.dlq", Prefetch: 1}

func TestConsumer_Start(t *testing.T) {
	t.Run("declares the topology then starts consuming", func(t *testing.T) {
		// Arrange
		b := &fakeBroker{}
		c := NewConsumer(b, NewDispatcher(NoDedupe{}, nil, time.Second, discard), topology, discard)

		// Act
		err := c.Start(context.Background())

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, b.declared, []messaging.Topology{topology})
		assert.True(t, b.handler != nil)
	})

	t.Run("fails when the topology cannot be declared", func(t *testing.T) {
		// Arrange
		b := &fakeBroker{declareErr: errors.New("PRECONDITION_FAILED")}
		c := NewConsumer(b, NewDispatcher(NoDedupe{}, nil, time.Second, discard), topology, discard)

		// Act
		err := c.Start(context.Background())

		// Assert
		assert.Contains(t, err.Error(), "declare topology: PRECONDITION_FAILED")
	})

	t.Run("fails when consuming cannot start", func(t *testing.T) {
		// Arrange
		b := &fakeBroker{consumeErr: errors.New("channel closed")}
		c := NewConsumer(b, NewDispatcher(NoDedupe{}, nil, time.Second, discard), topology, discard)

		// Act
		err := c.Start(context.Background())

		// Assert
		assert.Contains(t, err.Error(), "consume: channel closed")
	})
}

func TestConsumer_Handle(t *testing.T) {
	start := func(t *testing.T, h EventHandler) *fakeBroker {
		t.Helper()
		b := &fakeBroker{}
		d := NewDispatcher(NoDedupe{}, nil, time.Second, discard)
		_ = d.Register("order.placed", h)
		assert.NoErr(t, NewConsumer(b, d, topology, discard).Start(context.Background()))
		return b
	}

	t.Run("acks (nil) an applied event", func(t *testing.T) {
		// Arrange
		var got Envelope
		b := start(t, func(_ context.Context, e Envelope) error { got = e; return nil })
		msg := messaging.Message{RoutingKey: "order.placed", Body: []byte(`{"eventId":"e-1","eventType":"order.placed","payload":{}}`)}

		// Act
		err := b.handler(context.Background(), msg)

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, got.EventID, "e-1")
	})

	t.Run("dead-letters (error) a malformed envelope", func(t *testing.T) {
		// Arrange
		b := start(t, func(context.Context, Envelope) error { return nil })

		// Act
		err := b.handler(context.Background(), messaging.Message{RoutingKey: "order.placed", Body: []byte(`nope`)})

		// Assert
		assert.Err(t, err)
	})

	t.Run("dead-letters (error) a failed handler", func(t *testing.T) {
		// Arrange
		boom := errors.New("boom")
		b := start(t, func(context.Context, Envelope) error { return boom })

		// Act
		err := b.handler(context.Background(), messaging.Message{Body: []byte(`{"eventId":"e-1","eventType":"order.placed"}`)})

		// Assert
		assert.ErrIs(t, err, boom)
	})
}
