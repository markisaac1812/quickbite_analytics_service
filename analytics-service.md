# Task: Build the QuickBite analytics-service from scratch

## Context

You're working in a monorepo at `quickbite/` that contains two existing Node services and one new directory you'll build:

```
quickbite/
├── core-service/        # Node + TS + Postgres + Redis. Owns users, restaurants, products, RBAC.
├── order-service/       # Node + TS + sharded Postgres + Redis. Owns orders, payments, deliveries.
└── analytics-service/   # ← you build this. Go + MongoDB + RabbitMQ.
```

**Before writing a line of code, read these files in the existing services** — they are the patterns you will mirror:

- `core-service/CLAUDE.md` and `order-service/CLAUDE.md` — every architectural rule.
- `core-service/src/lib/events/{event-types,types,outbox.repo,outbox-drain,init}.ts` — the transactional outbox pattern.
- `core-service/src/pkg/messaging/rabbitmq.client.ts` — the publisher.
- `order-service/src/lib/core-events/consumer.ts` — the consumer (dedupe + DLQ).
- `order-service/src/lib/config/env.ts` — the env-parsing convention.
- `order-service/src/app/order/service/order.service.ts` — the order placement flow you'll publish events from.
- `order-service/src/lib/auth/{guard,rbac,api-key}.ts` — auth + RBAC.

You won't have to re-invent any of these. **Mirror the pattern, find the Go analogue.**

---

## Mission of `analytics-service`

It owns per-day rollups of order/payment/delivery activity:

- **Consumes** events from `order-service` via RabbitMQ.
- **Upserts** day-grained aggregates into MongoDB (`agg_restaurant_day`, `agg_branch_day`, `agg_product_day`, `agg_platform_day`).
- **Serves** read-only HTTP endpoints under `/api/v1/analytics/...`.

It does **not** own users, restaurants, products, orders, payments. It does **not** write to any operational data store. It does **not** emit events.

This is a **teaching artifact**, not a production push. Build **one full vertical slice end-to-end** in the video; document the rest as homework.

---

## Locked tech stack — do not deviate

| Concern        | Choice                                              |
| -------------- | --------------------------------------------------- |
| Runtime        | Go 1.21+                                            |
| HTTP router    | `github.com/go-chi/chi/v5`                          |
| DB             | `go.mongodb.org/mongo-driver` (official, v1)        |
| Messaging      | `github.com/rabbitmq/amqp091-go`                    |
| Config         | `github.com/caarlos0/env/v11` (struct tags)         |
| Logger         | stdlib `log/slog`                                   |
| Validation     | `github.com/go-playground/validator/v10`            |
| JWT            | `github.com/golang-jwt/jwt/v5`                      |
| UUID           | `github.com/google/uuid`                            |
| DI             | **NO framework.** Explicit constructor wiring in `cmd/api/main.go`. |

**Forbidden:** GORM / Ent / any ODM. Repositories use the official mongo-driver directly with typed structs and `bson` tags. This is the deliberate Go analogue of "Knex query builder, not an ORM" — same philosophy, different language.

**Forbidden:** DDL migrations. Mongo is schemaless. Indexes live in one file (`pkg/mongo/indexes.go`) and are created idempotently on boot via `EnsureIndexes`.

---

## Required folder structure (mirrors the Node services)

```
analytics-service/
├── cmd/
│   └── api/main.go                # ~10 lines: just calls lib/boot.Run()
├── pkg/                           # framework-free, NO imports from lib/ or app/, NO app-specific knowledge
│   ├── mongo/client.go            # Connect/Disconnect ONLY — knows nothing about your collections
│   ├── messaging/{types.go, amqp.go}      # Broker interface + amqp091 impl
│   └── httpclient/client.go               # net/http wrapper: timeout, JSON, retry-on-5xx
├── lib/                           # app-aware glue: env, middleware
│   ├── boot/boot.go               # wires every singleton; main.go just calls Run()
│   ├── config/env.go              # struct + Load() — equivalent of the zod schema
│   ├── logger/logger.go           # slog wrapper + FromContext
│   ├── appcontext/context.go      # ctx keys: claims, correlation_id (≈ express.d.ts)
│   ├── errors/{apperror.go, handler.go}   # AppError + Wrap(handler) middleware
│   ├── http/response.go           # SendSuccess, SendPaginated
│   ├── middleware/correlation.go  # Correlation + AccessLog
│   ├── auth/{jwt.go, middleware.go, apikey.go}
│   ├── rbac/{cache.go, middleware.go}     # in-process cache + Require("perm")
│   ├── coreclient/{client.go, rbac.go, types.go}
│   └── coreevents/{consumer.go, payloads.go}  # generic only; payloads.go holds Envelope + EventHandler type
├── app/
│   └── analytics/                          # PACKAGE analytics — shared module types live here
│       ├── types.go                        # OnOrderPlacedInput, RestaurantDayRow — shared across subpackages
│       ├── errors.go                       # var Err… = apperr.New(...)
│       ├── enums.go                        # const PermAnalyticsRead = "analytics:read"
│       ├── entity/                         # plain structs + bson tags
│       ├── repository/                     # ONLY place mongo-driver appears
│       │   └── indexes.go                  # EnsureIndexes for this module's collections (NOT in pkg/mongo)
│       ├── service/analytics.service.go    # JUST the service struct + methods (no types/errors/enums here)
│       ├── controller/{*.controller.go, routes.go}
│       ├── dto/{*.request.go, *.response.go}
│       └── eventhandlers/handlers.go       # event type → service method map
├── play/                          # GITIGNORED — dev aids only (mock-core, mint-jwt, publish-test, check-mongo)
├── go.mod, .env.example, .gitignore
├── CLAUDE.md, README.md, plan.md
└── docs/
    ├── folder-structure.md
    ├── system-design.md
    ├── api-contracts.md
    ├── node-to-go-mapping.md       # THE TEACHING DOC — see §below
    ├── ai-prompts.md
    └── implementation-plan.md
```

