package coreevents

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/quickbite/analytics-service/internal/testkit/assert"
	"github.com/quickbite/analytics-service/lib/appcontext"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

// fakeDeduper is an in-memory EventDeduper.
type fakeDeduper struct {
	mu        sync.Mutex
	seen      map[string]bool
	markErr   error
	forgetErr error
	forgotten []string
}

func newFakeDeduper() *fakeDeduper { return &fakeDeduper{seen: map[string]bool{}} }

func (f *fakeDeduper) MarkSeen(_ context.Context, id, _ string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.markErr != nil {
		return false, f.markErr
	}
	if f.seen[id] {
		return false, nil
	}
	f.seen[id] = true
	return true, nil
}

func (f *fakeDeduper) Forget(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgotten = append(f.forgotten, id)
	if f.forgetErr != nil {
		return f.forgetErr
	}
	delete(f.seen, id)
	return nil
}

// fakeTx snapshots the deduper and restores it when fn fails — a stand-in
// for a Mongo transaction rolling the marker back.
type fakeTx struct {
	d     *fakeDeduper
	calls int
}

func (t *fakeTx) InTx(ctx context.Context, fn func(ctx context.Context) error) error {
	t.calls++
	t.d.mu.Lock()
	snapshot := map[string]bool{}
	for k, v := range t.d.seen {
		snapshot[k] = v
	}
	t.d.mu.Unlock()

	err := fn(ctx)
	if err != nil {
		t.d.mu.Lock()
		t.d.seen = snapshot
		t.d.mu.Unlock()
	}
	return err
}

func env(id, eventType string) Envelope {
	return Envelope{EventID: id, EventType: eventType, OccurredAt: "2026-10-01T00:00:00Z", Payload: json.RawMessage(`{}`)}
}

func TestDecode(t *testing.T) {
	t.Run("parses a well-formed envelope", func(t *testing.T) {
		// Arrange
		body := []byte(`{"eventId":"e-1","eventType":"order.placed","occurredAt":"2026-10-01T00:00:00Z","payload":{"orderId":"o-1"}}`)

		// Act
		got, err := Decode(body)

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, got.EventID, "e-1")
		assert.Equal(t, string(got.Payload), `{"orderId":"o-1"}`)
	})

	cases := map[string]string{
		"not JSON":          `{"eventId":`,
		"missing eventId":   `{"eventType":"order.placed"}`,
		"missing eventType": `{"eventId":"e-1"}`,
	}
	for name, body := range cases {
		t.Run("rejects "+name, func(t *testing.T) {
			// Arrange — body above

			// Act
			_, err := Decode([]byte(body))

			// Assert
			assert.Err(t, err)
			assert.Contains(t, err.Error(), "malformed envelope")
		})
	}
}

func TestDispatcher_Register(t *testing.T) {
	t.Run("lists registered types and refuses a second handler for one type", func(t *testing.T) {
		// Arrange
		d := NewDispatcher(NoDedupe{}, nil, time.Second, discard)
		noop := func(context.Context, Envelope) error { return nil }

		// Act
		first := d.Register("a.b", noop)
		second := d.Register("a.b", noop)

		// Assert
		assert.NoErr(t, first)
		assert.Err(t, second)
		assert.Equal(t, d.RegisteredTypes(), []string{"a.b"})
		assert.False(t, d.Transactional())
	})
}

