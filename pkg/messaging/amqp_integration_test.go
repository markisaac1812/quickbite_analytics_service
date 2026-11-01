//go:build integration

package messaging

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/quickbite/analytics-service/internal/testkit/assert"
)

// Internal test (package messaging) so the reconnect test can drop the live
// connection. Like pkg/ itself, it uses no app helpers (no testkit).

func rabbitURL() string {
	if v := os.Getenv("TEST_RABBITMQ_URL"); v != "" {
		return v
	}
	return "amqp://guest:guest@localhost:5672"
}

// topo returns a namespaced topology and deletes it when the test ends.
func topo(t *testing.T, prefetch int) Topology {
	t.Helper()
	ns := fmt.Sprintf("analytics-test-messaging.%s.%d", t.Name(), time.Now().UnixNano())
	tp := Topology{
		Exchange: ns + ".x", Queue: ns + ".q", BindingKeys: []string{"order.#"},
		DeadLetterExchange: ns + ".dlx", DeadLetterQueue: ns + ".dlq", Prefetch: prefetch,
	}
	t.Cleanup(func() {
		conn, err := amqp.Dial(rabbitURL())
		if err != nil {
			return
		}
		defer conn.Close()
		ch, err := conn.Channel()
		if err != nil {
			return
		}
		defer ch.Close()
		_, _ = ch.QueueDelete(tp.Queue, false, false, false)
		_, _ = ch.QueueDelete(tp.DeadLetterQueue, false, false, false)
		_ = ch.ExchangeDelete(tp.Exchange, false, false)
		_ = ch.ExchangeDelete(tp.DeadLetterExchange, false, false)
	})
	return tp
}

func connected(t *testing.T) *AMQPBroker {
	t.Helper()
	b := NewAMQPBroker(Config{URL: rabbitURL(), ReconnectInitial: 20 * time.Millisecond, ReconnectMax: 100 * time.Millisecond, ConnectAttempts: 2}, nil)
	assert.NoErr(t, b.Connect(context.Background()))
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	return b
}

func queueDepth(t *testing.T, queue string) int {
	t.Helper()
	conn, err := amqp.Dial(rabbitURL())
	assert.NoErr(t, err)
	defer conn.Close()
	ch, err := conn.Channel()
	assert.NoErr(t, err)
	defer ch.Close()
	q, err := ch.QueueDeclarePassive(queue, true, false, false, false, nil)
	assert.NoErr(t, err)
	return q.Messages
}

func eventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for: %s", msg)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestBroker_Lifecycle(t *testing.T) {
	t.Run("connects idempotently and reports the connection", func(t *testing.T) {
		// Arrange
		b := connected(t)

		// Act
		err := b.Connect(context.Background())

		// Assert
		assert.NoErr(t, err)
		assert.True(t, b.IsConnected())
	})

	t.Run("refuses to work before Connect", func(t *testing.T) {
		// Arrange
		b := NewAMQPBroker(Config{URL: rabbitURL()}, nil)

		// Act
		declareErr := b.DeclareTopology(Topology{Exchange: "x"})
		consumeErr := b.Consume(context.Background(), Topology{}, nil)
		publishErr := b.Publish(context.Background(), "x", "k", nil)

		// Assert
		assert.ErrIs(t, declareErr, ErrNotConnected)
		assert.ErrIs(t, consumeErr, ErrNotConnected)
		assert.ErrIs(t, publishErr, ErrNotConnected)
		assert.False(t, b.IsConnected())
	})

	t.Run("gives up dialing after the configured attempts", func(t *testing.T) {
		// Arrange
		b := NewAMQPBroker(Config{URL: "amqp://guest:guest@127.0.0.1:1", ReconnectInitial: time.Millisecond, ConnectAttempts: 2}, nil)

		// Act
		err := b.Connect(context.Background())

		// Assert
		assert.Contains(t, err.Error(), "rabbit dial")
	})

	t.Run("stops dialing when the context is cancelled", func(t *testing.T) {
		// Arrange
		b := NewAMQPBroker(Config{URL: "amqp://guest:guest@127.0.0.1:1", ReconnectInitial: time.Hour, ConnectAttempts: 3}, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		// Act
		err := b.Connect(ctx)

		// Assert
		assert.ErrIs(t, err, context.DeadlineExceeded)
	})

	t.Run("stops dialing when the broker is closed", func(t *testing.T) {
		// Arrange
		b := NewAMQPBroker(Config{URL: "amqp://guest:guest@127.0.0.1:1", ReconnectInitial: time.Hour, ConnectAttempts: 3}, nil)
		go func() { time.Sleep(50 * time.Millisecond); _ = b.Close(context.Background()) }()

		// Act
		err := b.Connect(context.Background())

		// Assert
		assert.Contains(t, err.Error(), "broker closed")
	})

	t.Run("closes idempotently", func(t *testing.T) {
		// Arrange
		b := connected(t)

		// Act
		first := b.Close(context.Background())
		second := b.Close(context.Background())

		// Assert
		assert.NoErr(t, first)
		assert.NoErr(t, second)
		assert.False(t, b.IsConnected())
	})
}

