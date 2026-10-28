package messaging

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// ErrNotConnected is returned by Publish/DeclareTopology before Connect.
var ErrNotConnected = errors.New("messaging: not connected")

// AMQPBroker implements Broker on top of amqp091-go.
//
// Mental model (same as the Node amqp-connection-manager client)
// ---------------------------------------------------------------
//   - 1 TCP connection -> many cheap virtual "channels". One channel = one purpose.
//   - One long-lived publisher channel in confirm mode (broker acks every publish).
//   - One channel per Consume() registration.
//   - amqp091 does NOT reconnect on its own. A watcher goroutine waits on
//     NotifyClose, redials with exponential backoff, and replays every
//     registration (topology + consumer) on the new connection — the Go
//     analogue of amqp-connection-manager's `setup(ch)` replay.
type AMQPBroker struct {
	cfg Config
	log *slog.Logger

	mu        sync.Mutex // guards conn, pubCh, consumers, active, closing
	conn      *amqp.Connection
	pubCh     *amqp.Channel
	consumers []consumerReg
	active    []activeConsumer
	closing   bool
	done      chan struct{}

	pubMu    sync.Mutex     // serialises publish+confirm on the single publisher channel
	inflight sync.WaitGroup // consumer worker goroutines
	tagSeq   atomic.Uint64
}

var _ Broker = (*AMQPBroker)(nil)

// NewAMQPBroker builds an unconnected broker. A nil logger discards logs.
func NewAMQPBroker(cfg Config, log *slog.Logger) *AMQPBroker {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if cfg.ReconnectInitial <= 0 {
		cfg.ReconnectInitial = 500 * time.Millisecond
	}
	if cfg.ReconnectMax < cfg.ReconnectInitial {
		cfg.ReconnectMax = 15 * time.Second
	}
	return &AMQPBroker{cfg: cfg, log: log, done: make(chan struct{})}
}

// Connect dials (retrying up to cfg.ConnectAttempts) and opens the publisher
// channel. Idempotent: a second call on a live connection is a no-op.
func (b *AMQPBroker) Connect(ctx context.Context) error {
	if b.IsConnected() {
		return nil
	}

	attempts := max(b.cfg.ConnectAttempts, 1)
	conn, err := b.dial(ctx, attempts)
	if err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.attachLocked(conn); err != nil {
		_ = conn.Close()
		return err
	}
	go b.watch(conn)
	return nil
}

// Close cancels every consumer, waits for in-flight handlers (bounded by ctx),
// then closes the publisher channel and the connection.
func (b *AMQPBroker) Close(ctx context.Context) error {
	b.mu.Lock()
	if b.closing {
		b.mu.Unlock()
		return nil
	}
	b.closing = true
	close(b.done)
	active := b.active
	b.mu.Unlock()

	// Stop new deliveries. Workers finish the message they hold, then exit
	// when the deliveries channel closes.
	for _, a := range active {
		_ = a.ch.Cancel(a.tag, false)
	}

	drained := make(chan struct{})
	go func() {
		b.inflight.Wait()
		close(drained)
	}()
	select {
	case <-drained:
	case <-ctx.Done():
		b.log.Warn("rabbit close: in-flight handlers did not finish in time")
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pubCh != nil {
		_ = b.pubCh.Close()
	}
	if b.conn != nil && !b.conn.IsClosed() {
		return b.conn.Close()
	}
	return nil
}

// DeclareTopology asserts the topology on a throwaway channel.
func (b *AMQPBroker) DeclareTopology(t Topology) error {
	b.mu.Lock()
	conn := b.conn
	b.mu.Unlock()
	if conn == nil || conn.IsClosed() {
		return ErrNotConnected
	}

	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("open channel: %w", err)
	}
	defer ch.Close()
	return assertTopology(ch, t)
}

