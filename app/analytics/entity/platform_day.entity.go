package entity

import "time"

// PlatformDay is one document in agg_platform_day: the whole platform, one
// UTC day, one currency. Currency is part of the key because summing EGP and
// SAR into one number is meaningless.
type PlatformDay struct {
	Date        string `bson:"date"`
	Currency    string `bson:"currency"`
	DayCounters `bson:",inline"`
	UpdatedAt   time.Time `bson:"updated_at"`
}
