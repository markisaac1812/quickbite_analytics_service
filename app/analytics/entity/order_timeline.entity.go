package entity

import "time"

// OrderTimeline is one document in order_timelines: the facts analytics has
// seen about one order, so metrics that need two events (delivery time needs
// placed + delivered) are applied once, whatever order the events arrive in.
// Expires 30 days after its last update (TTL).
type OrderTimeline struct {
	OrderID          string     `bson:"order_id"`
	RestaurantID     int64      `bson:"restaurant_id"`
	BranchID         int64      `bson:"branch_id"`
	Currency         string     `bson:"currency,omitempty"`
	TotalMinor       int64      `bson:"total,omitempty"`
	PlacedAt         *time.Time `bson:"placed_at,omitempty"`
	PlacedDate       string     `bson:"placed_date,omitempty"` // bucket for every order-scoped metric
	DeliveredAt      *time.Time `bson:"delivered_at,omitempty"`
	RejectedAt       *time.Time `bson:"rejected_at,omitempty"`
	DeliveryApplied  bool       `bson:"delivery_applied"`
	RejectionApplied bool       `bson:"rejection_applied"`
	UpdatedAt        time.Time  `bson:"updated_at"`
}
