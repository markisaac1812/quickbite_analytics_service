package repository

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/quickbite/analytics-service/app/analytics/entity"
)

// EventIDsRepo is the idempotency store for inbound events. It satisfies
// lib/coreevents.EventDeduper implicitly (Go structural typing) — nothing in
// lib/ imports this package.
type EventIDsRepo struct {
	coll *mongo.Collection
	now  func() time.Time
}

func NewEventIDsRepo(db *mongo.Database) *EventIDsRepo {
	return &EventIDsRepo{coll: db.Collection(CollEventIDs), now: time.Now}
}

// MarkSeen inserts the marker. The unique index on event_id turns a replay
// into a duplicate-key error, which we translate to fresh=false. One round
// trip, atomic, no read-then-write race between consumers.
func (r *EventIDsRepo) MarkSeen(ctx context.Context, eventID, eventType string) (bool, error) {
	_, err := r.coll.InsertOne(ctx, entity.EventID{
		EventID:    eventID,
		EventType:  eventType,
		ReceivedAt: r.now().UTC(),
	})
	if mongo.IsDuplicateKeyError(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("event_ids insert: %w", err)
	}
	return true, nil
}

// Forget deletes the marker so the event can be processed again (used when
// the handler failed after MarkSeen, before the message is dead-lettered).
func (r *EventIDsRepo) Forget(ctx context.Context, eventID string) error {
	if _, err := r.coll.DeleteOne(ctx, bson.D{{Key: "event_id", Value: eventID}}); err != nil {
		return fmt.Errorf("event_ids delete: %w", err)
	}
	return nil
}
