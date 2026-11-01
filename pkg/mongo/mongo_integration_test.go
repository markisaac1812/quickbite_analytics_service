//go:build integration

package mongo_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"

	"github.com/quickbite/analytics-service/internal/testkit/assert"
	mongopkg "github.com/quickbite/analytics-service/pkg/mongo"
)

// Like pkg/ itself, these tests use no app helpers (testkit/assert only).
const testDB = "quickbite_analytics_test_pkg_mongo"

func mongoURI() string {
	if v := os.Getenv("TEST_MONGO_URI"); v != "" {
		return v
	}
	return "mongodb://localhost:27017/?directConnection=true"
}

func connect(t *testing.T) (*mongopkg.Client, *mongopkg.Database) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, db, err := mongopkg.Connect(ctx, mongopkg.Config{
		URI: mongoURI(), Database: testDB,
		ConnectTimeout: 5 * time.Second, MaxPoolSize: 5, AppName: "pkg-mongo-test",
	})
	assert.NoErr(t, err)
	t.Cleanup(func() {
		_ = db.Drop(context.Background())
		_ = mongopkg.Disconnect(context.Background(), client)
	})
	return client, db
}

func TestConnect(t *testing.T) {
	t.Run("connects, pings the primary and returns the configured database", func(t *testing.T) {
		// Arrange
		client, db := connect(t)

		// Act
		err := mongopkg.Ping(context.Background(), client)

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, db.Name(), testDB)
	})

	t.Run("fails fast when the server is unreachable", func(t *testing.T) {
		// Arrange
		cfg := mongopkg.Config{URI: "mongodb://127.0.0.1:1/?directConnection=true", Database: "x", ConnectTimeout: 300 * time.Millisecond}

		// Act
		_, _, err := mongopkg.Connect(context.Background(), cfg)

		// Assert
		assert.Contains(t, err.Error(), "mongo ping")
	})

	t.Run("rejects an invalid URI", func(t *testing.T) {
		// Arrange
		cfg := mongopkg.Config{URI: "postgres://nope", Database: "x", ConnectTimeout: time.Second}

		// Act
		_, _, err := mongopkg.Connect(context.Background(), cfg)

		// Assert
		assert.Contains(t, err.Error(), "mongo connect")
	})

	t.Run("Disconnect tolerates a nil client", func(t *testing.T) {
		// Arrange
		var client *mongopkg.Client

		// Act
		err := mongopkg.Disconnect(context.Background(), client)

		// Assert
		assert.NoErr(t, err)
	})
}

func TestTransactions(t *testing.T) {
	t.Run("detects a replica set", func(t *testing.T) {
		// Arrange
		client, _ := connect(t)

		// Act
		ok, err := mongopkg.SupportsTransactions(context.Background(), client)

		// Assert
		assert.NoErr(t, err)
		assert.True(t, ok, "the test MongoDB must run as a replica set (see README)")
	})

	t.Run("reports a failed hello", func(t *testing.T) {
		// Arrange
		client, _ := connect(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		// Act
		_, err := mongopkg.SupportsTransactions(ctx, client)

		// Assert
		assert.Contains(t, err.Error(), "mongo hello")
	})

	t.Run("commits every write of fn when it succeeds", func(t *testing.T) {
		// Arrange
		client, db := connect(t)
		coll := db.Collection("tx_commit")
		tx := mongopkg.NewTxRunner(client)

		// Act
		err := tx.InTx(context.Background(), func(ctx context.Context) error {
			if _, err := coll.InsertOne(ctx, bson.D{{Key: "n", Value: 1}}); err != nil {
				return err
			}
			_, err := coll.InsertOne(ctx, bson.D{{Key: "n", Value: 2}})
			return err
		})

		// Assert
		assert.NoErr(t, err)
		n, _ := coll.CountDocuments(context.Background(), bson.D{})
		assert.Equal(t, n, int64(2))
	})

	t.Run("rolls back every write and returns fn's error unchanged", func(t *testing.T) {
		// Arrange
		client, db := connect(t)
		coll := db.Collection("tx_abort")
		_, _ = coll.InsertOne(context.Background(), bson.D{{Key: "seed", Value: true}}) // collection must exist before a tx write
		tx := mongopkg.NewTxRunner(client)
		sentinel := errors.New("handler failed")

		// Act
		err := tx.InTx(context.Background(), func(ctx context.Context) error {
			_, _ = coll.InsertOne(ctx, bson.D{{Key: "n", Value: 1}})
			return sentinel
		})

		// Assert
		assert.ErrIs(t, err, sentinel)
		n, _ := coll.CountDocuments(context.Background(), bson.D{{Key: "n", Value: 1}})
		assert.Equal(t, n, int64(0))
	})
}
