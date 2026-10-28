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

var restaurantDayProjection = dayCounterProjection("restaurant_id", "date")

// RestaurantDayRepo reads and writes agg_restaurant_day.
type RestaurantDayRepo struct {
	coll *mongo.Collection
}

func NewRestaurantDayRepo(db *mongo.Database) *RestaurantDayRepo {
	return &RestaurantDayRepo{coll: db.Collection(CollRestaurantDay)}
}

// Increment applies one associative delta to the (restaurant_id, date) row,
// creating it on first touch. A single atomic UpdateOne — no read-modify-write.
// Two upserts racing to create the same row are safe: the unique index makes
// the loser retry as an update (server-side since MongoDB 4.2).
func (r *RestaurantDayRepo) Increment(ctx context.Context, restaurantID int64, date, currency string, d analytics.DayDelta, at time.Time) error {
	filter := bson.D{{Key: "restaurant_id", Value: restaurantID}, {Key: "date", Value: date}}
	_, err := r.coll.UpdateOne(ctx, filter, dayCounterUpdate(d, currency, at, nil), options.Update().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("agg_restaurant_day increment: %w", err)
	}
	return nil
}

// FindRange returns the restaurant's rows for fromDate..toDate inclusive,
// oldest first. "YYYY-MM-DD" strings sort lexicographically = chronologically,
// so a string range is a date range. Served by uq_agg_restaurant_day_restaurant_id_date.
func (r *RestaurantDayRepo) FindRange(ctx context.Context, restaurantID int64, fromDate, toDate string) ([]entity.RestaurantDay, error) {
	filter := bson.D{{Key: "restaurant_id", Value: restaurantID}, {Key: "date", Value: dateRange(fromDate, toDate)}}
	opts := options.Find().SetProjection(restaurantDayProjection).SetSort(bson.D{{Key: "date", Value: 1}})

	cur, err := r.coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("agg_restaurant_day find: %w", err)
	}
	rows := []entity.RestaurantDay{}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, fmt.Errorf("agg_restaurant_day decode: %w", err)
	}
	return rows, nil
}

// ActiveRestaurants counts restaurants with at least one order: per day, and
// distinct over the whole range. One $facet pass over the date index
// (idx_agg_restaurant_day_date_restaurant_id). Output ≤ 366 day buckets.
func (r *RestaurantDayRepo) ActiveRestaurants(ctx context.Context, fromDate, toDate string) (int64, []entity.DateCount, error) {
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.D{
			{Key: "date", Value: dateRange(fromDate, toDate)},
			{Key: "orders_count", Value: bson.D{{Key: "$gt", Value: 0}}},
		}}},
		{{Key: "$facet", Value: bson.D{
			{Key: "days", Value: bson.A{
				bson.D{{Key: "$group", Value: bson.D{{Key: "_id", Value: "$date"}, {Key: "count", Value: bson.D{{Key: "$sum", Value: 1}}}}}},
				bson.D{{Key: "$sort", Value: bson.D{{Key: "_id", Value: 1}}}},
			}},
			{Key: "total", Value: bson.A{
				bson.D{{Key: "$group", Value: bson.D{{Key: "_id", Value: "$restaurant_id"}}}},
				bson.D{{Key: "$count", Value: "count"}},
			}},
		}}},
	}

	cur, err := r.coll.Aggregate(ctx, pipeline)
	if err != nil {
		return 0, nil, fmt.Errorf("agg_restaurant_day active: %w", err)
	}
	var out []entity.ActiveRestaurantsFacet
	if err := cur.All(ctx, &out); err != nil {
		return 0, nil, fmt.Errorf("agg_restaurant_day active decode: %w", err)
	}
	days := []entity.DateCount{}
	var total int64
	if len(out) == 1 {
		if out[0].Days != nil {
			days = out[0].Days
		}
		if len(out[0].Total) == 1 {
			total = out[0].Total[0].Count
		}
	}
	return total, days, nil
}

// TopRestaurants ranks restaurants by revenue for one currency over a range.
// $match on the date index, $group per restaurant, $sort, $limit (≤ MaxTopLimit).
func (r *RestaurantDayRepo) TopRestaurants(ctx context.Context, fromDate, toDate, currency string, limit int) ([]entity.RestaurantTotal, error) {
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.D{
			{Key: "date", Value: dateRange(fromDate, toDate)},
			{Key: "currency", Value: currency},
		}}},
		{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: "$restaurant_id"},
			{Key: "currency", Value: bson.D{{Key: "$first", Value: "$currency"}}},
			{Key: "orders_count", Value: bson.D{{Key: "$sum", Value: "$orders_count"}}},
			{Key: "revenue_sum", Value: bson.D{{Key: "$sum", Value: "$revenue_sum"}}},
		}}},
		{{Key: "$match", Value: bson.D{{Key: "orders_count", Value: bson.D{{Key: "$gt", Value: 0}}}}}},
		{{Key: "$sort", Value: bson.D{{Key: "revenue_sum", Value: -1}, {Key: "_id", Value: 1}}}},
		{{Key: "$limit", Value: limit}},
	}

	cur, err := r.coll.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, fmt.Errorf("agg_restaurant_day top: %w", err)
	}
	rows := []entity.RestaurantTotal{}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, fmt.Errorf("agg_restaurant_day top decode: %w", err)
	}
	return rows, nil
}
