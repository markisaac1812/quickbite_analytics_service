package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/quickbite/analytics-service/app/analytics"
	"github.com/quickbite/analytics-service/app/analytics/entity"
)

// OrderTimelineRepo reads and writes order_timelines.
type OrderTimelineRepo struct {
	coll *mongo.Collection
}

func NewOrderTimelineRepo(db *mongo.Database) *OrderTimelineRepo {
	return &OrderTimelineRepo{coll: db.Collection(CollOrderTimeline)}
}

// RecordPlaced stores the placed facts. $set, not $inc: idempotent.
func (r *OrderTimelineRepo) RecordPlaced(ctx context.Context, in analytics.TimelinePlaced) error {
	update := bson.D{
		{Key: "$set", Value: bson.D{
			{Key: "restaurant_id", Value: in.RestaurantID},
			{Key: "branch_id", Value: in.BranchID},
			{Key: "currency", Value: in.Currency},
			{Key: "total", Value: in.TotalMinor},
			{Key: "placed_at", Value: in.PlacedAt},
			{Key: "placed_date", Value: in.PlacedDate},
			{Key: "updated_at", Value: in.At},
		}},
		{Key: "$setOnInsert", Value: bson.D{
			{Key: "delivery_applied", Value: false},
			{Key: "rejection_applied", Value: false},
		}},
	}
	return r.upsert(ctx, in.OrderID, update, "placed")
}

func (r *OrderTimelineRepo) RecordDelivered(ctx context.Context, orderID string, restaurantID, branchID int64, deliveredAt, at time.Time) error {
	return r.upsert(ctx, orderID, r.factUpdate("delivered_at", deliveredAt, restaurantID, branchID, at), "delivered")
}

func (r *OrderTimelineRepo) RecordRejected(ctx context.Context, orderID string, restaurantID, branchID int64, rejectedAt, at time.Time) error {
	return r.upsert(ctx, orderID, r.factUpdate("rejected_at", rejectedAt, restaurantID, branchID, at), "rejected")
}

// ClaimDelivery atomically flips delivery_applied false→true when both
// placed_at and delivered_at are present. Exactly one caller ever gets the
// document back, however many events race for it.
func (r *OrderTimelineRepo) ClaimDelivery(ctx context.Context, orderID string) (*entity.OrderTimeline, error) {
	return r.claim(ctx, orderID, "delivered_at", "delivery_applied")
}

func (r *OrderTimelineRepo) ClaimRejection(ctx context.Context, orderID string) (*entity.OrderTimeline, error) {
	return r.claim(ctx, orderID, "rejected_at", "rejection_applied")
}

// factUpdate: a later fact sets its own timestamp; tenant ids only on insert
// (placed, when it arrives, overwrites them with the authoritative values).
func (r *OrderTimelineRepo) factUpdate(field string, ts time.Time, restaurantID, branchID int64, at time.Time) bson.D {
	return bson.D{
		{Key: "$set", Value: bson.D{{Key: field, Value: ts}, {Key: "updated_at", Value: at}}},
		{Key: "$setOnInsert", Value: bson.D{
			{Key: "restaurant_id", Value: restaurantID},
			{Key: "branch_id", Value: branchID},
			{Key: "delivery_applied", Value: false},
			{Key: "rejection_applied", Value: false},
		}},
	}
}

func (r *OrderTimelineRepo) upsert(ctx context.Context, orderID string, update bson.D, fact string) error {
	_, err := r.coll.UpdateOne(ctx, bson.D{{Key: "order_id", Value: orderID}}, update, options.Update().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("order_timelines record %s: %w", fact, err)
	}
	return nil
}

func (r *OrderTimelineRepo) claim(ctx context.Context, orderID, factField, flag string) (*entity.OrderTimeline, error) {
	filter := bson.D{
		{Key: "order_id", Value: orderID},
		{Key: "placed_at", Value: bson.D{{Key: "$exists", Value: true}}},
		{Key: factField, Value: bson.D{{Key: "$exists", Value: true}}},
		{Key: flag, Value: bson.D{{Key: "$ne", Value: true}}},
	}
	update := bson.D{{Key: "$set", Value: bson.D{{Key: flag, Value: true}}}}

	var t entity.OrderTimeline
	err := r.coll.FindOneAndUpdate(ctx, filter, update,
		options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&t)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil // pair incomplete, or already applied
	}
	if err != nil {
		return nil, fmt.Errorf("order_timelines claim %s: %w", flag, err)
	}
	return &t, nil
}
