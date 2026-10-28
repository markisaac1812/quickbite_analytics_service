package entity

import "time"

// EventID is one document in event_ids: proof that an eventId was applied.
// A unique index on event_id makes InsertOne the atomic "first time?" check;
// a TTL index on received_at expires markers after 7 days.
type EventID struct {
	EventID    string    `bson:"event_id"`
	EventType  string    `bson:"event_type"`
	ReceivedAt time.Time `bson:"received_at"`
}