**Layering rules (enforced by review):**

```
app/  → may import lib, pkg
lib/  → may import pkg, env; may NOT import app/<module>/*
pkg/  → no imports from lib or app, no env, no globals, NO app-specific knowledge
```

**Strict reading of "no app-specific knowledge in `pkg/`":** `pkg/mongo` knows how to connect; it does NOT know that this service has an `agg_restaurant_day` collection. That's app knowledge — it lives in `app/analytics/repository/indexes.go`. If you find yourself writing collection names in `pkg/`, stop.

**Why `cmd/api/main.go` is tiny:** The process entry point should do one thing — hand control to the bootstrap and exit. Putting wiring in `main.go` means every new singleton requires editing the entry point, which gets crowded fast. Wiring lives in `lib/boot/boot.go`. Adding a module = edit one function in `lib/boot`. `main.go` never changes.

**Why `play/` instead of `cmd/`:** `play/` is gitignored. The four programs in it (mock-core, mint-jwt, publish-test, check-mongo) are dev aids — they make the slice testable without spinning up Postgres + order-service, but they're not part of the service. Pollute `cmd/` with them and someone will assume they're production binaries.

**Why types/errors/enums in `app/analytics/` (parent) and not `app/analytics/service/`:** The service struct is one concern; the types it consumes and returns are a different concern. Putting `OnOrderPlacedInput`, `ErrInvalidDateRange`, `PermAnalyticsRead` in the parent `analytics` package means subpackages (`service`, `controller`, `eventhandlers`) all import them by the same name, and `service/` stays focused on the service implementation only.

If a `lib/*` file needs something from `app/*`, invert the dependency by defining a small interface in `lib/`. Example: the consumer needs a "have I seen this event before?" capability — define `EventDeduper` interface in `lib/coreevents/`, and let `app/analytics/repository.EventIDsRepo` satisfy it implicitly via Go duck typing.

---

## The one slice to build (video scope)

Build these **fully end-to-end**, nothing else:

- **One aggregate collection:** `agg_restaurant_day` with documents `{restaurant_id, date "YYYY-MM-DD" UTC, currency, orders_count, revenue_sum, delivery_ms_sum, delivery_ms_count, updated_at}`. Averages stored as sum+count so per-day rows merge associatively across replays.
- **Indexes** (declared in `pkg/mongo/indexes.go`):
  - `agg_restaurant_day`: unique `(restaurant_id, date)`, range `(date, restaurant_id)`.
  - `event_ids`: unique `event_id`, TTL on `received_at` (7 days).
- **One inbound event:** `order.placed` consumed from RabbitMQ exchange `order.events` (queue `analytics-service.order-events`, bindings `order.#, payment.#`, DLQ wired).
- **Idempotency:** Mongo `event_ids` collection. Unique index → `InsertOne` returns dup-key on replay → ack-and-skip.
- **One GET endpoint:** `GET /api/v1/analytics/restaurants/:restaurantId/days?from=&to=`.
  - Returns array of `{date, ordersCount, revenueMinor, currency, avgOrderMinor}`.
  - Money in integer minor units. Timestamps ISO-8601 UTC.
  - `avgOrderMinor` derived in the service layer (revenue/count), not stored.