// Consume registers a handler and starts it on the current connection.
// The registration is replayed on every reconnect.
func (b *AMQPBroker) Consume(ctx context.Context, t Topology, h Handler) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.conn == nil || b.conn.IsClosed() {
		return ErrNotConnected
	}

	reg := consumerReg{ctx: context.WithoutCancel(ctx), topology: t, handler: h}
	if err := b.startConsumerLocked(b.conn, reg); err != nil {
		return err
	}
	b.consumers = append(b.consumers, reg)
	return nil
}

// Publish sends a persistent JSON message and blocks until the broker confirms.
func (b *AMQPBroker) Publish(ctx context.Context, exchange, routingKey string, body []byte) error {
	b.pubMu.Lock()
	defer b.pubMu.Unlock()

	b.mu.Lock()
	ch := b.pubCh
	b.mu.Unlock()
	if ch == nil || ch.IsClosed() {
		return ErrNotConnected
	}

	conf, err := ch.PublishWithDeferredConfirmWithContext(ctx, exchange, routingKey, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent, // survives broker restart (with a durable queue)
		Timestamp:    time.Now().UTC(),
		Body:         body,
	})
	if err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	acked, err := conf.WaitContext(ctx)
	if err != nil {
		return fmt.Errorf("publish confirm: %w", err)
	}
	if !acked {
		return errors.New("publish: broker nacked the message")
	}
	return nil
}

// IsConnected reports whether the connection is currently open.
func (b *AMQPBroker) IsConnected() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.conn != nil && !b.conn.IsClosed()
}

// ---------- connection lifecycle ----------

func (b *AMQPBroker) dial(ctx context.Context, attempts int) (*amqp.Connection, error) {
	delay := b.cfg.ReconnectInitial
	var lastErr error
	for i := 0; i < attempts; i++ {
		conn, err := amqp.Dial(b.cfg.URL)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		if i == attempts-1 {
			break
		}
		b.log.Warn("rabbit dial failed, retrying", "attempt", i+1, "retry_in", delay.String(), "error", err.Error())
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-b.done:
			return nil, errors.New("messaging: broker closed")
		}
		delay = min(delay*2, b.cfg.ReconnectMax)
	}
	return nil, fmt.Errorf("rabbit dial: %w", lastErr)
}

// attachLocked binds a fresh connection: publisher channel + replay consumers.
// Caller holds b.mu.
func (b *AMQPBroker) attachLocked(conn *amqp.Connection) error {
	pub, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("open publisher channel: %w", err)
	}
	if err := pub.Confirm(false); err != nil {
		return fmt.Errorf("enable publisher confirms: %w", err)
	}

	b.conn = conn
	b.pubCh = pub
	b.active = nil
	for _, reg := range b.consumers {
		if err := b.startConsumerLocked(conn, reg); err != nil {
			return err
		}
	}
	return nil
}

// watch blocks until the connection drops, then reconnects forever (until Close).
func (b *AMQPBroker) watch(conn *amqp.Connection) {
	closed := conn.NotifyClose(make(chan *amqp.Error, 1))
	select {
	case <-b.done:
		return
	case amqpErr := <-closed:
		if b.isClosing() {
			return
		}
		reason := "connection closed"
		if amqpErr != nil {
			reason = amqpErr.Error()
		}
		b.log.Warn("rabbit connection lost, reconnecting", "reason", reason)
	}

	for {
		next, err := b.dial(context.Background(), 1<<30)
		if err != nil {
			return // only returns on Close
		}
		b.mu.Lock()
		if b.closing {
			b.mu.Unlock()
			_ = next.Close()
			return
		}
		err = b.attachLocked(next)
		b.mu.Unlock()
		if err != nil {
			b.log.Warn("rabbit re-attach failed, retrying", "error", err.Error())
			_ = next.Close()
			continue
		}
		b.log.Info("rabbit reconnected")
		go b.watch(next)
		return
	}
}

func (b *AMQPBroker) isClosing() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closing
}

