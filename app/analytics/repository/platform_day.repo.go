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

var platformDayProjection = dayCounterProjection("date")

// PlatformDayRepo reads and writes agg_platform_day.
//
// Hot document: every event of the day in a currency hits the same row.
// $inc on one document is fine (the server serialises it cheaply); under
// transactions concurrent writers get WriteConflict and are retried by the
// driver. If that ever shows up in latency, split the row into N buckets
// (key + bucket = restaurant_id % N) and sum the buckets on read.
type PlatformDayRepo struct {
	coll *mongo.Collection
}

func NewPlatformDayRepo(db *mongo.Database) *PlatformDayRepo {
	return &PlatformDayRepo{coll: db.Collection(CollPlatformDay)}
}

func (r *PlatformDayRepo) Increment(ctx context.Context, date, currency string, d analytics.DayDelta, at time.Time) error {
	filter := bson.D{{Key: "date", Value: date}, {Key: "currency", Value: currency}}
	if _, err := r.coll.UpdateOne(ctx, filter, dayCounterUpdate(d, currency, at, nil), options.Update().SetUpsert(true)); err != nil {
		return fmt.Errorf("agg_platform_day increment: %w", err)
	}
	return nil
}

// FindRange returns every (date, currency) row in the range, by date then currency.
func (r *PlatformDayRepo) FindRange(ctx context.Context, fromDate, toDate string) ([]entity.PlatformDay, error) {
	opts := options.Find().
		SetProjection(platformDayProjection).
		SetSort(bson.D{{Key: "date", Value: 1}, {Key: "currency", Value: 1}})

	cur, err := r.coll.Find(ctx, bson.D{{Key: "date", Value: dateRange(fromDate, toDate)}}, opts)
	if err != nil {
		return nil, fmt.Errorf("agg_platform_day find: %w", err)
	}
	rows := []entity.PlatformDay{}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, fmt.Errorf("agg_platform_day decode: %w", err)
	}
	return rows, nil
}