- **All cross-cutting infra:** JWT auth (same secret/shape as core/order), RBAC read-through cache from core (via HTTP, NOT the rbac.permissions_changed consumer — that's homework), correlation IDs, structured logging, error envelope, graceful shutdown.

---

## Cross-cutting requirements

### Auth + RBAC
- JWT verified with `ACCESS_SECRET` (same as core/order). HS256. Claims: `userId, role, restaurantId?, restaurantRole?, branchIds?`. Token from cookie first, then `Authorization: Bearer`.
- RBAC: this service has **no permissions catalog**. It looks up role permissions via core's `GET /api/internal/rbac/permissions?role=...` (header `api-key: <internal>`). In-process cache by role with TTL (default 5min). The `rbac.permissions_changed` consumer is **homework**.

### Idempotency
Every event handler funnels through `EventDeduper.MarkSeen(eventId)` before dispatching. Duplicate → ack-and-skip. Unknown event types → log + ack-skip (don't DLQ everything).

### Error handling
- Services and controllers return `error`. The `errors.Wrap(logger, handler)` middleware renders the envelope.
- Module-level `var Err… = apperr.New(code, status, msg)` in `service/errors.go`. No inline `apperr.New(...)` at call sites for cases that have a stable name.

### Response envelopes
- Success: `{"success": true, "data": ...}`.
- Error: `{"success": false, "error": {"code": "...", "message": "..."}}`.
- Every HTTP response goes through a **Response DTO struct** in `dto/*.response.go`. Never leak entity fields directly.

### Inline types forbidden
Never declare `type X struct{...}` inside a `*.service.go`, `*.controller.go`, or `*.repo.go`. Module-shared types live in `app/<module>/types.go` (package `<module>`); cross-cutting infra types in `lib/<area>/types.go` or `lib/<area>/payloads.go`.

---

## Docs you must produce

1. **`CLAUDE.md`** — every rule above, in a form a future AI agent or developer can grep. Sections: mission, stack, folder structure, layering rules, naming, response shape, db rules, cross-cutting infra, performance budget, code style "what to avoid", reference doc index, out-of-scope list.
2. **`README.md`** — quick start: prereqs (mongod + rabbit running), `.env` setup, three terminals (api, mock-core, publish-test), expected output.
3. **`plan.md`** — what's in the video vs. homework.
4. **`docs/folder-structure.md`** — annotated tree.
5. **`docs/system-design.md`** — ASCII diagram of the platform, sync vs. async flows, failure-mode table, why-Mongo-dedupe-not-Redis-SETNX explanation.
6. **`docs/api-contracts.md`** — request/response shapes + every error code.
7. **`docs/node-to-go-mapping.md`** — **the most important doc.** Side-by-side tables: TS idiom → Go idiom (class → struct+constructor, decorators → struct tags, `Promise<T>` → `(T, error)`, etc.). For every layer (config, logger, auth, errors, DTOs, repos, services, controllers, routes, messaging, DI), show a few-line Node example and the Go equivalent. End with "common gotchas when porting" and "when to break the mapping" sections.
8. **`docs/ai-prompts.md`** — prompt templates for students using AI on the homework. Include: "make the AI prove it understood the existing code before writing new code", "ask for the Node analogue then the Go translation", concrete prompts for each piece of homework. The meta-message: **the skill you're learning isn't Go, it's directing an AI through a non-trivial implementation in a new language by anchoring it to a codebase you already know.**
9. **`docs/implementation-plan.md`** — phases (1–6 = video, 7+ = homework) with acceptance checks per phase.

---

## E2E acceptance — must pass before you call this done

Build the four helper binaries in `play/` (gitignored): `publish-test`, `mock-core`, `mint-jwt`, `check-mongo` so the slice is testable without spinning up postgres + order-service.

Run the binary and verify, in order:

1. `go build ./...` and `go vet ./...` clean.
2. Boot logs structured JSON: `mongo connected`, `rabbit connected`, `event consumer started`, `http listening`.
3. `GET /health` → 200 envelope.
4. Publish `order.placed` for `restaurant=42, total=2500` → `mongo agg_restaurant_day` shows `orders_count: 1, revenue_sum: 2500`.
5. Publish same `eventId` again → mongo unchanged (dedupe works).
6. Publish second event `total=1500` → `orders_count: 2, revenue_sum: 4000`.
7. `GET …/days` without auth → 401 `UNAUTHENTICATED`.
8. `GET …/days` with garbage cookie → 401.
9. `GET …/days` with valid JWT (mock-core returning `analytics:read`) → 200 with `[{date, ordersCount: 2, revenueMinor: 4000, currency: "EGP", avgOrderMinor: 2000}]`. The derived `avgOrderMinor: 2000` confirms service-layer projection works.
10. `from > to` → 400 `ANALYTICS_INVALID_DATE_RANGE`. Missing `from` → 400 `VALIDATION_ERROR`. Bad path id → 400.
11. Restaurant with no data → 200 `[]`.

If any of these don't pass, fix and re-run before reporting done. Don't claim success on theoretical tests — actually run the commands.

---

## Working style

- **Read first, write second.** Skim `order-service/CLAUDE.md` and the messaging files before writing anything. Most decisions are already made.
- **Mirror the existing pattern.** If you find yourself inventing a new convention, you're probably wrong — go re-read the Node side.
- **One slice end-to-end before going wide.** Don't write all four aggregate repos and then try to wire them up; finish `agg_restaurant_day` completely first.
- **Verify with real commands.** "I built the consumer" and "I built the consumer, published an event, and saw the mongo doc update" are different claims.
- **The teaching docs matter.** Especially `docs/node-to-go-mapping.md` — that's the document the students will reach for most, and they'll judge the whole codebase by how good it is.
