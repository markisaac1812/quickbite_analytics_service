// Package mongo owns the MongoDB connection lifecycle and nothing else.
//
// It knows how to connect, ping and disconnect. It does NOT know which
// collections exist or which indexes they need — that is app knowledge and
// lives in app/<module>/repository/indexes.go.
package mongo

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

// Client and Database re-export the driver types so callers outside
// pkg/mongo and the repositories (e.g. lib/boot wiring) can name them without
// importing the driver themselves.
type (
	Client   = mongo.Client
	Database = mongo.Database
)

// Config is everything the driver needs to open a pool. Callers build it
// from their own env; this package never reads the environment.
type Config struct {
	URI            string
	Database       string
	ConnectTimeout time.Duration
	MaxPoolSize    uint64
	AppName        string
}

// Connect opens the pool, pings the primary, and returns the client plus a
// handle to the configured database. Fails fast if the server is unreachable.
func Connect(ctx context.Context, cfg Config) (*mongo.Client, *mongo.Database, error) {
	opts := options.Client().
		ApplyURI(cfg.URI).
		SetConnectTimeout(cfg.ConnectTimeout).
		SetServerSelectionTimeout(cfg.ConnectTimeout)
	if cfg.MaxPoolSize > 0 {
		opts.SetMaxPoolSize(cfg.MaxPoolSize)
	}
	if cfg.AppName != "" {
		opts.SetAppName(cfg.AppName)
	}

	client, err := mongo.Connect(ctx, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("mongo connect: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := client.Ping(pingCtx, readpref.Primary()); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, nil, fmt.Errorf("mongo ping: %w", err)
	}

	return client, client.Database(cfg.Database), nil
}

// Ping checks the primary is reachable. Used by the health endpoint.
func Ping(ctx context.Context, client *mongo.Client) error {
	return client.Ping(ctx, readpref.Primary())
}

// Disconnect drains the pool. Safe to call with a nil client.
func Disconnect(ctx context.Context, client *mongo.Client) error {
	if client == nil {
		return nil
	}
	return client.Disconnect(ctx)
}
