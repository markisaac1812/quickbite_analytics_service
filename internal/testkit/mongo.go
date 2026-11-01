package testkit

import (
	"context"
	"fmt"
	"time"

	"github.com/quickbite/analytics-service/app/analytics/repository"
	mongopkg "github.com/quickbite/analytics-service/pkg/mongo"
)

// TestMongo is a connection to a throw-away, per-package test database
// (≈ tests/helpers/db.ts). It goes through pkg/mongo, so testkit itself never
// imports the driver (CLAUDE.md §4: driver only in repository + pkg/mongo).
type TestMongo struct {
	Client *mongopkg.Client
	DB     *mongopkg.Database
}

// OpenMongo connects, drops any leftover test database from an earlier run
// and ensures the module's indexes. suffix keeps packages isolated.
func OpenMongo(suffix string) (*TestMongo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	client, db, err := mongopkg.Connect(ctx, mongopkg.Config{
		URI: MongoURI(), Database: DBName(suffix), ConnectTimeout: 5 * time.Second, AppName: "analytics-tests",
	})
	if err != nil {
		return nil, fmt.Errorf("test mongo (%s) unreachable — is MongoDB running? %w", MongoURI(), err)
	}
	if err := db.Drop(ctx); err != nil {
		return nil, err
	}
	if err := repository.EnsureIndexes(ctx, db); err != nil {
		return nil, err
	}
	return &TestMongo{Client: client, DB: db}, nil
}

// Reset empties every collection the module owns (indexes are kept).
func (m *TestMongo) Reset() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := repository.ResetAll(ctx, m.DB)
	return err
}

// Count returns the number of documents in coll matching filter (nil = all).
func (m *TestMongo) Count(coll string, filter map[string]any) int64 {
	if filter == nil {
		filter = map[string]any{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n, err := m.DB.Collection(coll).CountDocuments(ctx, filter)
	if err != nil {
		panic(err)
	}
	return n
}

// Close drops the test database and disconnects.
func (m *TestMongo) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = m.DB.Drop(ctx)
	_ = mongopkg.Disconnect(ctx, m.Client)
}
