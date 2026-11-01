# analytics-service

QuickBite's analytics service, in Go. It consumes `order.*` and `payment.*` events from `order-service` (plus `rbac.*` from `core-service`) over RabbitMQ, folds them into per-day rollups in MongoDB, and serves them read-only over HTTP.

```text
order-service ──(outbox → order.events)──▶ RabbitMQ ──▶ analytics-service ──▶ MongoDB
core-service  ──(outbox → core.events: rbac.#)──────▶        │   ▲
core-service ◀──── GET /api/internal/rbac/permissions ───────┘   │
                                    GET /api/v1/analytics/... ───┘
```

Rules for contributors live in [`CLAUDE.md`](CLAUDE.md). If you know the Node services, read [`docs/node-to-go-mapping.md`](docs/node-to-go-mapping.md) first.

---

## Prerequisites

- **Go 1.26+**. The code needs 1.22; the floor comes from current `x/crypto`/`validator`.
- **MongoDB 6+**, ideally as a **single-node replica set**, which enables transactions and exactly-once applies. A standalone mongod also works, with weaker guarantees (`CLAUDE.md` §7).
- **RabbitMQ 3.x**
- `curl`

No Postgres, core-service, or order-service is needed: the `play/` tools stand in for them.

```bash
docker run -d --name qb-analytics-mongo  -p 27017:27017 mongo:7 --replSet rs0 --bind_ip_all
docker exec qb-analytics-mongo mongosh --quiet --eval \
  'rs.initiate({_id:"rs0",members:[{_id:0,host:"localhost:27017"}]})'
docker run -d --name qb-analytics-rabbit -p 5672:5672 -p 15672:15672 rabbitmq:3-management
```

(A Docker Compose file comes with the DevOps section later.)

## Configure

```bash
cp .env.example .env
```

`ACCESS_SECRET` and `CORE_INTERNAL_API_KEY` are **shared secrets**: they must be byte-identical to core-service's `ACCESS_SECRET` / `INTERNAL_API_KEY` and order-service's `ACCESS_SECRET` / `CORE_INTERNAL_API_KEY`. Generate each value once and paste it into all three `.env` files:

```bash
python -c "import secrets; print(secrets.token_urlsafe(48))"
```

Production values come from a secret store, never from a committed file. Every variable is listed in `lib/config/env.go`.

## Build & check

```bash
go build ./... && go vet ./...
```

## Test

Same flow as order-service's `npm test` / `test:integration` / `test:coverage`, written the Go way: test files sit next to the code, and integration files carry `//go:build integration`.

```bash
go test ./...                          # unit: no Mongo, no RabbitMQ (fakes + httptest)
go test -tags integration ./...        # + integration: needs the Mongo replica set and RabbitMQ above
scripts/test-coverage.sh               # both suites, merged coverage, fails below 80% (writes coverage.html)
```

The integration suites never touch dev data. Each package uses its own `quickbite_analytics_test_<pkg>` database and `analytics-test-*` queues and exchanges, and drops them afterwards. Point them elsewhere with `TEST_MONGO_URI` / `TEST_RABBITMQ_URL`. Set `TEST_LOG=1` to see the booted service's logs in `lib/boot`. Conventions are in `CLAUDE.md` §14.

---

## Run it

```bash
# terminal 1: the service
go run ./cmd/api
#   mongo connected → mongo indexes ensured → rabbit connected
#   → mongo transactions enabled → event consumer started (order.events) → event consumer started (core.events)
#   → http listening :4002

# terminal 2: mock core-service (RBAC)
go run ./play/mock-core            # owner & branch_manager have analytics:read; staff doesn't

# terminal 3: the full acceptance suite (56 checks; wipes the analytics collections)
go run ./play/e2e
```

`play/e2e` publishes real events through RabbitMQ and checks Mongo plus every endpoint. It covers:
- dedupe;
- `BulkWrite` line merging;
- delivered-before-placed, rejected-before-placed, payment-before-placed;
- branch, tenant and admin access rules;
- the `rbac.permissions_changed` cache invalidation;
- backfill rebuilds that must equal the live state, even from a **reversed** event file.

### Driving it by hand

```bash
go run ./play/check-mongo -reset
go run ./play/publish-test -restaurant 42 -total 2500 -order-id o-1 \
  -items '[{"productId":1,"quantity":2,"lineTotal":2500}]'
go run ./play/publish-test -type order.delivered -restaurant 42 -order-id o-1   # delivery time = now − placedAt
go run ./play/publish-test -type order.rejected  -restaurant 42 -order-id o-2   # waits until o-2 is placed
go run ./play/publish-test -type payment.completed -restaurant 42 -order-id o-3 -total 4200
go run ./play/publish-test -type rbac.permissions_changed -role owner          # → core.events
go run ./play/check-mongo                                                      # restaurant, branch, product, platform rows

TOKEN=$(go run ./play/mint-jwt)                                  # owner of restaurant 42
ADMIN=$(go run ./play/mint-jwt -role system_admin -user-id 1)
MGR=$(go run ./play/mint-jwt -restaurant-role branch_manager -branch-ids 7)
D=$(date -u +%F); Q="from=$D&to=$D"; B=localhost:4002/api/v1/analytics

curl -s -b "access_token=$TOKEN" "$B/restaurants/42/days?$Q"
curl -s -b "access_token=$TOKEN" "$B/restaurants/42/failures?$Q"
curl -s -b "access_token=$TOKEN" "$B/restaurants/42/delivery-avg?$Q"
curl -s -b "access_token=$TOKEN" "$B/restaurants/42/payments?$Q"
curl -s -b "access_token=$TOKEN" "$B/restaurants/42/products/top?$Q&limit=5"
curl -s -b "access_token=$TOKEN" "$B/restaurants/42/branches/7/days?$Q"
curl -s -b "access_token=$ADMIN" "$B/platform/days?$Q"
curl -s -b "access_token=$ADMIN" "$B/platform/active-restaurants?$Q"
curl -s -b "access_token=$ADMIN" "$B/platform/top-restaurants?$Q&currency=EGP"
```

`Authorization: Bearer <token>` works as well as the cookie. Every contract and error code is in [`docs/api-contracts.md`](docs/api-contracts.md).

### Backfill

```bash
go run ./cmd/backfill-aggs -input events.ndjson -dry-run        # parse + count
go run ./cmd/backfill-aggs -input events.ndjson                 # catch-up (deduped)
go run ./cmd/backfill-aggs -input events.ndjson -reset -yes     # full rebuild
```

[`docs/backfill.md`](docs/backfill.md) has the `psql` export query against order-service's `events_outbox`.

---

## Against the real platform

1. **Secrets:** identical `ACCESS_SECRET` and internal key in core, order, and analytics (see Configure).
2. **Permission:** run core-service's migrations (`npm run migrate` in `core-services/`). `20261028180000_seed_analytics_service_permissions.ts` grants `analytics:read` to `owner` and `branch_manager`.
3. **Events:** order-service already writes `order.placed` / `order.delivered` / `order.rejected` / `payment.completed` to its transactional outbox and drains them to `order.events`. Point all services at the same RabbitMQ.
4. **Upgrading from the first version of this service:** the order-events DLX was renamed to `analytics-service.order-events.dlx`. Delete the old queue once (`rabbitmqctl delete_queue analytics-service.order-events`) or boot fails with `PRECONDITION_FAILED`.

## What's here and what isn't

`plan.md` lists what's built and what's left. `docs/implementation-plan.md` has the phases and their acceptance checks.
