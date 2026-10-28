package entity

// Result shapes of aggregation pipelines (read side). bson tags match the
// $group/$project output field names in the repositories.

// DateCount is {date, count} — e.g. active restaurants per day.
type DateCount struct {
	Date  string `bson:"_id"`
	Count int64  `bson:"count"`
}

// RestaurantTotal is one restaurant's totals over a date range.
type RestaurantTotal struct {
	RestaurantID int64  `bson:"_id"`
	Currency     string `bson:"currency"`
	OrdersCount  int64  `bson:"orders_count"`
	RevenueSum   int64  `bson:"revenue_sum"`
}

// ProductTotal is one product's totals over a date range.
type ProductTotal struct {
	ProductID   int64  `bson:"_id"`
	Currency    string `bson:"currency"`
	UnitsSold   int64  `bson:"units_sold"`
	RevenueSum  int64  `bson:"revenue_sum"`
	OrdersCount int64  `bson:"orders_count"`
}

// ActiveRestaurantsFacet is the single document the active-restaurants
// $facet pipeline returns.
type ActiveRestaurantsFacet struct {
	Days  []DateCount `bson:"days"`
	Total []CountOnly `bson:"total"`
}

// CountOnly is the output of a $count stage.
type CountOnly struct {
	Count int64 `bson:"count"`
}
