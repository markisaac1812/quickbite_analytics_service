# CLAUDE.md — Analytics Service Guidelines

These rules apply to the **`analytics-service`** of the **QuickBite** platform. It is the Go sibling of `core-service` and `order-service` (both Node + TypeScript). Its architecture **mirrors** theirs on purpose: same layering, same envelopes, same auth contract, same event envelope. When in doubt, read how `order-service` does it and find the Go analogue. Deviate only where this file says so.

> Grep tip: every rule has a stable heading. `grep -n "^## \|^### " CLAUDE.md` gives you the index.

---

## 1. Mission

Owns **per-day rollups** of order / payment / delivery activity.

- **Consumes** `order.placed`, `order.delivered`, `order.rejected`, `payment.completed` from `order-service` (`order.events` exchange) and `rbac.permissions_changed` from `core-service` (`core.events`).
- **Upserts** day-grained aggregates into MongoDB: `agg_restaurant_day`, `agg_branch_day`, `agg_product_day`, `agg_platform_day` (+ the `order_timelines` helper and `event_ids` dedupe).
- **Serves** read-only HTTP endpoints under `/api/v1/analytics/...` (9 endpoints, see `docs/api-contracts.md`).
- **Rebuilds** aggregates from exported events with `cmd/backfill-aggs`.

It does **not** own users, restaurants, products, orders or payments. It does **not** write to any operational store. It does **not** emit events. It does **not** have its own RBAC catalog (`analytics:read` is seeded in **core-service**).

---

## 2. Tech stack (locked — do not deviate)

| Concern | Choice |
| --- | --- |
| Runtime | Go (code needs ≥ 1.22; `go.mod` says 1.26 because current `x/crypto` / `validator` require it) |
| HTTP router | `github.com/go-chi/chi/v5` (+ its `middleware` subpackage) |
| DB | `go.mongodb.org/mongo-driver` **v1** (official) |
| Messaging | `github.com/rabbitmq/amqp091-go` |
| Config | `github.com/caarlos0/env/v11` (struct tags) |
| Logger | stdlib `log/slog` (JSON handler) |
| Validation | `github.com/go-playground/validator/v10` |
| JWT | `github.com/golang-jwt/jwt/v5` |
| UUID | `github.com/google/uuid` |
| DI | **None.** Explicit constructor wiring in `lib/boot` |

