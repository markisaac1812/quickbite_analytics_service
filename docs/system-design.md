# System design: analytics-service

## 1. Where it sits

```text
                              ┌──────────────────────────────────────────────┐
   browser / dashboard ──────▶│                 API gateway                  │
   (cookie access_token)      └───────┬──────────────┬───────────────┬───────┘
                                      ▼              ▼               ▼
                          ┌────────────────┐ ┌───────────────┐ ┌────────────────────┐
                          │  core-service  │ │ order-service │ │ analytics-service  │
                          │  Node/TS       │ │ Node/TS       │ │ Go                 │
                          │  users, rest., │ │ orders, pay-  │ │ per-day rollups    │
                          │  products, RBAC│ │ ments, deliv. │ │ (read-only API)    │
                          └──┬────┬────────┘ └──┬────────┬───┘ └─┬────▲────────▲──┬─┘
                    Postgres │    │ outbox      │ sharded│outbox │    │        │  │ HTTP (sync)
                             ▼    │             ▼  PG    │       ▼    │        │  │ GET /api/internal/
                          ┌─────┐ │          ┌──────┐    │  ┌─────────┐        │  │ rbac/permissions?role=
                          │ PG  │ │          │PG × N│    │  │ MongoDB │        │  │ (api-key)
                          └─────┘ │          └──────┘    │  │ (replica│        │  │
                                  │ core.events           │  │  set)   │        │  │
                                  ▼                      ▼  └─────────┘        │  │
                    ┌────────────────────────────────────────────────────────────┐│
                    │ RabbitMQ                                                    ││
                    │  core.events  ──rbac.#──────▶ analytics-service.core-events ┼┘  ← cache invalidation
                    │  order.events ──order.#, payment.#──▶ analytics-service.     │
                    │                                       order-events ─────────┼──▶ aggregates
                    │  nack → analytics-service.{order,core}-events.dlx → .dlq    │
                    └─────────────────────────────────────────────────────────────┘
        core-service ◀─────────────────────────────── (sync RBAC lookups) ──────────
```

| Service | Owns | Analytics' relationship |
| --- | --- | --- |
| core-service | users, restaurants, branches, products, **RBAC catalog**, JWT issuance | sync HTTP for permissions; seeds `analytics:read`; async `rbac.permissions_changed`; shares `ACCESS_SECRET` + internal key |
| order-service | orders, payments, deliveries, finance | async `order.events` via its transactional outbox |
| analytics-service | `agg_*_day`, `order_timelines`, `event_ids` | — |

## 2. Async flow: event → aggregates

```text
order-service: BEGIN; mutate order; INSERT events_outbox(...); COMMIT
outbox drainer (every 2s, FOR UPDATE SKIP LOCKED) → publish + confirm → mark dispatched
        │
        ▼ order.events / <routing key = eventType>
analytics-service worker goroutine (prefetch 32)
  coreevents.Consumer.handle   Decode envelope ─── malformed → nack → DLQ
  coreevents.Dispatcher.Apply  handler for eventType? ─── no → log, ack
     ┌──────────────── one Mongo transaction (replica set) ──────────────────┐
     │ event_ids.insertOne(eventId)  ── E11000 → abort → ack (duplicate)     │
     │ handler → service.OnXxx:                                              │
     │   order.placed      $inc restaurant/branch/platform day               │
     │                     BulkWrite product days (1 round trip)             │
     │                     timeline $set placed_*                            │
     │                     settle(): claim delivery / rejection if waiting   │
     │   order.delivered   timeline $set delivered_at → settle()             │
     │   order.rejected    timeline $set rejected_at  → settle()             │
     │   payment.completed $inc restaurant/branch/platform day of capture    │
     └── commit → ack            error → abort (nothing persisted) → DLQ ───┘
```

Delivery guarantees:
- **Producer → RabbitMQ: at-least-once.** The outbox write is transactional with the order; the drainer retries until the broker confirms, re-publishing with the **same `eventId`**.
- **RabbitMQ → analytics: at-least-once.** The ack happens after the commit.
- **At the aggregates: exactly-once with a replica set.** The dedupe insert and every write share one transaction, so either everything commits (and the eventId is recorded) or nothing does. On a standalone mongod (`MONGO_TRANSACTIONS=auto` falls back, or `off`), see §5 rows 6 and 7.

## 3. Out-of-order events: the order timeline

Events of one order are **not** guaranteed to arrive in order:

- `payment.completed` and the online `order.placed` are written in the **same** outbox transaction, payment first (lower `id`), so payment usually arrives **first**.
- Multiple consumers process in parallel; a redelivered or DLQ-replayed `order.placed` can land after its `order.delivered`.
- A backfill file can be in any order (shards concatenated).

Per-event metrics don't care. `order.placed` → orders/revenue and `payment.completed` → payments are self-contained `$inc`s, which are commutative.

Two-event metrics do care. `order.delivered` carries `deliveredAt` but **no `placedAt`**, and a rejection must count against the day the order was placed. These go through `order_timelines`:

```text
order_timelines { order_id, restaurant_id, branch_id, currency, total,
                  placed_at, placed_date, delivered_at, rejected_at,
                  delivery_applied, rejection_applied, updated_at }   TTL 30 days

each event:  upsert its own fact ($set — idempotent)
then settle: findOneAndUpdate({order_id, placed_at∃, delivered_at∃, delivery_applied≠true},
                              {$set: {delivery_applied: true}})  → doc? apply Δ on placed_date
             same for rejection
```

| Arrival order | What happens |
| --- | --- |
| placed → delivered | delivered completes the pair, claims, applies `deliveredAt − placedAt` |
| **delivered → placed** | delivered records the fact; its claim finds nothing. placed records its facts, claims, applies |
| placed ∥ delivered on two workers | each records, then claims. The claim is one atomic update, so exactly one wins. In tx mode the two transactions write the same timeline doc; one gets `WriteConflict`, retries, and then sees both facts |
| delivered twice (same or different eventId) | the second claim finds `delivery_applied: true`, so nothing happens |

## 4. Sync flow: read request

```text
client ─GET /api/v1/analytics/restaurants/42/failures?from=…&to=…─▶
  Correlation → AccessLog → Recoverer
  Authenticate      cookie | Bearer → HS256 (ACCESS_SECRET) → ctx Claims                         ✗ 401
  Require(analytics:read)   admin → pass; restaurant_user → rbac.Cache.Has(restaurantRole)        ✗ 403 / 503
  RequireRestaurantAccess   JWT restaurantId == path                                               ✗ 403
  (branch route) RequireBranchAccess   owner/admin pass; else branchId ∈ JWT branchIds             ✗ 403
  (platform routes) RequireRole(system_admin)                                                      ✗ 403
  controller: DTO ← path+query; validation.Struct                                                  ✗ 400
  service: range rules (from ≤ to, ≤ 366 d) → repo (one indexed Find or $match→$group→$limit)
           → derive avg / rate / totals → DTO mapper → 200
```

## 5. Failure modes

