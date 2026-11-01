# Plan: what's built, what's left

## Video slice (phases 1–6): done

One vertical slice end to end:
- `agg_restaurant_day` from `order.placed`, with `event_ids` dedupe;
- `GET /restaurants/{id}/days`;
- JWT auth, RBAC via core, correlation IDs, the envelope, graceful shutdown;
- the `play/` dev aids.

## Homework round 1 (phases 7–12): done

| Item | What exists |
| --- | --- |
| 3 more aggregates | `agg_branch_day` `(branch_id, date)`, `agg_product_day` `(product_id, date)`, `agg_platform_day` `(date, currency)`. Every day grain shares one counter block (`entity.DayCounters`). |
| 3 more handlers | `order.delivered` (delivery time), `order.rejected` (rejection count and revenue), `payment.completed` (captured payments) |
| Gotcha: out-of-order | `order_timelines` + atomic claims: delivered/rejected **before** placed are settled when placed arrives; payment-before-placed needs nothing (self-contained `$inc`) |
| Gotcha: line items | `agg_product_day` = **one unordered `BulkWrite`** per order, lines merged by product first |
| Exactly-once | every event applies in **one Mongo transaction** (dedupe marker + all writes) on a replica set; standalone falls back to dedupe + `Forget` |
| 8 more endpoints | restaurant `failures`, `delivery-avg`, `payments`, `products/top`, `branches/{id}/days`; platform `days`, `active-restaurants`, `top-restaurants`. Failures and delivery-avg are **derived** from existing counters; no new collections. |
| RBAC invalidation | second consumer `analytics-service.core-events` (`rbac.#`, own DLX/DLQ, `NoDedupe`) → `rbac.Cache.Invalidate(role)` |
| Backfill | `cmd/backfill-aggs`: NDJSON export of `events_outbox` → the same Dispatcher; catch-up or `-reset -yes` rebuild; order-independent |
| core-service | migration `20261028180000_seed_analytics_service_permissions.ts` (`analytics:read` → owner, branch_manager) |
| Secrets | one strong shared `ACCESS_SECRET` + internal key across core, order, and analytics `.env` |
| Acceptance | `play/e2e`: 56 checks, green with transactions on **and** off |

## Still open (homework round 2)

1. ~~**Go unit tests**~~: done. Unit + integration suites, `scripts/test-coverage.sh` gate at 80% (see `CLAUDE.md` §14).
2. **More handlers**: `order.cancelled` (cancel rate, same timeline pattern as rejected), `order.accepted` (time-to-accept), `payment.failed` (payment failure rate per capture day).
3. **core-service publishes `rbac.permissions_changed`** when a role's permissions change. Today no code path changes them, so this ships together with a permission-management API.
4. **Hot platform row**: bucket `agg_platform_day` (`restaurant_id % N`) if transaction `WriteConflict` retries show up in latency.
5. **DLQ replay tool** under `play/` (shovel the DLQ back to its exchange with the original routing key).
6. **order-service internal export endpoint**, so backfill can pull history instead of relying on a `psql` export.
7. **Docker Compose** for Mongo (replica set) + RabbitMQ + the three services: the DevOps section.

Prompt templates for these: `docs/ai-prompts.md`. Acceptance checks: `docs/implementation-plan.md`.
