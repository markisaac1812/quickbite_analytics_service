package repository

import (
	"time"

	"go.mongodb.org/mongo-driver/bson"

	"github.com/quickbite/analytics-service/app/analytics"
)

// dayCounterUpdate builds the upsert update shared by every agg_*_day
// collection: $inc every counter (zeros included, so a freshly inserted row
// already has the full shape), stamp updated_at, set currency on insert.
func dayCounterUpdate(d analytics.DayDelta, currency string, at time.Time, setOnInsert bson.D) bson.D {
	onInsert := append(bson.D{{Key: "currency", Value: currency}}, setOnInsert...)
	return bson.D{
		{Key: "$inc", Value: bson.D{
			{Key: "orders_count", Value: d.Orders},
			{Key: "revenue_sum", Value: d.Revenue},
			{Key: "delivery_ms_sum", Value: d.DeliveryMs},
			{Key: "delivery_ms_count", Value: d.Deliveries},
			{Key: "rejected_count", Value: d.Rejected},
			{Key: "rejected_revenue_sum", Value: d.RejectedRevenue},
			{Key: "payments_count", Value: d.Payments},
			{Key: "payments_sum", Value: d.PaymentsSum},
		}},
		{Key: "$set", Value: bson.D{{Key: "updated_at", Value: at}}},
		{Key: "$setOnInsert", Value: onInsert},
	}
}

// dayCounterProjection lists the counter fields (≈ the Node <MODULE>_COLUMNS).
func dayCounterProjection(keyFields ...string) bson.D {
	p := bson.D{{Key: "_id", Value: 0}}
	for _, f := range keyFields {
		p = append(p, bson.E{Key: f, Value: 1})
	}
	for _, f := range []string{
		"currency", "orders_count", "revenue_sum", "delivery_ms_sum", "delivery_ms_count",
		"rejected_count", "rejected_revenue_sum", "payments_count", "payments_sum", "updated_at",
	} {
		p = append(p, bson.E{Key: f, Value: 1})
	}
	return p
}

// dateRange is the {$gte, $lte} filter on a "YYYY-MM-DD" field.
func dateRange(fromDate, toDate string) bson.D {
	return bson.D{{Key: "$gte", Value: fromDate}, {Key: "$lte", Value: toDate}}
}