| # | Failure | What happens | Data effect | Recovery |
| --- | --- | --- | --- | --- |
| 1 | Duplicate delivery (outbox re-publish, redelivery) | dedupe insert E11000 → ack-skip | none | automatic |
| 2 | Malformed envelope | nack → DLQ | none | fix the producer; replay from the DLQ |
| 3 | Unknown event type (`order.accepted`, `payment.failed`, …) | logged, acked, no marker | none | add a handler; backfill for history |
| 4 | Invalid payload | `ANALYTICS_INVALID_EVENT_PAYLOAD` → rollback/Forget → DLQ | none | fix the producer; replay |
| 5 | Mongo down while consuming | insert fails → DLQ (tx aborted / marker forgotten) | none | replay the DLQ, or catch-up backfill |
| 6 | **Crash mid-apply, standalone mongod** | marker kept, some writes done → redelivery skipped | **partial event lost** | use a replica set; or rebuild with backfill |
| 7 | **Write error mid-apply, standalone mongod** | `Forget` → DLQ → replay re-applies the earlier writes | **partial double count** | use a replica set; or rebuild |
| 6′/7′ | Same, **replica set** | transaction aborts, nothing persisted → redelivery or replay applies once | none | automatic / replay |
| 8 | Events of one order out of order | timeline + claims | none | automatic (§3) |
| 9 | Same order delivered twice (new eventId) | second claim finds the flag set | none | automatic |
| 10 | `deliveredAt < placedAt` (producer clock skew) | claim taken, metric skipped, warn logged | that one delivery isn't averaged | investigate the producer clocks |
| 11 | Counterpart event > 30 days late | timeline expired → the late event recreates a half timeline that never completes | that metric is missing | rebuild with backfill |
| 12 | Replay of an event older than the 7-day `event_ids` TTL | treated as new | over-count | use rebuild (`-reset`), never catch-up, for old history |
| 13 | RabbitMQ restart | reconnect with backoff; both consumers re-declared; `/health` shows rabbit down meanwhile | none (unacked messages are requeued) | automatic |
| 14 | RabbitMQ / Mongo down at boot | retries, then exit 1 | none | orchestrator restarts |
| 15 | Queue redeclared with different args (e.g. a new DLX) | `PRECONDITION_FAILED`, boot fails | none | `rabbitmqctl delete_queue …`, reboot |
| 16 | core-service down | cached roles keep working; misses → 503 (fail closed) | none | automatic |
| 17 | core 4xx (bad internal key) | 502 `UPSTREAM_ERROR` | none | fix the shared key in `.env` |
| 18 | Permissions changed in core | stale ≤ `RBAC_CACHE_TTL` (core doesn't emit `rbac.permissions_changed` yet) | none | automatic on TTL / event |
| 19 | Hot platform row under transactions | `WriteConflict` → driver retries the transaction | latency only | bucket the row if it matters (CLAUDE.md §9) |
| 20 | Handler panic | recovered → nack → DLQ | none (tx aborted) | fix, replay |
| 21 | SIGTERM during processing | HTTP drains; consumers cancelled; in-flight handlers finish and ack | none | automatic |

## 6. Why dedupe in Mongo (`event_ids`), not Redis `SETNX`

order-service dedupes core events with Redis `SETNX` (24h). That's right **for its handlers**, which are idempotent cache invalidations. Analytics handlers `$inc` counters, which are **not** idempotent:

| | order-service (core events) | analytics-service (order events) |
| --- | --- | --- |
| Handler effect | cache invalidation (idempotent) | `$inc` counters (not idempotent) |
| Missed dedupe costs | an extra cache delete | wrong revenue numbers, permanently |
| Losing the dedupe store costs | nothing | every redelivery in the window double-counts |

1. **Same durability as the data it protects.** A Redis flush, eviction or failover would erase markers while the counters they guard survive.
2. **One transaction with the writes.** Only a marker in the same database can commit atomically with the `$inc`s. That's what makes failure modes 6 and 7 disappear on a replica set. Redis plus Mongo can never be atomic together.
3. **The same primitive.** A unique index plus `InsertOne` is the Mongo `SETNX`; a TTL index is `EX`.
4. **One fewer dependency** to run, configure and health-check.

The flip side: the `rbac.permissions_changed` queue uses **`NoDedupe`**. Its handler is idempotent, so a database write per event would be pure cost. That's the same reasoning order-service applies, pointed the other way.

## 7. Data model

```js
// agg_restaurant_day / agg_branch_day / agg_platform_day: same counter block
{ restaurant_id: 42, date: "2026-10-15", currency: "EGP",            // branch: + branch_id; platform: key (date, currency)
  orders_count: 6, revenue_sum: 14200,
  delivery_ms_sum: 3600000, delivery_ms_count: 2,
  rejected_count: 2, rejected_revenue_sum: 2500,
  payments_count: 1, payments_sum: 4200, updated_at: ISODate(…) }

// agg_product_day
{ product_id: 2, restaurant_id: 42, date: "2026-10-15", currency: "EGP",
  units_sold: 5, revenue_sum: 5700, orders_count: 2, updated_at: ISODate(…) }

// order_timelines (TTL 30d) and event_ids (TTL 7d): see §3 / §6
```

**Sum and count, never averages or rates:** they're associative and commutative, so rows merge with `$inc` in any order, and a rebuild from any permutation of the history gives the same result (`play/e2e` checks a reversed rebuild). Averages and rates are derived on read.

**`"YYYY-MM-DD"` strings:** an unambiguous UTC day, and lexicographic order equals chronological order, so string ranges are date ranges served by the same index.

**Currency in the platform key:** EGP and SAR revenue must never be summed. Every restaurant has one currency, so restaurant, branch and product rows carry it as a plain field.

## 8. Backfill

`cmd/backfill-aggs` replays an NDJSON export of order-service's `events_outbox` through the same `Dispatcher` (dedupe, transactions, handlers) without a broker. There are two modes: catch-up (deduped) and rebuild (`-reset -yes`). See `docs/backfill.md`.

## 9. Scaling notes

- **Horizontal:** N replicas compete on each queue. `$inc` upserts, unique indexes, and atomic claims keep concurrent writes correct.
- **Per process:** `RABBITMQ_PREFETCH` workers. The Mongo pool must be at least prefetch plus HTTP concurrency.
- **Hot rows:** platform day (every event) and very busy restaurants. Plain `$inc` serializes cheaply; transactions add `WriteConflict` retries. The fix is counter buckets.
- **Reads:** at most 366 small docs, or one `$group` over an index range. Add HTTP caching only if dashboards poll aggressively.