func TestBroker_Topology(t *testing.T) {
	t.Run("declares an exchange-only topology for publishers", func(t *testing.T) {
		// Arrange
		b := connected(t)
		tp := topo(t, 1)
		tp.Queue, tp.DeadLetterExchange, tp.DeadLetterQueue = "", "", ""

		// Act
		err := b.DeclareTopology(tp)

		// Assert
		assert.NoErr(t, err)
	})

	t.Run("fails when an existing queue has different arguments", func(t *testing.T) {
		// Arrange — same queue, first without then with a DLX (queue args are immutable)
		b := connected(t)
		tp := topo(t, 1)
		plain := tp
		plain.DeadLetterExchange, plain.DeadLetterQueue = "", ""
		assert.NoErr(t, b.DeclareTopology(plain))

		// Act
		err := b.DeclareTopology(tp)

		// Assert
		assert.Contains(t, err.Error(), "PRECONDITION_FAILED")
	})

	t.Run("fails to consume a topology that cannot be declared", func(t *testing.T) {
		// Arrange
		b := connected(t)
		tp := topo(t, 1)
		plain := tp
		plain.DeadLetterExchange, plain.DeadLetterQueue = "", ""
		assert.NoErr(t, b.DeclareTopology(plain))

		// Act
		err := b.Consume(context.Background(), tp, func(context.Context, Message) error { return nil })

		// Assert
		assert.Err(t, err)
	})
}

