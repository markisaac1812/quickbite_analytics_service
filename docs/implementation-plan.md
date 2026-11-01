# Implementation plan

Every phase ends with an **acceptance check you run**, not one you reason about. Phases 1–6 are the video; 7–12 are homework round 1 (done); 13+ are still open.

**One command checks phases 1–12:** start Mongo (replica set) and RabbitMQ, then `go run ./cmd/api` and `go run ./play/mock-core`, then:

```bash
go run ./play/e2e                       # 56 checks, expects "56 passed, 0 failed"
MONGO_TRANSACTIONS=off go run ./cmd/api # restart the API in fallback mode, then
MONGO_TRANSACTIONS=off go run ./play/e2e   # same 56 checks
```

---

## Phase 1: Skeleton, config, logging, boot ✅

**Accept:** `go build ./... && go vet ./...` is clean. With `ACCESS_SECRET` unset, boot exits 1 with a `config:` error. Ctrl+C → `shutdown requested`, exit 0.

## Phase 2: HTTP plumbing ✅

**Accept:** `/health` → 200 envelope + `X-CorrelationId`. Unknown route → 404 `NOT_FOUND` with the echoed correlation id in the access log.

## Phase 3: Mongo + `agg_restaurant_day` ✅

**Accept:** boot logs `mongo indexes ensured`. `getIndexes()` shows the unique + range indexes. `grep -rn "agg_\|event_ids" pkg/` prints nothing.

## Phase 4: RabbitMQ, consumer, `order.placed`, dedupe ✅

**Accept (e2e: original acceptance):** 2500 → `orders 1 / revenue 2500`; same eventId → unchanged; 1500 → `2 / 4000`; malformed → DLQ; RabbitMQ restart → `rabbit reconnected`.

## Phase 5: Auth + RBAC ✅

**Accept:** 401 for missing, garbage or expired tokens. Owner twice → one core call. Staff / other tenant / customer → 403. Core down on an uncached role → 503.

## Phase 6: Read endpoint + docs ✅

**Accept:** `/days` → `[{date, ordersCount: 2, revenueMinor: 4000, currency: "EGP", avgOrderMinor: 2000}]`. Range errors → 400 codes. Unknown restaurant → `[]`. Layering checks pass. Graceful shutdown → exit 0.

---

## Phase 7: Transactions + Dispatcher refactor ✅

**Build:** `pkg/mongo/tx.go`; `coreevents.Dispatcher` (broker-free) + `Consumer`; `MONGO_TRANSACTIONS=auto|on|off`.

**Accept:** with a replica set the boot log says `mongo transactions enabled`. An invalid payload leaves no `event_ids` marker (e2e "ignored / invalid events"). `MONGO_TRANSACTIONS=on` against a standalone fails boot.

## Phase 8: `agg_branch_day`, `agg_platform_day`, `agg_product_day` ✅

**Build:** shared `DayCounters` + `counters.go`; branch/platform repos; product repo with `BulkWrite`; `mergeLines`.

**Accept (e2e):** an order with a duplicated product line → product 1 `units 4 / revenue 4000 / orders 2`; branch 8 row exists; platform EGP and SAR rows are separate.

## Phase 9: `order.delivered`, `order.rejected`, `payment.completed` ✅

**Build:** `order_timelines` + claims; `settle()`; payment `incDay` on the capture day.

**Accept (e2e):**
- delivered-before-placed → nothing until placed, then `delivery_ms_sum 2,100,000`;
- in-order delivered → count 2;
- replayed and re-sent delivered → still 2;
- rejected-before-placed → counted once placed arrives;
- payment-before-placed → `payments 1 / 4200`.

## Phase 10: 8 endpoints ✅

**Build:** DTOs, `reports.service.go`, controllers, guards `RequireBranchAccess` / `RequireRole`.

**Accept (e2e):**
- failures `0.3333`, delivery-avg `1,800,000`, payments `4200`;
- top products `[2, 1, 4]`, `limit` validation;
- branch manager allowed on branch 7, 403 on branch 8;
- platform days / active (3) / top (`[42, 43]`), currency validation, owner → 403.

## Phase 11: `rbac.permissions_changed` consumer ✅

**Build:** `analytics-service.core-events` queue (`rbac.#`, own DLX/DLQ), `NoDedupe`, `Cache.HandlePermissionsChanged`.

**Accept (e2e):** cached role → no core call. Event with a role → that role is re-fetched. Event without a role → every role is re-fetched.

## Phase 12: Backfill + core seed + secrets ✅

**Build:** `cmd/backfill-aggs`, `repository.ResetAll`, `docs/backfill.md`; core migration `20261028180000_seed_analytics_service_permissions.ts`; shared strong secrets in all three `.env` files.

**Accept:**
- e2e: rebuild == live; catch-up changes nothing; rebuild from the **reversed** file == live; `-reset` without `-yes` is refused.
- CLI: `-dry-run` counts per type; a bad line → exit 1.
- The core migration type-checks: `npx tsc --noEmit --ignoreConfig --skipLibCheck --target es2020 --module commonjs --esModuleInterop src/migrations/20261028180000_seed_analytics_service_permissions.ts`.

---

## Phase 13: Go tests (done)

**Accept:** `go test ./...` (unit) and `go test -tags integration ./...` are green; `scripts/test-coverage.sh` reports ≥ 80% merged coverage. Covers:
- `mergeLines`, `checkRange`, `avg`, `rate`, every report, out-of-order settling (fake `Stores`);
- `Dispatcher.Apply`: no handler / dup / failure → `Forget` / tx path, with fakes; then exactly-once against a real replica set;
- the guards, auth, controllers and validation with `httptest`;
- every repository, `pkg/mongo`, `pkg/messaging` (DLQ, panic, reconnect) against real servers;
- the booted service end to end (events in, HTTP out), backfill, boot failures.

## Phase 14: `order.cancelled`, `order.accepted`, `payment.failed` (open)

**Accept:** cancellation counted on the placed day through the timeline (same e2e pattern as rejections); time-to-accept average; payment failure rate per capture day.

## Phase 15: core publishes `rbac.permissions_changed` (open)

**Accept:** a core API that changes a role's permissions writes the event to core's outbox in the same transaction. The analytics e2e invalidation check passes against real core.

## Phase 16: DLQ replay tool + export endpoint (open)

**Accept:** DLQ messages are re-published with their original routing key; backfill pulls history from an order-service internal endpoint.

## Phase 17: Docker Compose (DevOps section, open)

**Accept:** `docker compose up` starts the Mongo replica set, RabbitMQ, and the three services with shared secrets from one env file. `go run ./play/e2e` is green against it.