**Forbidden:** GORM, Ent, mgm, any ODM or ORM. Repositories use the official driver with typed structs + `bson` tags (the Go analogue of "Knex, not an ORM").
**Forbidden:** DDL migrations. Mongo is schemaless; indexes are declared in code and ensured idempotently on boot.
**Forbidden:** new libraries without a written justification in this file. (No godotenv — `lib/config/dotenv.go` is 40 lines. No CORS/helmet — no browser calls this service directly yet. No Postgres driver — backfill reads an NDJSON export instead of order-service's DB.)

---

## 3. Folder structure

```text
analytics-service/
├── cmd/
│   ├── api/main.go                 # ~10 lines: boot.Run()
│   └── backfill-aggs/main.go       # flags → boot.RunBackfill(opts)
├── pkg/                            # framework-free, app-agnostic, no env, no globals
│   ├── mongo/{client.go,tx.go}     # Connect/Ping/Disconnect, SupportsTransactions, TxRunner — NO collection names
│   ├── messaging/{types.go,amqp.go}# Broker interface + amqp091 impl (confirms, reconnect, DLQ)
│   └── httpclient/client.go        # net/http: base URL, timeout, JSON, retry on 5xx/network
├── lib/                            # app-aware glue (env, middleware, cross-cutting infra)
│   ├── boot/{boot.go,wiring.go,backfill.go,router.go,types.go}  # composition root; the ONLY lib pkg importing app/
│   ├── config/{env.go,dotenv.go}
│   ├── logger/logger.go
│   ├── appcontext/context.go       # ctx keys: Claims, correlation id  (≈ express.d.ts)
│   ├── errors/{apperror.go,handler.go}    # package apperr
│   ├── http/{response.go,types.go} # package httpx
│   ├── middleware/correlation.go
│   ├── validation/validate.go
│   ├── auth/{jwt.go,middleware.go,apikey.go,types.go}
│   ├── rbac/{cache.go,middleware.go,events.go,types.go}  # cache, Require/RequireRole/RequireRestaurantAccess/RequireBranchAccess, permissions_changed handler
│   ├── coreclient/{client.go,rbac.go,types.go}
│   └── coreevents/{dispatcher.go,consumer.go,dedupe.go,payloads.go}  # Dispatcher (route→dedupe→tx→handler), Consumer (broker binding), NoDedupe
├── app/analytics/                  # package analytics — module-shared types/errors/enums
│   ├── types.go  errors.go  enums.go
│   ├── entity/                     # *.entity.go — plain structs + bson tags, no driver imports
│   ├── repository/                 # ONLY place mongo-driver is used (+ indexes.go, counters.go, maintenance.go)
│   ├── service/{analytics,ingest,reports}.service.go
│   ├── controller/{analytics,platform}.controller.go + routes.go
│   ├── dto/{analytics.request.go,analytics.response.go}
│   └── eventhandlers/handlers.go   # event type → service method table
├── play/                           # GITIGNORED dev aids: mock-core, mint-jwt, publish-test, check-mongo, e2e, internal/fixtures
└── docs/                           # see §12
```

Annotated version: `docs/folder-structure.md`.

**Package-name ≠ folder-name (deliberate, only two):** `lib/errors` is `package apperr`, `lib/http` is `package httpx` — they must not shadow stdlib `errors` / `net/http`. Always import them with the explicit alias.

---

## 4. Layering rules (enforced by review)

```text
app/  → may import lib, pkg
lib/  → may import pkg; may NOT import app/<module>/*   (exception: lib/boot, the composition root)
pkg/  → NO imports from lib or app, no env, no globals, NO app-specific knowledge
```

- **`pkg/` knows nothing about this app.** No collection, queue or permission names in `pkg/`. `pkg/mongo` re-exports `Client`/`Database` as type aliases so `lib/boot` can name them without importing the driver.
- **`lib/boot` is the composition root** (≈ `lib/di/container.ts`). Adding a module = edit `lib/boot/wiring.go` + `boot.go`. `cmd/*/main.go` never change.
- **Invert dependencies with small interfaces defined by the consumer:** `coreevents.EventDeduper` (← `repository.EventIDsRepo`), `coreevents.TxRunner` (← `pkg/mongo.TxRunner`), `rbac.PermissionFetcher` (← `coreclient.Client`), `analytics.*Store` (← repositories).
- **Module types live in the parent package** (`package analytics`) so `service`, `controller`, `eventhandlers`, `dto`, `repository` share one vocabulary without import cycles.

Verify mechanically:

```bash
M=github.com/quickbite/analytics-service
go list -f '{{.ImportPath}}: {{join .Imports " "}}' ./pkg/... | grep -E "$M/(lib|app)"                       # nothing
go list -f '{{.ImportPath}} {{join .Imports " "}}' ./lib/... | grep "$M/app" | cut -d' ' -f1                  # only lib/boot
go list -f '{{.ImportPath}} {{join .Imports " "}}' ./cmd/... ./app/... ./lib/... ./pkg/... | grep go.mongodb.org | cut -d' ' -f1  # repository + pkg/mongo
```

---

## 5. Naming

### Go

- Packages: short, lower-case, no underscores. Exported `PascalCase`, unexported `camelCase`, acronyms keep case (`RestaurantID`, `URL`).
- Constructors: `New` for a package's main type, `NewX` otherwise.
- Sentinel errors `ErrXxx`; error **codes** `UPPER_SNAKE`, module-prefixed (`ANALYTICS_INVALID_DATE_RANGE`).
- Permissions `Perm<Resource><Action>`; event consts `Event<Aggregate><PastVerb>` = routing key.

### Files

- Layer-suffixed like the Node side: `*.service.go`, `*.controller.go`, `*.repo.go`, `*.entity.go`, `*.request.go`, `*.response.go`. Multi-word stems use `_`, never `-`. A component may span several files of the same layer (`ingest.service.go` + `reports.service.go` are methods of one `Service`).

### MongoDB

- Collections `snake_case`. Aggregates `agg_<grain>_day`. Fields `snake_case` via `bson` tags; JSON on the wire stays `camelCase` via DTO `json` tags.
- Day keys: `date: "YYYY-MM-DD"`, **UTC**. Index names: `uq_<coll>_<f1>_<f2>`, `idx_…`, `ttl_<coll>_<field>`.

### RabbitMQ

- Queues: `<service>.<source>-events` (`analytics-service.order-events`, `analytics-service.core-events`).
- **DLX and DLQ are service-scoped:** `<queue>.dlx` → `<queue>.dlq`. Never reuse another service's DLX (e.g. `core.events.dlx`): its DLQ binds `#`, so our dead letters would be copied into it.

### Routes

- Prefix `/api/v1/analytics`. Restaurant scope under `/restaurants/{restaurantId}/…`, branch under `/restaurants/{restaurantId}/branches/{branchId}/…`, platform under `/platform/…`. `GET` only.

---

## 6. Response shape

- **Success:** `{"success": true, "data": <DTO | []DTO>}` via `httpx.SendSuccess`.
- **Error:** `{"success": false, "error": {"code", "message"}}` — rendered **only** by `apperr.Wrap` / `apperr.Write`.
- **Every response body is a Response DTO** from `dto/*.response.go` built by an `XxxFrom(...)` mapper. Never encode an entity or a service row.
- Lists render `[]`, never `null`. Reports are **sparse**: a day appears only if that report's metric has data.
- **Money** `int64` minor units + `currency`. **Durations** integer ms. **Rates** 0..1, 4 decimals (`failureRate`). **Dates** ISO-8601 UTC.
- **Derived values** (`avgOrderMinor`, `avgDeliveryMs`, `failureRate`) are computed in the **service**, never stored. Range averages are `Σsum / Σcount`, never an average of daily averages.

---

## 7. Database rules (MongoDB)

- **Driver only in `app/<module>/repository/`** (and `pkg/mongo`). Entities never import the driver.
- **No migrations.** `repository/indexes.go` declares every index with a comment naming the query it supports; `EnsureIndexes` runs on boot (and in backfill).
- **Explicit projections** (`dayCounterProjection` ≈ `<MODULE>_COLUMNS`).
- **Aggregates are associative and commutative.** Store `sum` + `count`; every write is an `$inc` upsert on the natural key; every `agg_*_day` row has the same counter block (`entity.DayCounters`, shared `$inc` builder in `repository/counters.go`). Event order never matters.
- **Day bucket rule:** order-scoped metrics (orders, revenue, delivery time, rejections) land on the UTC day the order was **placed**; payments on the day they were **captured**.
- **Two-event metrics go through `order_timelines`.** Each event `$set`s its fact; the metric is applied by an atomic **claim** (`findOneAndUpdate` flipping `delivery_applied` / `rejection_applied`) by whichever event completes the pair. Never compute a two-event metric from one event's payload.
- **Line items → one `BulkWrite`** (unordered, upserts), lines merged by product first. Never one round trip per line.
- **Transactions:** with a replica set (`MONGO_TRANSACTIONS=auto|on`), the dispatcher runs `MarkSeen` + every write of an event in **one transaction** → exactly-once. Repositories need nothing special: the session rides on `ctx`. Standalone (`off`/auto-fallback): dedupe + `Forget`-on-failure; a *partial* failure can double-count earlier writes on replay (documented trade-off).
- Repos are structs holding a `*mongo.Collection`, built once in `lib/boot/wiring.go`.

| Collection | Natural key | Indexes |
| --- | --- | --- |
| `agg_restaurant_day` | `(restaurant_id, date)` | `uq_…_restaurant_id_date`, `idx_…_date_restaurant_id` |
| `agg_branch_day` | `(branch_id, date)` (+ `restaurant_id`) | `uq_agg_branch_day_branch_id_date` |
| `agg_product_day` | `(product_id, date)` (+ `restaurant_id`) | `uq_…_product_id_date`, `idx_…_restaurant_id_date` |
| `agg_platform_day` | `(date, currency)` | `uq_agg_platform_day_date_currency` |
| `order_timelines` | `order_id` | `uq_order_timelines_order_id`, `ttl_…_updated_at` (30d) |
| `event_ids` | `event_id` | `uq_event_ids_event_id`, `ttl_event_ids_received_at` (7d) |

---

## 8. Cross-cutting infra

### Config & secrets

- Every env var is a field on `lib/config.Config` (`env:"…"` + default or `required,notEmpty`). Never `os.Getenv` elsewhere. `.env` is read if present; real env vars win.
- **Shared secrets must be identical across services:** `ACCESS_SECRET` (core = order = analytics) and the internal key (core `INTERNAL_API_KEY` = order/analytics `CORE_INTERNAL_API_KEY`). Local dev uses one generated strong value in all three `.env` files; production injects them from a secret store. `.env.example` holds placeholders only.

### Logging & correlation

- `log/slog` JSON to stdout; `logger.FromContext(ctx)` is already tagged `correlation_id` (request id, or the **eventId** for events).
- Boot milestones in order: `mongo connected` → `mongo indexes ensured` → `rabbit connected` → `mongo transactions enabled|…standalone…` → `event consumer started` (×2) → `http listening`.

### Auth

- JWT HS256, `ACCESS_SECRET`, `exp` required. Claims `userId, role, email?, restaurantId?, restaurantRole?, branchIds?`. Cookie `access_token` first, then `Bearer`. Any failure → 401 `UNAUTHENTICATED`.
- Internal routes (none yet) use `auth.RequireInternalAPIKey`.

### RBAC

- **No local catalog.** `analytics:read` is seeded by core migration `20261028180000_seed_analytics_service_permissions.ts` (owner, branch_manager). Same pattern as order-service's `20261022173627_seed_order_service_permissions.ts`.
- `rbac.Cache`: per-`restaurantRole` read-through to core `GET /api/internal/rbac/permissions?role=`, TTL `RBAC_CACHE_TTL` (5m), fail closed (503), errors not cached.
- Guards: `Require(perm)` (system_admin bypass), `RequireRestaurantAccess("restaurantId")`, `RequireBranchAccess("branchId")` (owner/admin bypass; others need the id in `branchIds`; mount after restaurant access), `RequireRole(...)` (platform endpoints: `system_admin`).
- **`rbac.permissions_changed`** (core.events) → `Cache.Invalidate(role)`; empty role = all. ⚠️ core declares the event but has no code path that publishes it yet (permissions only change via migrations), so today the TTL is what bounds staleness.

### Errors & validation

- Services/handlers/controllers **return** `error`. Module errors are package-level `var ErrXxx = apperr.New(...)`; detail via `.WithMessage/.WithCause` (copies). Match with `errors.Is`.
- Request DTOs carry `validate` tags; controllers copy path + query in and call `validation.Struct` → 400 `VALIDATION_ERROR`. Business rules (range order, 366-day cap) live in the service.

### Messaging (inbound only)

| Queue | Exchange | Bindings | DLX → DLQ | Dedupe | Tx |
| --- | --- | --- | --- | --- | --- |
| `analytics-service.order-events` | `order.events` | `order.#`, `payment.#` | `…order-events.dlx` → `…order-events.dlq` | Mongo `event_ids` | yes (replica set) |
| `analytics-service.core-events` | `core.events` | `rbac.#` | `…core-events.dlx` → `…core-events.dlq` | `NoDedupe` (handler idempotent) | no |

- Pipeline (`coreevents.Dispatcher.Apply`): no handler → log + ack · `MarkSeen` dup → ack-skip · handler ok → ack · failure → rollback (tx) or `Forget` (no tx) → nack → DLQ. Malformed envelope → DLQ.
- The **same Dispatcher** serves the live consumer and `cmd/backfill-aggs` — one code path.
- Prefetch = worker goroutines per queue; handlers must be concurrency-safe (they are: `$inc`, atomic claims).
- Queue arguments are immutable: changing a DLX means deleting the queue (`rabbitmqctl delete_queue …`) before the next boot, or it fails with `PRECONDITION_FAILED`.

### Graceful shutdown

SIGINT/SIGTERM → HTTP `Shutdown` → `broker.Close` (cancel both consumers, wait for in-flight handlers) → Mongo disconnect, bounded by `SHUTDOWN_TIMEOUT`.

---

## 9. Performance budget

1. Restaurant/branch reads: one indexed `Find` (≤ 366 small docs), p95 < 50 ms. Ranking reads: one `$match` on an index + `$group` + `$limit` (≤ 100).
2. Range cap 366 days on every endpoint; `limit` ≤ 100.
3. Event apply: `order.placed` = dedupe insert + 3 `$inc` upserts + 1 `BulkWrite` + 1 timeline upsert + 2 claims — constant, independent of item count. Others: ≤ 6 round trips.
4. Every query is index-backed; new query → new index in `indexes.go`, with a comment, in the same PR.
5. RBAC on the hot path is an in-memory lookup; core is hit ≤ once per role per TTL per process.
6. `MONGO_MAX_POOL_SIZE` ≥ `RABBITMQ_PREFETCH` + HTTP concurrency.
7. **Hot document:** `agg_platform_day` (date, currency) is touched by every event. Fine with plain `$inc`; with transactions concurrent workers hit `WriteConflict` and the driver retries. If that shows in latency, bucket the row (`restaurant_id % N`) and sum on read.

---

## 10. Code style — what to avoid

- ❌ ODMs/ORMs, or driver imports outside `repository/` and `pkg/mongo`.
- ❌ Collection/queue/permission names in `pkg/`.
- ❌ `type X struct{…}` in `*.service.go`, `*.controller.go`, `*.repo.go` (other than the component's own struct) — including anonymous result structs; put aggregation result shapes in `entity/aggregates.entity.go`.
- ❌ Encoding entities or service rows in responses; writing error responses from controllers/services.
- ❌ `apperr.New(...)` inline for a named case; mutating a sentinel.
- ❌ Storing averages or rates. Computing a two-event metric from one event. Per-line-item round trips.
- ❌ Reusing another service's DLX. Changing queue args without deleting the queue.
- ❌ Swallowing errors in handlers (`log; return nil` silently acks a lost event).
- ❌ `context.Background()` inside request/event paths — the tx session rides on `ctx`; dropping it silently escapes the transaction.
- ❌ Side effects other than Mongo writes inside an event handler (the tx may be retried).
- ❌ Floats for money; local time for day buckets.
- ❌ New env vars that aren't `config.Config` fields; secrets in `.env.example`.
- ❌ Dev scripts outside `play/`; production binaries inside `play/`.

---

## 11. Adding something

**A new aggregate:** entity (embed `DayCounters` if it's a day grain) → indexes (with query comments) → repo (+ `Store` interface in `types.go`, add to `Stores`) → service write in `ingest.service.go` → read in `reports.service.go` → DTO + controller + route → wire in `lib/boot/wiring.go` → extend `play/e2e`.

**A new event:** const in `enums.go` → payload struct in `types.go` → handler in `eventhandlers/handlers.go` → service method. Self-contained metric → `incDay`. Needs another event's data → record a fact on the timeline + a claim. Bindings rarely change.

**A new endpoint:** request DTO (+ `validate`) → response DTO + mapper → service method → controller method → `routes.go` with the right guard → `docs/api-contracts.md` → `play/e2e`.

Run `go run ./play/e2e` (service + mock-core running) before calling anything done.

---

## 12. Reference docs

- `README.md` — run it locally; E2E; backfill.
- `plan.md` — what's built vs. what's left.
- `docs/folder-structure.md` · `docs/system-design.md` · `docs/api-contracts.md` · `docs/backfill.md`
- `docs/node-to-go-mapping.md` — **start here if you know the Node services.**
- `docs/ai-prompts.md` · `docs/implementation-plan.md`

---

## 13. Out of scope / known gaps

- Handlers for `order.accepted`, `order.cancelled`, `payment.failed` (acked + logged today).
- core-service publishing `rbac.permissions_changed` (no permission-mutation path exists in core yet).
- Exactly-once on a **standalone** mongod (needs a replica set; see §7).
- DLQ replay tooling; an order-service internal export endpoint (backfill reads a `psql` export, see `docs/backfill.md`).
- Emitting events, writing to any operational DB, owning any business entity.
- CORS, rate limiting, OpenAPI generation, metrics/tracing, Docker Compose / deployment (later DevOps section).
- Go unit tests (the E2E suite in `play/e2e` is the acceptance gate today).
