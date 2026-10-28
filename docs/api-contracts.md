# API contracts: analytics-service

Base URL (local): `http://localhost:4002`. Versioned prefix: `/api/v1/analytics`.

## Conventions

| Concern | Rule |
| --- | --- |
| Success envelope | `{"success": true, "data": <payload>}` |
| Error envelope | `{"success": false, "error": {"code": "<UPPER_SNAKE>", "message": "<human text>"}}` |
| Money | integer **minor units** (`4000` = 40.00 EGP) with `currency` alongside |
| Durations | integer **milliseconds** |
| Rates | `0..1`, rounded to 4 decimals (`0.3333`) |
| Dates | `YYYY-MM-DD` = a **UTC** calendar day |
| Ranges | `from`/`to` inclusive, `from ≤ to`, at most **366 days** |
| Sparse series | a day appears only if the endpoint's metric has data that day; no data → `[]` |
| Day bucket | orders, revenue, delivery time, rejections → the day the order was **placed**; payments → the day they were **captured** |
| Correlation | send `X-CorrelationId`; it is always echoed back |
| Auth | `access_token` cookie (preferred) **or** `Authorization: Bearer <jwt>`; HS256, signed by core-service |

## Endpoint index

| # | Method + path | Access |
| --- | --- | --- |
| 0 | `GET /health` | public |
| 1 | `GET /restaurants/{restaurantId}/days` | `analytics:read` + own restaurant |
| 2 | `GET /restaurants/{restaurantId}/failures` | `analytics:read` + own restaurant |
| 3 | `GET /restaurants/{restaurantId}/delivery-avg` | `analytics:read` + own restaurant |
| 4 | `GET /restaurants/{restaurantId}/payments` | `analytics:read` + own restaurant |
| 5 | `GET /restaurants/{restaurantId}/products/top` | `analytics:read` + own restaurant |
| 6 | `GET /restaurants/{restaurantId}/branches/{branchId}/days` | `analytics:read` + own restaurant + own branch |
| 7 | `GET /platform/days` | `system_admin` |
| 8 | `GET /platform/active-restaurants` | `system_admin` |
| 9 | `GET /platform/top-restaurants` | `system_admin` |

### Access rules

| Caller (JWT) | Restaurant endpoints (1–5) | Branch endpoint (6) | Platform (7–9) |
| --- | --- | --- | --- |
| no / invalid / expired token | 401 | 401 | 401 |
| `system_admin` | any restaurant | any branch | ✓ |
| `restaurant_user`, role with `analytics:read`, `restaurantId` = path | ✓ | `owner`: any branch of it; others: only ids in `branchIds` | 403 |
| `restaurant_user` without `analytics:read` (e.g. `staff`) | 403 | 403 | 403 |
| `restaurant_user` of another restaurant | 403 | 403 | 403 |
| other roles | 403 | 403 | 403 |

`analytics:read` is granted to `owner` and `branch_manager` by core migration `20261028180000_seed_analytics_service_permissions.ts`.

Check order: authenticate → permission/role → restaurant scope → branch scope → request validation → business rules.

---

## 0. `GET /health`

**200** `{"success": true, "data": {"status": "ok", "mongo": "up", "rabbit": "up"}}`
**503** `{"success": false, "error": {"code": "UNHEALTHY", "message": "mongo=up rabbit=down"}}`

---

## 1. `GET /restaurants/{restaurantId}/days?from=&to=`

One row per day with orders.

```json
{"success": true, "data": [
  {"date": "2026-10-15", "ordersCount": 6, "revenueMinor": 14200, "currency": "EGP", "avgOrderMinor": 2366}
]}
```

| Field | Source |
| --- | --- |
| `ordersCount` | `orders_count` (placed orders, including later-rejected ones) |
| `revenueMinor` | `revenue_sum` (gross placed total) |
| `avgOrderMinor` | **derived**: `revenue_sum / orders_count`, integer division |

