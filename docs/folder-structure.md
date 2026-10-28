# Folder structure (annotated)

Each directory has an equivalent in `order-service/src/`. The right-hand side names it.

```text
analytics-service/
│
├── cmd/
│   ├── api/main.go                    ≈ `node dist/server.js`. ~10 lines: boot.Run(), exit 1 on error.
│   └── backfill-aggs/main.go          flags → boot.RunBackfill(opts). Same "main stays tiny" rule.
│
├── pkg/                               ≈ src/pkg/ — reusable, app-agnostic, framework-free
│   ├── mongo/
│   │   ├── client.go                  Connect / Ping / Disconnect; Client & Database type aliases. No collection names.
│   │   └── tx.go                      SupportsTransactions (hello → setName), TxRunner.InTx (session.WithTransaction)
│   ├── messaging/
│   │   ├── types.go                   Broker interface, Message, Handler, Topology, Config
│   │   └── amqp.go                    amqp091 impl: publisher confirms, prefetch worker pool, DLX/DLQ assert,
│   │                                  reconnect loop that replays consumers  ≈ rabbitmq.client.ts (amqp-connection-manager)
│   └── httpclient/client.go           DoJSON: base URL, per-attempt timeout, retry+backoff on 5xx/network
│
├── lib/                               ≈ src/lib/ — app-aware glue
│   ├── boot/                          composition root (≈ server.ts + lib/di/container.ts); the ONLY lib pkg importing app/
│   │   ├── boot.go                    Run(): config → logger → Mongo → indexes → Rabbit → tx detection → core client/RBAC/auth
│   │   │                              → analytics module → 2 consumers → HTTP → wait → ordered shutdown
│   │   ├── wiring.go                  analyticsModule() (repos → service → dispatcher + handlers), txRunner() (auto|on|off).
│   │   │                              Shared by Run and RunBackfill so both apply events identically.
│   │   ├── backfill.go                RunBackfill(): NDJSON → Dispatcher.Apply, -reset/-dry-run, outcome counts
│   │   ├── router.go                  chi router, global middleware, 404/405, /health, /api/v1 mount  ≈ app.ts + routes.ts
│   │   └── types.go                   healthDeps, healthResponse, BackfillOptions
│   ├── config/{env.go,dotenv.go}      Config struct + Load()  ≈ env.ts (zod) + dotenv
│   ├── logger/logger.go               slog JSON; WithContext / FromContext  ≈ lib/logger/logger.ts
│   ├── appcontext/context.go          Claims + typed ctx keys  ≈ lib/types/express.d.ts
│   ├── errors/                        package apperr: AppError, sentinels, Wrap, Write, Recoverer  ≈ AppError.ts + errorHandler.ts
│   ├── http/                          package httpx: SendSuccess, SendPaginated, SendError  ≈ lib/http/response.ts
│   ├── middleware/correlation.go      Correlation + AccessLog  ≈ lib/correlation/correlationId.ts
│   ├── validation/validate.go         validator singleton → 400 VALIDATION_ERROR  ≈ lib/validation/validate.ts
│   ├── auth/                          jwt.go (Verifier), middleware.go (Authenticate), apikey.go, types.go  ≈ lib/auth/{jwt,guard,api-key}.ts
│   ├── rbac/
│   │   ├── cache.go                   per-role read-through cache, TTL, Invalidate  ≈ permission-cache.service.ts
│   │   ├── middleware.go              Guard: Require, RequireRole, RequireRestaurantAccess, RequireBranchAccess  ≈ lib/auth/rbac.ts
│   │   ├── events.go                  HandlePermissionsChanged (rbac.permissions_changed → Invalidate)
│   │   └── types.go                   role consts, PermissionFetcher, PermissionsChangedPayload
│   ├── coreclient/                    client.go, rbac.go, types.go  ≈ lib/core-client/
│   └── coreevents/
│       ├── dispatcher.go              Dispatcher: Register, Apply (route → dedupe → [tx] → handler), Decode
│       ├── consumer.go                Consumer: binds a Dispatcher to one queue  ≈ lib/core-events/consumer.ts
│       ├── dedupe.go                  NoDedupe (for idempotent queues)
│       └── payloads.go                Envelope, EventHandler, EventDeduper, TxRunner, Outcome  ≈ lib/core-events/types.ts
│
├── app/analytics/                     package analytics  ≈ src/app/<module>/
│   ├── types.go                       event payloads (placed/delivered/transition/payment), service inputs, read models,
│   │                                  DayDelta, *Store interfaces, Stores, TimelinePlaced  ≈ types.ts
│   ├── errors.go                      ErrInvalidDateRange, ErrDateRangeTooLarge, ErrInvalidEventPayload
│   ├── enums.go                       PermAnalyticsRead, Event* routing keys, DateLayout, MaxRangeDays, top limits
│   ├── entity/
│   │   ├── restaurant_day.entity.go   DayCounters (shared counter block) + RestaurantDay
│   │   ├── branch_day.entity.go       BranchDay
│   │   ├── product_day.entity.go      ProductDay
│   │   ├── platform_day.entity.go     PlatformDay (key: date + currency)
│   │   ├── order_timeline.entity.go   OrderTimeline (facts + *_applied claim flags)
│   │   ├── event_id.entity.go         EventID
│   │   └── aggregates.entity.go       aggregation result shapes (DateCount, RestaurantTotal, ProductTotal, facet)
│   ├── repository/                    ONLY place the mongo-driver is used for module data
│   │   ├── indexes.go                 collection names, EnsureIndexes (every index + the query it serves), TTLs
│   │   ├── counters.go                shared $inc update / projection / date-range builders for agg_*_day
│   │   ├── restaurant_day.repo.go     Increment, FindRange, ActiveRestaurants ($facet), TopRestaurants
│   │   ├── branch_day.repo.go         Increment, FindRange (tenant-filtered)
│   │   ├── product_day.repo.go        IncrementLines (one BulkWrite), TopProducts
│   │   ├── platform_day.repo.go       Increment, FindRange
│   │   ├── order_timeline.repo.go     RecordPlaced/Delivered/Rejected, ClaimDelivery/ClaimRejection (findOneAndUpdate)
│   │   ├── event_ids.repo.go          MarkSeen (insert, E11000 = dup), Forget
│   │   └── maintenance.go             ResetAll (backfill -reset)
│   ├── service/                       one Service, three files
│   │   ├── analytics.service.go       struct, New, incDay, range/avg/rate helpers
│   │   ├── ingest.service.go          OnOrderPlaced/Delivered/Rejected, OnPaymentCompleted, settle(), mergeLines
│   │   └── reports.service.go         ListRestaurantDays, RestaurantFailures/DeliveryAvg/Payments, TopProducts,
│   │                                  ListBranchDays, PlatformDays, ActiveRestaurants, TopRestaurants
│   ├── controller/
│   │   ├── analytics.controller.go    restaurant-scoped handlers + parse helpers
│   │   ├── platform.controller.go     system_admin handlers
│   │   └── routes.go                  Mount(): guards per route group  ≈ routes.ts
│   ├── dto/
│   │   ├── analytics.request.go       RestaurantRange, BranchDays, TopProducts, PlatformRange, TopRestaurants (validate tags)
│   │   └── analytics.response.go      one response DTO + XxxFrom mapper per endpoint
│   └── eventhandlers/handlers.go      Register(dispatcher, svc): eventType → decode → service  ≈ core-events.handlers.ts
│
├── play/                              GITIGNORED dev aids (≈ order-service/play/), compiled by `go build ./...`
│   ├── mock-core/                     fake core RBAC endpoint + GET /__calls counter
│   ├── mint-jwt/                      core-shaped access tokens (-role, -restaurant-role, -branch-ids, -ttl)
│   ├── publish-test/                  publish any event type (or -print NDJSON for backfill)
│   ├── check-mongo/                   dump every collection for a restaurant; -reset
│   ├── e2e/                           the 56-check acceptance suite
│   └── internal/fixtures/             envelope builders shared by publish-test and e2e
│
├── go.mod / go.sum · .env.example · .gitignore · CLAUDE.md · README.md · plan.md
└── docs/  folder-structure · system-design · api-contracts · backfill · node-to-go-mapping · ai-prompts · implementation-plan
```

