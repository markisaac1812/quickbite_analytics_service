// Package entity holds plain persistence structs with bson tags. No methods
// with I/O, no driver imports — the Go analogue of the Node *.entity.ts classes.
package entity

import "time"

// DayCounters is the counter block shared by every agg_*_day collection
// (inlined into each document). Averages and rates are NEVER stored: only
// sums and counts, so partial rows merge associatively ($inc) no matter the
// order events arrive in or how often a day is replayed/rebuilt.
type DayCounters struct {
	OrdersCount        int64 `bson:"orders_count"`
	RevenueSum         int64 `bson:"revenue_sum"` // minor units, placed orders (gross)
	DeliveryMsSum      int64 `bson:"delivery_ms_sum"`
	DeliveryMsCount    int64 `bson:"delivery_ms_count"`
	RejectedCount      int64 `bson:"rejected_count"`
	RejectedRevenueSum int64 `bson:"rejected_revenue_sum"` // minor units
	PaymentsCount      int64 `bson:"payments_count"`       // captured online payments
	PaymentsSum        int64 `bson:"payments_sum"`         // minor units
}

// RestaurantDay is one document in agg_restaurant_day: one restaurant, one UTC day.
// No _id field: the natural key is (restaurant_id, date).
type RestaurantDay struct {
	RestaurantID int64  `bson:"restaurant_id"`
	Date         string `bson:"date"` // "YYYY-MM-DD", UTC
	Currency     string `bson:"currency"`
	DayCounters  `bson:",inline"`
	UpdatedAt    time.Time `bson:"updated_at"`
}
