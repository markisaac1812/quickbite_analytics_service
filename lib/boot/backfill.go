package boot

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/quickbite/analytics-service/app/analytics/repository"
	"github.com/quickbite/analytics-service/lib/config"
	"github.com/quickbite/analytics-service/lib/coreevents"
	"github.com/quickbite/analytics-service/lib/logger"
	mongopkg "github.com/quickbite/analytics-service/pkg/mongo"
)

// RunBackfill rebuilds (or catches up) the aggregates by replaying event
// envelopes from an NDJSON file through the SAME dispatcher the live consumer
// uses: same dedupe, same transactions, same handlers. No broker involved.
//
// Input: one envelope per line, exactly what order-service's outbox drainer
// publishes ({eventId, eventType, occurredAt, payload, ...}). See
// docs/backfill.md for the export query against order-service's events_outbox.
//
// Order of lines does not matter: every metric is a commutative $inc, and
// two-event metrics are settled by the order timeline whichever event comes first.
func RunBackfill(opts BackfillOptions) error {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "error", err.Error())
		return err
	}
	level := cfg.LogLevel
	if opts.LogLevel != "" {
		level = opts.LogLevel
	}
	log := logger.New(level, cfg.AppEnv).With("component", "backfill-aggs")
	slog.SetDefault(log)

	if err := runBackfill(cfg, opts, log); err != nil {
		log.Error("backfill failed", "error", err.Error())
		return err
	}
	return nil
}

func runBackfill(cfg config.Config, opts BackfillOptions, log *slog.Logger) error {
	if opts.Reset && !opts.Yes {
		return errors.New("refusing to -reset without -yes (it deletes every aggregate, timeline and event_ids document)")
	}

	in, closeIn, err := openInput(opts.Input)
	if err != nil {
		return err
	}
	defer closeIn()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	connCtx, cancel := context.WithTimeout(ctx, cfg.Mongo.ConnectTimeout+5*time.Second)
	defer cancel()
	client, db, err := mongopkg.Connect(connCtx, mongopkg.Config{
		URI: cfg.Mongo.URI, Database: cfg.Mongo.Database,
		ConnectTimeout: cfg.Mongo.ConnectTimeout, MaxPoolSize: cfg.Mongo.MaxPoolSize, AppName: "analytics-backfill",
	})
	if err != nil {
		return err
	}
	defer mongopkg.Disconnect(context.Background(), client)
	log.Info("mongo connected", "database", cfg.Mongo.Database)

	if err := repository.EnsureIndexes(connCtx, db); err != nil {
		return err
	}
	tx, err := txRunner(connCtx, cfg.Mongo.Transactions, client, log)
	if err != nil {
		return err
	}

	if opts.Reset && !opts.DryRun {
		deleted, err := repository.ResetAll(ctx, db)
		if err != nil {
			return err
		}
		log.Warn("reset: collections emptied", "deleted", deleted)
	}

	_, dispatcher, err := analyticsModule(cfg, db, tx, log)
	if err != nil {
		return err
	}

	counts := map[string]int{}
	byType := map[string]int{}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // an envelope with many items can exceed 64KB
	start := time.Now()

	for line := 1; sc.Scan(); line++ {
		if ctx.Err() != nil {
			return fmt.Errorf("interrupted at line %d: %w", line, ctx.Err())
		}
		raw := sc.Bytes()
		if len(raw) == 0 {
			continue
		}
		env, err := coreevents.Decode(raw)
		if err != nil {
			counts["malformed"]++
			log.Error("backfill: malformed line", "line", line, "error", err.Error())
			if opts.StopOnError {
				return fmt.Errorf("line %d: %w", line, err)
			}
			continue
		}
		byType[env.EventType]++
		if opts.DryRun {
			counts["parsed"]++
			continue
		}

		outcome, err := dispatcher.Apply(ctx, env)
		counts[string(outcome)]++
		if err != nil {
			log.Error("backfill: event failed", "line", line, "event_id", env.EventID, "error", err.Error())
			if opts.StopOnError {
				return fmt.Errorf("line %d (%s): %w", line, env.EventID, err)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read input: %w", err)
	}

	log.Info("backfill finished",
		"dry_run", opts.DryRun, "transactional", dispatcher.Transactional(),
		"outcomes", counts, "event_types", byType, "duration_ms", time.Since(start).Milliseconds())
	if n := counts[string(coreevents.OutcomeFailed)] + counts["malformed"]; n > 0 {
		return fmt.Errorf("%d line(s) failed; see logs", n)
	}
	return nil
}

func openInput(path string) (io.Reader, func(), error) {
	if path == "" {
		return nil, nil, errors.New("-input is required (a file path, or - for stdin)")
	}
	if path == "-" {
		return os.Stdin, func() {}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open input: %w", err)
	}
	return f, func() { _ = f.Close() }, nil
}
