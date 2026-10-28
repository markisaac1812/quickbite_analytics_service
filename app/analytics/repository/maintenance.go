package repository

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// ResetAll deletes every document this module owns (aggregates, timelines,
// dedupe markers) but keeps the collections and their indexes. Used by
// cmd/backfill-aggs -reset before a full rebuild. Returns deleted counts.
func ResetAll(ctx context.Context, db *mongo.Database) (map[string]int64, error) {
	deleted := make(map[string]int64, len(AllCollections))
	for _, name := range AllCollections {
		res, err := db.Collection(name).DeleteMany(ctx, bson.D{})
		if err != nil {
			return deleted, fmt.Errorf("reset %s: %w", name, err)
		}
		deleted[name] = res.DeletedCount
	}
	return deleted, nil
}
