package repository

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/quickbite/analytics-service/app/analytics"
	"github.com/quickbite/analytics-service/app/analytics/entity"
)

var branchDayProjection = dayCounterProjection("branch_id", "restaurant_id", "date")

// BranchDayRepo reads and writes agg_branch_day.
type BranchDayRepo struct {
	coll *mongo.Collection
}

func NewBranchDayRepo(db *mongo.Database) *BranchDayRepo {
	return &BranchDayRepo{coll: db.Collection(CollBranchDay)}
}

// Increment applies one delta to the (branch_id, date) row; restaurant_id is
// set on insert (a branch never changes restaurant).
func (r *BranchDayRepo) Increment(ctx context.Context, branchID, restaurantID int64, date, currency string, d analytics.DayDelta, at time.Time) error {
	filter := bson.D{{Key: "branch_id", Value: branchID}, {Key: "date", Value: date}}
	update := dayCounterUpdate(d, currency, at, bson.D{{Key: "restaurant_id", Value: restaurantID}})
	if _, err := r.coll.UpdateOne(ctx, filter, update, options.Update().SetUpsert(true)); err != nil {
		return fmt.Errorf("agg_branch_day increment: %w", err)
	}
	return nil
}

// FindRange returns one branch's rows, oldest first. restaurant_id is part of
// the filter so a branch id from another tenant simply returns nothing.
// Served by uq_agg_branch_day_branch_id_date (restaurant_id is a residual filter).
func (r *BranchDayRepo) FindRange(ctx context.Context, restaurantID, branchID int64, fromDate, toDate string) ([]entity.BranchDay, error) {
	filter := bson.D{
		{Key: "branch_id", Value: branchID},
		{Key: "date", Value: dateRange(fromDate, toDate)},
		{Key: "restaurant_id", Value: restaurantID},
	}
	opts := options.Find().SetProjection(branchDayProjection).SetSort(bson.D{{Key: "date", Value: 1}})

	cur, err := r.coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("agg_branch_day find: %w", err)
	}
	rows := []entity.BranchDay{}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, fmt.Errorf("agg_branch_day decode: %w", err)
	}
	return rows, nil
}
