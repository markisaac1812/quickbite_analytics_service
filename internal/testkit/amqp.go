package testkit

import (
	"context"
	"errors"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// AmqpProbe is a side-channel to the real broker for arranging and asserting
// broker state (publish an event, count dead letters, clean up). The code
// under test still uses its own messaging.AMQPBroker. Go analogue of
// tests/helpers/amqp.ts.
type AmqpProbe struct {
	conn *amqp.Connection
}

func OpenAmqp() (*AmqpProbe, error) {
	conn, err := amqp.Dial(RabbitURL())
	if err != nil {
		return nil, fmt.Errorf("test rabbitmq (%s) unreachable — is RabbitMQ running? %w", RabbitURL(), err)
	}
	return &AmqpProbe{conn: conn}, nil
}

func (p *AmqpProbe) Close() { _ = p.conn.Close() }

// Publish sends a persistent JSON message (as order-service's drainer would).
// The exchange must exist (the service under test declares it on boot).
func (p *AmqpProbe) Publish(exchange, routingKey string, body []byte) error {
	return p.withChannel(func(ch *amqp.Channel) error {
		if err := ch.Confirm(false); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conf, err := ch.PublishWithDeferredConfirmWithContext(ctx, exchange, routingKey, false, false, amqp.Publishing{
			ContentType: "application/json", DeliveryMode: amqp.Persistent, Body: body,
		})
		if err != nil {
			return err
		}
		if ok, err := conf.WaitContext(ctx); err != nil || !ok {
			return errors.Join(errors.New("publish not confirmed"), err)
		}
		return nil
	})
}

// MessageCount is the number of ready messages in queue (0 if it doesn't exist).
func (p *AmqpProbe) MessageCount(queue string) int {
	n := 0
	_ = p.withChannel(func(ch *amqp.Channel) error {
		q, err := ch.QueueDeclarePassive(queue, true, false, false, false, nil)
		if err == nil {
			n = q.Messages
		}
		return err
	})
	return n
}

// Purge removes ready messages from queue (no-op if it doesn't exist).
func (p *AmqpProbe) Purge(queue string) {
	_ = p.withChannel(func(ch *amqp.Channel) error {
		_, err := ch.QueuePurge(queue, false)
		return err
	})
}

// DeleteQueues / DeleteExchanges clean up the namespaced test topology.
func (p *AmqpProbe) DeleteQueues(names ...string) {
	for _, n := range names {
		_ = p.withChannel(func(ch *amqp.Channel) error {
			_, err := ch.QueueDelete(n, false, false, false)
			return err
		})
	}
}

func (p *AmqpProbe) DeleteExchanges(names ...string) {
	for _, n := range names {
		_ = p.withChannel(func(ch *amqp.Channel) error { return ch.ExchangeDelete(n, false, false) })
	}
}

// A broker-side error (404 on a missing queue) closes the channel, so every
// probe operation gets its own short-lived channel.
func (p *AmqpProbe) withChannel(fn func(ch *amqp.Channel) error) error {
	ch, err := p.conn.Channel()
	if err != nil {
		return err
	}
	defer func() { _ = ch.Close() }()
	return fn(ch)
}