func TestBroker_PublishConsume(t *testing.T) {
	t.Run("delivers a published message to the handler and acks it", func(t *testing.T) {
		// Arrange
		b := connected(t)
		tp := topo(t, 2)
		got := make(chan Message, 1)
		assert.NoErr(t, b.Consume(context.Background(), tp, func(_ context.Context, m Message) error { got <- m; return nil }))

		// Act
		err := b.Publish(context.Background(), tp.Exchange, "order.placed", []byte(`{"n":1}`))

		// Assert
		assert.NoErr(t, err)
		select {
		case m := <-got:
			assert.Equal(t, m.RoutingKey, "order.placed")
			assert.Equal(t, string(m.Body), `{"n":1}`)
		case <-time.After(5 * time.Second):
			t.Fatal("message not delivered")
		}
		eventually(t, func() bool { return queueDepth(t, tp.Queue) == 0 }, "queue drained")
		assert.Equal(t, queueDepth(t, tp.DeadLetterQueue), 0)
	})

	t.Run("dead-letters a message whose handler fails", func(t *testing.T) {
		// Arrange
		b := connected(t)
		tp := topo(t, 1)
		assert.NoErr(t, b.Consume(context.Background(), tp, func(context.Context, Message) error { return errors.New("boom") }))

		// Act
		assert.NoErr(t, b.Publish(context.Background(), tp.Exchange, "order.placed", []byte(`{}`)))

		// Assert
		eventually(t, func() bool { return queueDepth(t, tp.DeadLetterQueue) == 1 }, "message in the DLQ")
	})

	t.Run("dead-letters a message whose handler panics, and keeps consuming", func(t *testing.T) {
		// Arrange
		b := connected(t)
		tp := topo(t, 1)
		var calls atomic.Int32
		assert.NoErr(t, b.Consume(context.Background(), tp, func(context.Context, Message) error {
			if calls.Add(1) == 1 {
				panic("handler bug")
			}
			return nil
		}))

		// Act
		assert.NoErr(t, b.Publish(context.Background(), tp.Exchange, "order.placed", []byte(`1`)))
		assert.NoErr(t, b.Publish(context.Background(), tp.Exchange, "order.placed", []byte(`2`)))

		// Assert
		eventually(t, func() bool { return calls.Load() == 2 }, "second message handled")
		eventually(t, func() bool { return queueDepth(t, tp.DeadLetterQueue) == 1 }, "panicked message in the DLQ")
	})

	t.Run("fails to publish to a missing exchange", func(t *testing.T) {
		// Arrange
		b := connected(t)

		// Act
		err := b.Publish(context.Background(), "analytics-test-messaging.does-not-exist", "k", []byte(`{}`))

		// Assert
		assert.Err(t, err)
	})

	t.Run("waits for in-flight handlers on Close", func(t *testing.T) {
		// Arrange
		b := NewAMQPBroker(Config{URL: rabbitURL()}, nil)
		assert.NoErr(t, b.Connect(context.Background()))
		tp := topo(t, 1)
		started := make(chan struct{})
		var finished atomic.Bool
		assert.NoErr(t, b.Consume(context.Background(), tp, func(context.Context, Message) error {
			close(started)
			time.Sleep(200 * time.Millisecond)
			finished.Store(true)
			return nil
		}))
		assert.NoErr(t, b.Publish(context.Background(), tp.Exchange, "order.placed", []byte(`{}`)))
		<-started

		// Act
		err := b.Close(context.Background())

		// Assert
		assert.NoErr(t, err)
		assert.True(t, finished.Load(), "handler finished before Close returned")
	})

	t.Run("gives up waiting for handlers when the close context expires", func(t *testing.T) {
		// Arrange
		b := NewAMQPBroker(Config{URL: rabbitURL()}, nil)
		assert.NoErr(t, b.Connect(context.Background()))
		tp := topo(t, 1)
		started := make(chan struct{})
		release := make(chan struct{})
		assert.NoErr(t, b.Consume(context.Background(), tp, func(context.Context, Message) error {
			close(started)
			<-release
			return nil
		}))
		assert.NoErr(t, b.Publish(context.Background(), tp.Exchange, "order.placed", []byte(`{}`)))
		<-started
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		// Act
		start := time.Now()
		_ = b.Close(ctx)

		// Assert
		assert.True(t, time.Since(start) < 2*time.Second)
		close(release)
	})
}

func TestBroker_Reconnect(t *testing.T) {
	t.Run("redials after the connection drops and replays consumers", func(t *testing.T) {
		// Arrange
		b := connected(t)
		tp := topo(t, 1)
		var mu sync.Mutex
		var bodies []string
		assert.NoErr(t, b.Consume(context.Background(), tp, func(_ context.Context, m Message) error {
			mu.Lock()
			defer mu.Unlock()
			bodies = append(bodies, string(m.Body))
			return nil
		}))
		b.mu.Lock()
		old := b.conn
		b.mu.Unlock()

		// Act — drop the live connection as a broker restart would
		assert.NoErr(t, old.Close())
		eventually(t, func() bool {
			b.mu.Lock()
			defer b.mu.Unlock()
			return b.conn != old && b.conn != nil && !b.conn.IsClosed()
		}, "reconnected")
		assert.NoErr(t, b.Publish(context.Background(), tp.Exchange, "order.placed", []byte(`after`)))

		// Assert
		eventually(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(bodies) == 1 && bodies[0] == "after"
		}, "consumer replayed on the new connection")
	})
}

func TestNewAMQPBroker_Defaults(t *testing.T) {
	// Arrange
	cfg := Config{URL: "amqp://x", ReconnectMax: time.Millisecond}

	// Act
	b := NewAMQPBroker(cfg, nil)

	// Assert
	assert.Equal(t, b.cfg.ReconnectInitial, 500*time.Millisecond)
	assert.Equal(t, b.cfg.ReconnectMax, 15*time.Second)
}