## Why the non-obvious choices

**Both `cmd/*/main.go` are tiny.** Wiring lives in `lib/boot`; `wiring.go` is shared, so the API and the backfill can never drift apart in how they apply events.

**`play/`, not `cmd/`.** `cmd/` means "production binary": `api` and `backfill-aggs` qualify. Mocks and test drivers don't, and they're gitignored like order-service's `play/`.

**Types, errors, and enums sit in `app/analytics/`, not `service/`.** One vocabulary for every subpackage, with no import cycles. Go forbids cycles, so this layout is load-bearing.

**One `Service` across three files.** Ingest (writes) and reports (reads) are different concerns but share the same stores and helpers. Go lets one type's methods span files in a package, so we split by concern without inventing two services.

**Indexes in `repository/indexes.go`, not `pkg/mongo`.** A collection name is app knowledge.

**`pkg/mongo` re-exports `Client`/`Database`.** It lets `lib/boot` name those types without importing the driver, which keeps the "driver only in `repository/` + `pkg/mongo`" check true.

**`lib/coreevents` has a Dispatcher *and* a Consumer.** The pipeline (dedupe, transaction, routing) is independent of where events come from: a queue for the API, a file for the backfill.

**Two package names differ from their folders:** `lib/errors` → `apperr`, `lib/http` → `httpx`. Always import them with the explicit alias.
