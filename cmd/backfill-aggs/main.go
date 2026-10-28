// Command backfill-aggs rebuilds or catches up the analytics aggregates by
// replaying exported order-service events (NDJSON) through the same
// dispatcher the live consumer uses. Wiring lives in lib/boot.
//
//	go run ./cmd/backfill-aggs -input events.ndjson                 # catch-up (deduped)
//	go run ./cmd/backfill-aggs -input events.ndjson -reset -yes     # full rebuild
//	go run ./cmd/backfill-aggs -input events.ndjson -dry-run        # parse + count only
//
// See docs/backfill.md.
package main

import (
	"flag"
	"os"

	"github.com/quickbite/analytics-service/lib/boot"
)

func main() {
	var opts boot.BackfillOptions
	flag.StringVar(&opts.Input, "input", "", "NDJSON file of event envelopes (- for stdin)")
	flag.BoolVar(&opts.Reset, "reset", false, "delete all aggregates, timelines and event_ids before replaying (needs -yes)")
	flag.BoolVar(&opts.Yes, "yes", false, "confirm -reset")
	flag.BoolVar(&opts.DryRun, "dry-run", false, "parse and count only; write nothing")
	flag.BoolVar(&opts.StopOnError, "stop-on-error", false, "abort at the first failed line")
	flag.StringVar(&opts.LogLevel, "log-level", "warn", "log level for the run (info logs every event)")
	flag.Parse()

	if err := boot.RunBackfill(opts); err != nil {
		os.Exit(1)
	}
}