## 2. `GET /restaurants/{restaurantId}/failures?from=&to=`

Derived from `orders_count` and `rejected_count`; no extra collection.

```json
{"success": true, "data": {
  "from": "2026-10-15", "to": "2026-10-15",
  "ordersCount": 6, "rejectedCount": 2, "failureRate": 0.3333,
  "days": [{"date": "2026-10-15", "ordersCount": 6, "rejectedCount": 2, "failureRate": 0.3333}]
}}
```

`failureRate = rejectedCount / ordersCount` (0 when there are no orders). The range rate is computed from the range totals, not by averaging the daily rates.

## 3. `GET /restaurants/{restaurantId}/delivery-avg?from=&to=`

Derived from `delivery_ms_sum` and `delivery_ms_count`; no extra collection. Delivery time = `deliveredAt − placedAt`, bucketed on the placed day.

```json
{"success": true, "data": {
  "from": "2026-10-15", "to": "2026-10-15",
  "deliveredCount": 2, "avgDeliveryMs": 1800000,
  "days": [{"date": "2026-10-15", "deliveredCount": 2, "avgDeliveryMs": 1800000}]
}}
```

## 4. `GET /restaurants/{restaurantId}/payments?from=&to=`

Captured online payments (`payment.completed`; COD orders never emit it), by capture day.

```json
{"success": true, "data": {
  "from": "2026-10-15", "to": "2026-10-15",
  "paymentsCount": 1, "paymentsMinor": 4200, "currency": "EGP",
  "days": [{"date": "2026-10-15", "paymentsCount": 1, "paymentsMinor": 4200, "currency": "EGP"}]
}}
```

## 5. `GET /restaurants/{restaurantId}/products/top?from=&to=&limit=`

Best sellers by revenue. `limit` is optional: default 10, allowed range 1–100 (0 or absent means the default).

```json
{"success": true, "data": [
  {"productId": 2, "unitsSold": 5, "revenueMinor": 5700, "ordersCount": 2, "currency": "EGP"},
  {"productId": 1, "unitsSold": 4, "revenueMinor": 4000, "ordersCount": 2, "currency": "EGP"}
]}
```

`ordersCount` = number of orders containing the product. Ties are broken by `productId` ascending.

## 6. `GET /restaurants/{restaurantId}/branches/{branchId}/days?from=&to=`

Same row shape as endpoint 1, for one branch. A `branchId` that belongs to another restaurant returns `[]`, because the query filters on both ids.

## 7. `GET /platform/days?from=&to=`

One row per (day, currency). Currencies are never summed together.

```json
{"success": true, "data": [
  {"date": "2026-10-15", "currency": "EGP", "ordersCount": 7, "revenueMinor": 19200, "avgOrderMinor": 2742,
   "rejectedCount": 2, "failureRate": 0.2857, "deliveredCount": 2, "avgDeliveryMs": 1800000,
   "paymentsCount": 1, "paymentsMinor": 4200},
  {"date": "2026-10-15", "currency": "SAR", "ordersCount": 1, "revenueMinor": 3000, "avgOrderMinor": 3000,
   "rejectedCount": 0, "failureRate": 0, "deliveredCount": 0, "avgDeliveryMs": 0, "paymentsCount": 0, "paymentsMinor": 0}
]}
```

## 8. `GET /platform/active-restaurants?from=&to=`

Restaurants with at least one order: distinct over the whole range, plus per day.

```json
{"success": true, "data": {"from": "2026-10-15", "to": "2026-10-15", "activeRestaurants": 3,
  "days": [{"date": "2026-10-15", "activeRestaurants": 3}]}}
```

## 9. `GET /platform/top-restaurants?from=&to=&currency=&limit=`

`currency` is **required** (ISO 4217, uppercase), because revenue in different currencies can't be ranked together. `limit` works as in endpoint 5.