// ---------- consuming ----------

// startConsumerLocked opens a channel, asserts topology, and fans deliveries
// out to Prefetch worker goroutines. Caller holds b.mu.
func (b *AMQPBroker) startConsumerLocked(conn *amqp.Connection, reg consumerReg) error {
	t := reg.topology
	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("open consumer channel: %w", err)
	}
	if err := assertTopology(ch, t); err != nil {
		_ = ch.Close()
		return err
	}
	workers := max(t.Prefetch, 1)
	if err := ch.Qos(workers, 0, false); err != nil {
		_ = ch.Close()
		return fmt.Errorf("set prefetch: %w", err)
	}

	tag := fmt.Sprintf("%s-%d-%d", t.Queue, os.Getpid(), b.tagSeq.Add(1))
	deliveries, err := ch.Consume(t.Queue, tag, false /*autoAck*/, false, false, false, nil)
	if err != nil {
		_ = ch.Close()
		return fmt.Errorf("consume %s: %w", t.Queue, err)
	}

	for range workers {
		b.inflight.Add(1)
		go func() {
			defer b.inflight.Done()
			for d := range deliveries {
				b.dispatch(reg, d)
			}
		}()
	}
	b.active = append(b.active, activeConsumer{ch: ch, tag: tag})
	return nil
}

// dispatch runs the handler and settles the delivery: nil → ack,
// error or panic → nack without requeue (→ DLQ when configured).
func (b *AMQPBroker) dispatch(reg consumerReg, d amqp.Delivery) {
	msg := Message{RoutingKey: d.RoutingKey, Body: d.Body, MessageID: d.MessageId, Redelivered: d.Redelivered}

	if err := safeHandle(reg.ctx, reg.handler, msg); err != nil {
		b.log.Warn("rabbit handler failed, dead-lettering", "routing_key", d.RoutingKey, "error", err.Error())
		_ = d.Nack(false, false)
		return
	}
	_ = d.Ack(false)
}

func safeHandle(ctx context.Context, h Handler, msg Message) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("handler panic: %v", r)
		}
	}()
	return h(ctx, msg)
}

// assertTopology: "assert" = create if missing, else verify settings match.
func assertTopology(ch *amqp.Channel, t Topology) error {
	if err := ch.ExchangeDeclare(t.Exchange, "topic", true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare exchange %s: %w", t.Exchange, err)
	}

	// Optional dead-letter setup: rejected messages land here.
	if t.DeadLetterExchange != "" && t.DeadLetterQueue != "" {
		if err := ch.ExchangeDeclare(t.DeadLetterExchange, "topic", true, false, false, false, nil); err != nil {
			return fmt.Errorf("declare dlx %s: %w", t.DeadLetterExchange, err)
		}
		if _, err := ch.QueueDeclare(t.DeadLetterQueue, true, false, false, false, nil); err != nil {
			return fmt.Errorf("declare dlq %s: %w", t.DeadLetterQueue, err)
		}
		if err := ch.QueueBind(t.DeadLetterQueue, "#", t.DeadLetterExchange, false, nil); err != nil {
			return fmt.Errorf("bind dlq: %w", err)
		}
	}

	args := amqp.Table{}
	if t.DeadLetterExchange != "" {
		args["x-dead-letter-exchange"] = t.DeadLetterExchange
	}
	if t.Queue == "" {
		return nil // exchange-only declaration (publishers)
	}
	if _, err := ch.QueueDeclare(t.Queue, true, false, false, false, args); err != nil {
		return fmt.Errorf("declare queue %s: %w", t.Queue, err)
	}
	for _, key := range t.BindingKeys {
		if err := ch.QueueBind(t.Queue, key, t.Exchange, false, nil); err != nil {
			return fmt.Errorf("bind %s -> %s (%s): %w", t.Exchange, t.Queue, key, err)
		}
	}
	return nil
}