func TestDispatcher_Apply(t *testing.T) {
	t.Run("acks an event type without a handler, without touching dedupe", func(t *testing.T) {
		// Arrange
		dd := newFakeDeduper()
		d := NewDispatcher(dd, nil, time.Second, discard)

		// Act
		outcome, err := d.Apply(context.Background(), env("e-1", "order.accepted"))

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, outcome, OutcomeNoHandler)
		assert.Equal(t, len(dd.seen), 0)
	})

	t.Run("applies a fresh event with the event id as correlation id and a deadline", func(t *testing.T) {
		// Arrange
		d := NewDispatcher(newFakeDeduper(), nil, time.Second, discard)
		var gotCorr string
		var hasDeadline bool
		_ = d.Register("order.placed", func(ctx context.Context, _ Envelope) error {
			gotCorr = appcontext.CorrelationID(ctx)
			_, hasDeadline = ctx.Deadline()
			return nil
		})

		// Act
		outcome, err := d.Apply(context.Background(), env("e-1", "order.placed"))

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, outcome, OutcomeApplied)
		assert.Equal(t, gotCorr, "e-1")
		assert.True(t, hasDeadline)
	})

	t.Run("skips a duplicate without running the handler again", func(t *testing.T) {
		// Arrange
		d := NewDispatcher(newFakeDeduper(), nil, time.Second, discard)
		runs := 0
		_ = d.Register("order.placed", func(context.Context, Envelope) error { runs++; return nil })
		_, _ = d.Apply(context.Background(), env("e-1", "order.placed"))

		// Act
		outcome, err := d.Apply(context.Background(), env("e-1", "order.placed"))

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, outcome, OutcomeDuplicate)
		assert.Equal(t, runs, 1)
	})

	t.Run("without a tx, forgets the marker when the handler fails so a replay is not skipped", func(t *testing.T) {
		// Arrange
		dd := newFakeDeduper()
		d := NewDispatcher(dd, nil, time.Second, discard)
		boom := errors.New("boom")
		_ = d.Register("order.placed", func(context.Context, Envelope) error { return boom })

		// Act
		outcome, err := d.Apply(context.Background(), env("e-1", "order.placed"))

		// Assert
		assert.ErrIs(t, err, boom)
		assert.Equal(t, outcome, OutcomeFailed)
		assert.Equal(t, dd.forgotten, []string{"e-1"})
		assert.False(t, dd.seen["e-1"])
	})

	t.Run("still reports the handler failure when Forget itself fails", func(t *testing.T) {
		// Arrange
		dd := newFakeDeduper()
		dd.forgetErr = errors.New("mongo down")
		d := NewDispatcher(dd, nil, time.Second, discard)
		boom := errors.New("boom")
		_ = d.Register("order.placed", func(context.Context, Envelope) error { return boom })

		// Act
		outcome, err := d.Apply(context.Background(), env("e-1", "order.placed"))

		// Assert
		assert.ErrIs(t, err, boom)
		assert.Equal(t, outcome, OutcomeFailed)
	})

	t.Run("fails when the dedupe store fails, without running the handler", func(t *testing.T) {
		// Arrange
		dd := newFakeDeduper()
		dd.markErr = errors.New("mongo down")
		d := NewDispatcher(dd, nil, time.Second, discard)
		ran := false
		_ = d.Register("order.placed", func(context.Context, Envelope) error { ran = true; return nil })

		// Act
		outcome, err := d.Apply(context.Background(), env("e-1", "order.placed"))

		// Assert
		assert.Equal(t, outcome, OutcomeFailed)
		assert.Contains(t, err.Error(), "dedupe: mongo down")
		assert.False(t, ran)
	})

	t.Run("in tx mode, runs MarkSeen and the handler in one transaction and rolls both back on failure", func(t *testing.T) {
		// Arrange
		dd := newFakeDeduper()
		tx := &fakeTx{d: dd}
		d := NewDispatcher(dd, tx, time.Second, discard)
		boom := errors.New("boom")
		_ = d.Register("order.placed", func(context.Context, Envelope) error { return boom })

		// Act
		outcome, err := d.Apply(context.Background(), env("e-1", "order.placed"))

		// Assert
		assert.ErrIs(t, err, boom)
		assert.Equal(t, outcome, OutcomeFailed)
		assert.True(t, d.Transactional())
		assert.Equal(t, tx.calls, 1)
		assert.False(t, dd.seen["e-1"], "marker rolled back by the tx")
		assert.Len(t, dd.forgotten, 0, "no Forget in tx mode")
	})

	t.Run("in tx mode, reports a duplicate as a skip", func(t *testing.T) {
		// Arrange
		dd := newFakeDeduper()
		d := NewDispatcher(dd, &fakeTx{d: dd}, time.Second, discard)
		_ = d.Register("order.placed", func(context.Context, Envelope) error { return nil })
		first, _ := d.Apply(context.Background(), env("e-1", "order.placed"))

		// Act
		second, err := d.Apply(context.Background(), env("e-1", "order.placed"))

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, first, OutcomeApplied)
		assert.Equal(t, second, OutcomeDuplicate)
	})
}

func TestNoDedupe(t *testing.T) {
	// Arrange
	var d NoDedupe

	// Act
	first, err1 := d.MarkSeen(context.Background(), "e-1", "rbac.permissions_changed")
	second, err2 := d.MarkSeen(context.Background(), "e-1", "rbac.permissions_changed")
	errForget := d.Forget(context.Background(), "e-1")

	// Assert
	assert.True(t, first && second, "every event is fresh")
	assert.NoErr(t, err1)
	assert.NoErr(t, err2)
	assert.NoErr(t, errForget)
}
