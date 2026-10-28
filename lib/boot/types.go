package boot

import "context"

// healthDeps are the probes /health reports on. Closures keep boot's router
// free of driver types: it never sees *mongo.Client or the broker.
type healthDeps struct {
	pingMongo       func(ctx context.Context) error
	rabbitConnected func() bool
}

// healthResponse is the /health payload — a response DTO like any other.
type healthResponse struct {
	Status string `json:"status"`
	Mongo  string `json:"mongo"`
	Rabbit string `json:"rabbit"`
}

// BackfillOptions are cmd/backfill-aggs' flags.
type BackfillOptions struct {
	Input       string // NDJSON file of event envelopes, "-" = stdin
	Reset       bool   // wipe aggregates, timelines and event_ids first (full rebuild)
	Yes         bool   // required together with Reset
	DryRun      bool   // parse and count only, write nothing
	StopOnError bool   // abort at the first failed event instead of continuing
	LogLevel    string // overrides LOG_LEVEL (backfills are noisy at info)
}
