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

// ProductDayRepo reads and writes agg_product_day.
type ProductDayRepo struct {
	coll *mongo.Collection
}

func NewProductDayRepo(db *mongo.Database) *ProductDayRepo {
	return &ProductDayRepo{coll: db.Collection(CollProductDay)}
}

// IncrementLines upserts one (product_id, date) row per line in a SINGLE
// BulkWrite — one round trip per order instead of one per line item (the Go
// analogue of order-service's bulkInsertItems). Lines must already be merged
// by product (the service does it) so each model touches a distinct row.
//
// Unordered: the models are independent, so the server may apply them in
// parallel and one failing model doesn't stop the rest (the error still
// fails the whole event, which rolls back in tx mode).
func (r *ProductDayRepo) IncrementLines(ctx context.Context, restaurantID int64, date, currency string, lines []analytics.OrderLine, at time.Time) error {
	if len(lines) == 0 {
		return nil
	}
	models := make([]mongo.WriteModel, 0, len(lines))
	for _, l := range lines {
		models = append(models, mongo.NewUpdateOneModel().
			SetFilter(bson.D{{Key: "product_id", Value: l.ProductID}, {Key: "date", Value: date}}).
			SetUpdate(bson.D{
				{Key: "$inc", Value: bson.D{
					{Key: "units_sold", Value: l.Quantity},
					{Key: "revenue_sum", Value: l.LineTotalMinor},
					{Key: "orders_count", Value: 1},
				}},
				{Key: "$set", Value: bson.D{{Key: "updated_at", Value: at}}},
				{Key: "$setOnInsert", Value: bson.D{
					{Key: "restaurant_id", Value: restaurantID},
					{Key: "currency", Value: currency},
				}},
			}).
			SetUpsert(true))
	}
	if _, err := r.coll.BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false)); err != nil {
		return fmt.Errorf("agg_product_day bulk increment (%d lines): %w", len(lines), err)
	}
	return nil
}

// TopProducts ranks a restaurant's products by revenue over a range.
// $match uses idx_agg_product_day_restaurant_id_date; output ≤ limit rows.
func (r *ProductDayRepo) TopProducts(ctx context.Context, restaurantID int64, fromDate, toDate string, limit int) ([]entity.ProductTotal, error) {
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.D{
			{Key: "restaurant_id", Value: restaurantID},
			{Key: "date", Value: dateRange(fromDate, toDate)},
		}}},
		{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: "$product_id"},
			{Key: "currency", Value: bson.D{{Key: "$first", Value: "$currency"}}},
			{Key: "units_sold", Value: bson.D{{Key: "$sum", Value: "$units_sold"}}},
			{Key: "revenue_sum", Value: bson.D{{Key: "$sum", Value: "$revenue_sum"}}},
			{Key: "orders_count", Value: bson.D{{Key: "$sum", Value: "$orders_count"}}},
		}}},
		{{Key: "$sort", Value: bson.D{{Key: "revenue_sum", Value: -1}, {Key: "_id", Value: 1}}}},
		{{Key: "$limit", Value: limit}},
	}

	cur, err := r.coll.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, fmt.Errorf("agg_product_day top: %w", err)
	}
	rows := []entity.ProductTotal{}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, fmt.Errorf("agg_product_day top decode: %w", err)
	}
	return rows, nil
}
