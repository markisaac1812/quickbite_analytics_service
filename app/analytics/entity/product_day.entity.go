package entity

import "time"

// ProductDay is one document in agg_product_day: one product, one UTC day
// (by the order's placedAt). orders_count = orders containing the product.
type ProductDay struct {
	ProductID    int64     `bson:"product_id"`
	RestaurantID int64     `bson:"restaurant_id"`
	Date         string    `bson:"date"`
	Currency     string    `bson:"currency"`
	UnitsSold    int64     `bson:"units_sold"`
	RevenueSum   int64     `bson:"revenue_sum"` // sum of lineTotal, minor units
	OrdersCount  int64     `bson:"orders_count"`
	UpdatedAt    time.Time `bson:"updated_at"`
}
