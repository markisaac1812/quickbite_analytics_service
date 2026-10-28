# Backfill: `cmd/backfill-aggs`

Rebuilds or catches up every aggregate by replaying **exported events** through the same `coreevents.Dispatcher` the live consumer uses. That means the same dedupe, the same transactions, and the same handlers. No broker is involved.

```bash
go run ./cmd/backfill-aggs -input events.ndjson -dry-run          # parse + count, write nothing
go run ./cmd/backfill-aggs -input events.ndjson                   # catch-up: deduped against event_ids
go run ./cmd/backfill-aggs -input events.ndjson -reset -yes       # full rebuild from scratch
cat a.ndjson b.ndjson | go run ./cmd/backfill-aggs -input -       # stdin
```

| Flag | Meaning |
| --- | --- |
| `-input` | NDJSON file, one envelope per line (`-` = stdin). Required. |
| `-reset` + `-yes` | delete every document in `agg_*`, `order_timelines`, `event_ids` first (indexes kept). `-reset` alone is refused. |
| `-dry-run` | parse and count per event type; no writes |
| `-stop-on-error` | abort at the first bad line (default: log it, continue, exit 1 at the end) |
| `-log-level` | default `warn`; `info` logs every event |

The exit code is 0 only if every line was applied, skipped as a duplicate, or had no handler.

## Input format

One JSON envelope per line, exactly what order-service's outbox drainer publishes:

```json
{"eventId":"…","eventType":"order.placed","occurredAt":"2026-10-15T10:00:01Z","aggregateType":"order","aggregateId":"…","region":"eg","payload":{…}}
```

### Exporting from order-service (per region shard)

`events_outbox` keeps every event with its original `event_id`, so a replay dedupes correctly against whatever the live consumer already applied:

```sql
-- psql against each region's hot DB (eg, ksa, …); append the outputs together
\copy (
  SELECT json_build_object(
           'eventId',       event_id,
           'eventType',     event_type,
           'occurredAt',    to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
           'aggregateType', aggregate_type,
           'aggregateId',   aggregate_id,
           'payload',       payload)
  FROM events_outbox
  WHERE event_type IN ('order.placed','order.delivered','order.rejected','payment.completed')
  ORDER BY id
) TO 'events-eg.ndjson'
```

Hand-made test files: `go run ./play/publish-test -print … >> events.ndjson`.

## Why line order doesn't matter

- Every counter is a commutative `$inc`.
- Two-event metrics (delivery time, rejections) are settled through `order_timelines` by whichever event completes the pair.
- So shards can be concatenated in any order, and lines can be shuffled. `play/e2e` proves it: a rebuild from the **reversed** file equals the live state.

## Which mode to use

| Situation | Mode |
| --- | --- |
| Consumer was down / events were dead-lettered within the last 7 days | catch-up (no `-reset`). Already-applied events hit `event_ids` and are skipped. |
| Events older than the 7-day `event_ids` TTL, a new aggregate, or a bug fix in a handler | rebuild (`-reset -yes`) from the **full** history. Catch-up would double-count events whose markers expired. |
| Not sure what a file contains | `-dry-run` first |

Rebuild safety:

- **Stop the API's order-events consumer, then export, then rebuild, then restart it.** Messages published meanwhile wait in the durable queue and are applied after the restart. If you rebuild while the consumer runs:
  - An event in the file that the consumer applied *before* the reset is wiped and re-applied once from the file. ✓
  - An event in the file that the consumer applies *after* the reset is deduped when its line is replayed. ✓
  - An event **newer than your export** that the consumer applied *before* the reset is wiped and isn't in the file, so it's **lost**. ✗
- With a replica set, each line is its own transaction. A crash mid-rebuild leaves a consistent prefix, and re-running the same command (without `-reset`) finishes the job, because applied lines are deduped.