```json
{"success": true, "data": [
  {"restaurantId": 42, "ordersCount": 6, "revenueMinor": 14200, "avgOrderMinor": 2366, "currency": "EGP"},
  {"restaurantId": 43, "ordersCount": 1, "revenueMinor": 5000, "avgOrderMinor": 5000, "currency": "EGP"}
]}
```

---

## Errors

| Status | Code | When |
| --- | --- | --- |
| 400 | `VALIDATION_ERROR` | `from`/`to` missing or not `YYYY-MM-DD`; id not a positive integer; `limit` not an integer in 0–100; `currency` missing, not 3 letters, or not uppercase |
| 400 | `ANALYTICS_INVALID_DATE_RANGE` | `from` after `to` |
| 400 | `ANALYTICS_DATE_RANGE_TOO_LARGE` | more than 366 days |
| 401 | `UNAUTHENTICATED` | no token / bad signature / expired / malformed / missing `userId`/`role` |
| 403 | `FORBIDDEN` | missing permission or role, wrong restaurant, or wrong branch |
| 404 | `NOT_FOUND` | no such route |
| 405 | `METHOD_NOT_ALLOWED` | wrong method |
| 502 | `UPSTREAM_ERROR` | core rejected the internal permissions call (4xx, e.g. bad api-key) |
| 503 | `UPSTREAM_UNAVAILABLE` | core unreachable / 5xx on an RBAC cache miss |
| 503 | `UNHEALTHY` | `/health` with Mongo or RabbitMQ down |
| 500 | `INTERNAL_ERROR` | unexpected error or panic (details only in logs) |
| — | `ANALYTICS_INVALID_EVENT_PAYLOAD` (422) | event side only: dead-letters the message, never an HTTP response |

Declared in `lib/errors/apperror.go` (cross-cutting), `lib/coreclient/client.go` (upstream), and `app/analytics/errors.go` (module).

---

## Inbound event contracts

The envelope is identical for every event: `{eventId, eventType, occurredAt, aggregateType, aggregateId, region, payload}`. The `eventId` is the dedupe key.

### order.events → `analytics-service.order-events` (bindings `order.#`, `payment.#`)

| Event | Producer | Payload fields used | Effect |
| --- | --- | --- | --- |
| `order.placed` | order.service.ts (COD) / kashier-webhook.service.ts (online, after capture) | `orderId, restaurantId, branchId, total, currency, items[{productId, quantity, lineTotal}], placedAt` | restaurant + branch + platform day: `orders_count+1`, `revenue_sum+=total`; product day per item (one `BulkWrite`); timeline `placed_*`; settles any waiting delivery/rejection |
| `order.delivered` | settlement.service.ts | `orderId, restaurantId, branchId, deliveredAt` (**no placedAt**) | timeline `delivered_at`; once placed is known (in any order): `delivery_ms_sum += deliveredAt−placedAt`, `delivery_ms_count+1` on the placed day |
| `order.rejected` | order.service.ts status transition | `orderId, restaurantId, branchId, occurredAt` | timeline `rejected_at`; once placed is known: `rejected_count+1`, `rejected_revenue_sum += total` on the placed day |
| `payment.completed` | kashier-webhook.service.ts (written **before** the online `order.placed`) | `restaurantId, branchId, amount, currency, completedAt` | restaurant + branch + platform day of capture: `payments_count+1`, `payments_sum+=amount` |
| `order.accepted`, `order.cancelled`, `payment.failed` | — | — | logged + acked (no handler yet) |

Missing or invalid required fields → `ANALYTICS_INVALID_EVENT_PAYLOAD` → DLQ.

### core.events → `analytics-service.core-events` (binding `rbac.#`)

| Event | Payload | Effect |
| --- | --- | --- |
| `rbac.permissions_changed` | `{"role": "owner"}` or `{}` | drop that role (or all roles) from the RBAC cache. No dedupe needed: the handler is idempotent. ⚠️ core does not publish it yet. |
