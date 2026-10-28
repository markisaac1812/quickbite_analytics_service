package entity

import "time"

// BranchDay is one document in agg_branch_day: one branch, one UTC day.
// restaurant_id is denormalised so a branch row can be scoped to its tenant.
type BranchDay struct {
	BranchID     int64  `bson:"branch_id"`
	RestaurantID int64  `bson:"restaurant_id"`
	Date         string `bson:"date"`
	Currency     string `bson:"currency"`
	DayCounters  `bson:",inline"`
	UpdatedAt    time.Time `bson:"updated_at"`
}
