// Package repository is the ONLY place the mongo-driver is used for this
// module's data. Typed structs + bson tags, no ODM.
package repository

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Collection names owned by this module.
const (
	CollRestaurantDay = "agg_restaurant_day"
	CollBranchDay     = "agg_branch_day"
	CollProductDay    = "agg_product_day"
	CollPlatformDay   = "agg_platform_day"
	CollOrderTimeline = "order_timelines"
	CollEventIDs      = "event_ids"
)

// AllCollections is every collection this module writes (used by reset/backfill).
var AllCollections = []string{
	CollRestaurantDay, CollBranchDay, CollProductDay, CollPlatformDay, CollOrderTimeline, CollEventIDs,
}

// EventIDTTL is how long a processed eventId is remembered. It must exceed the
// longest realistic redelivery window (consumer restart, DLQ replay by ops).
const EventIDTTL = 7 * 24 * time.Hour

// OrderTimelineTTL bounds how long an order's facts are kept waiting for a
// counterpart event (e.g. delivered arriving long after placed).
const OrderTimelineTTL = 30 * 24 * time.Hour

// EnsureIndexes creates every index this module's queries rely on.
// Idempotent: CreateMany with an identical spec is a no-op on the server, so
// it runs on every boot. Mongo has no migrations — this file IS the schema.
//
// Changing an existing index's options (e.g. a TTL) under the same name fails
// with IndexOptionsConflict: drop it by hand (or collMod) first.
func EnsureIndexes(ctx context.Context, db *mongo.Database) error {
	specs := map[string][]mongo.IndexModel{
		CollRestaurantDay: {
			{
				// supports: Increment upsert by (restaurant_id, date)
				//           + every /restaurants/:id/* range read (equality on id, range on date)
				// unique:   one row per restaurant per day, so concurrent upserts converge
				Keys:    bson.D{{Key: "restaurant_id", Value: 1}, {Key: "date", Value: 1}},
				Options: options.Index().SetName("uq_agg_restaurant_day_restaurant_id_date").SetUnique(true),
			},
			{
				// supports: GET /analytics/platform/active-restaurants and
				//           GET /analytics/platform/top-restaurants (range on date across restaurants)
				Keys:    bson.D{{Key: "date", Value: 1}, {Key: "restaurant_id", Value: 1}},
				Options: options.Index().SetName("idx_agg_restaurant_day_date_restaurant_id"),
			},
		},
		CollBranchDay: {
			{
				// supports: Increment upsert + GET /restaurants/:rid/branches/:bid/days
				Keys:    bson.D{{Key: "branch_id", Value: 1}, {Key: "date", Value: 1}},
				Options: options.Index().SetName("uq_agg_branch_day_branch_id_date").SetUnique(true),
			},
		},
		CollProductDay: {
			{
				// supports: IncrementLines bulk upserts by (product_id, date)
				Keys:    bson.D{{Key: "product_id", Value: 1}, {Key: "date", Value: 1}},
				Options: options.Index().SetName("uq_agg_product_day_product_id_date").SetUnique(true),
			},
			{
				// supports: GET /restaurants/:id/products/top ($match restaurant + date range, then $group)
				Keys:    bson.D{{Key: "restaurant_id", Value: 1}, {Key: "date", Value: 1}},
				Options: options.Index().SetName("idx_agg_product_day_restaurant_id_date"),
			},
		},
		CollPlatformDay: {
			{
				// supports: Increment upsert + GET /analytics/platform/days (range on date)
				Keys:    bson.D{{Key: "date", Value: 1}, {Key: "currency", Value: 1}},
				Options: options.Index().SetName("uq_agg_platform_day_date_currency").SetUnique(true),
			},
		},
		CollOrderTimeline: {
			{
				// supports: Record* upserts and Claim* findOneAndUpdate by order_id
				Keys:    bson.D{{Key: "order_id", Value: 1}},
				Options: options.Index().SetName("uq_order_timelines_order_id").SetUnique(true),
			},
			{
				// TTL: drop timelines 30 days after their last update
				Keys: bson.D{{Key: "updated_at", Value: 1}},
				Options: options.Index().SetName("ttl_order_timelines_updated_at").
					SetExpireAfterSeconds(int32(OrderTimelineTTL / time.Second)),
			},
		},
		CollEventIDs: {
			{
				// supports: MarkSeen — InsertOne fails with E11000 on a replayed eventId
				Keys:    bson.D{{Key: "event_id", Value: 1}},
				Options: options.Index().SetName("uq_event_ids_event_id").SetUnique(true),
			},
			{
				// TTL: the server's TTL monitor deletes markers older than EventIDTTL
				Keys: bson.D{{Key: "received_at", Value: 1}},
				Options: options.Index().SetName("ttl_event_ids_received_at").
					SetExpireAfterSeconds(int32(EventIDTTL / time.Second)),
			},
		},
	}

	for coll, models := range specs {
		if _, err := db.Collection(coll).Indexes().CreateMany(ctx, models); err != nil {
			return fmt.Errorf("ensure indexes on %s: %w", coll, err)
		}
	}
	return nil
}
