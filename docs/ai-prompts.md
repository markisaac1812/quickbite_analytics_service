# AI prompts for the homework

> **The skill you're learning isn't Go.** It's directing an AI through a non-trivial implementation in a language you're new to, by anchoring it to a codebase you already know.

You know `order-service` and `core-service` cold. The AI doesn't, and it'll happily invent its own conventions in a language you can't review well yet. Each prompt below does three things:

1. **Anchor**: make the AI read the existing code (Node *and* Go) before it writes anything.
2. **Prove**: make it explain the existing pattern back to you, so you can catch a misunderstanding before it becomes code.
3. **Constrain**: name the exact files, the layering rules, and the acceptance check, so "done" is something you can verify by running commands.

---

## 0. Session opener (paste once per session)

```text
You're working in the Go service at quickbite/analytics-service. It mirrors two Node/TS
services in the same monorepo (core-service, order-service).

Before you write ANY code:
1. Read analytics-service/CLAUDE.md fully. Those rules are binding.
2. Read docs/node-to-go-mapping.md and docs/system-design.md.
3. Skim the existing vertical slice end to end:
   app/analytics/{types,errors,enums}.go, entity/, repository/, service/, controller/,
   dto/, eventhandlers/, and lib/boot/boot.go.

Then reply with ONLY:
- a 10-line summary of the layering rules (pkg/lib/app) in your own words,
- the exact path an order.placed event takes from RabbitMQ to Mongo (file → function),
- the exact path GET /api/v1/analytics/restaurants/{id}/days takes (middleware → controller → service → repo),
- anything in the rules you think is ambiguous.

Do not propose code yet.
```

Read the answer. If the event path skips `MarkSeen`, or it puts a collection name in `pkg/`, correct it **now**. A misunderstanding costs one message to fix here and a whole diff to fix later.

---

## 1. Meta-patterns (reuse these in every prompt)

### 1a. "Prove you understood the existing code"

```text
Before writing anything, show me how the existing code already does the closest
thing to what I'm asking. Quote the relevant lines from <file> and explain:
- which layer each piece lives in and why,
- which interface it satisfies (if any) and where that interface is declared,
- how it's wired in lib/boot/boot.go.
Then list the files you intend to create or change. No code yet.
```

### 1b. "Node analogue first, then the Go translation"

```text
Find the Node code in order-service that plays the same role as <thing>.
Show it (file + the 10–20 relevant lines). Then show the Go translation following
docs/node-to-go-mapping.md, and call out every place where you DEVIATE from a
literal translation and why (cite §4 "When to break the mapping" if it applies).
```

### 1c. "Small diff, verified"

```text
Implement only step <N>. After the change:
- run `go build ./... && go vet ./...`,
- run <the acceptance command>,
- paste the actual output.
Don't claim success from reasoning. If a command fails, fix it and re-run before replying.
```

### 1d. "Review against the rules"

```text
Review your diff against CLAUDE.md §4 (layering), §6 (response shape), §7 (DB rules),
§10 (what to avoid). For each rule, say "ok" or quote the offending line.
Also run:
  go list -f '{{.ImportPath}}: {{join .Imports " "}}' ./pkg/... | grep quickbite/analytics-service/(lib|app)
  go list -f '{{.ImportPath}} {{join .Imports " "}}' ./app/... ./lib/... ./pkg/... | grep go.mongodb.org
and paste the output.
```

### 1e. Red flags to push back on

- "I added a `utils` package." Ask: which layer does this belong to?
- An interface declared next to its only implementation. Ask: who consumes it?
- `bson.M` for a sort or an index key. Ask for `bson.D`.
- `context.Background()` inside a handler, service, or repo.
- `_ = err`, or `if err != nil { log...; return nil }` in an event handler (that silently acks a lost event).
- Any new dependency in `go.mod`.
- "This should work." Ask for the command output.

---

## 2. Homework prompts (round 2)

Round 1 (branch/product/platform aggregates, delivered/rejected/payment handlers, 8 endpoints, the RBAC consumer, backfill) is **built**. Use it as the reference implementation: every prompt below says which existing file to mirror. Each prompt assumes the session opener (§0) has already been run.

### H1. Go unit tests

```text
Goal: unit tests with no Mongo or RabbitMQ.

Prove understanding first: list every interface the service and the dispatcher depend on
(analytics.Stores members, coreevents.EventDeduper, coreevents.TxRunner) and where each is declared.

1. app/analytics/service/*_test.go with fakes for the five stores:
   - mergeLines merges duplicate productIds and rejects quantity<=0;
   - OnOrderPlaced buckets 23:30 at UTC+3 into the UTC day;
   - settle() applies delivery exactly once when ClaimDelivery returns a timeline, never when it returns nil;
   - reports: from>to, 367 days, avg/rate rounding, empty → non-nil slices.
2. lib/coreevents/dispatcher_test.go: no handler → MarkSeen NOT called; dup → handler NOT called;
   handler error without tx → Forget called; with a fake TxRunner → Forget NOT called.
3. lib/rbac/middleware_test.go with httptest: RequireBranchAccess (owner bypass, branchIds), RequireRole.

Acceptance: `go test ./...` passes; paste the output. play/e2e still passes.
```

### H2. `order.cancelled`, `order.accepted`, `payment.failed`

```text
Goal: cancel rate, time-to-accept, payment failure rate.

Prove understanding first:
- Walk me through how order.rejected flows today: handlers.go → ingest.service.go OnOrderRejected →
  order_timeline.repo.go RecordRejected/ClaimRejection → applyRejection. Why is it bucketed on the
  PLACED day and why does it need the timeline?
- Which of the three new events are self-contained (like payment.completed) and which need the
  timeline (like rejected)? Justify each.

Then implement by mirroring those files exactly: enums → payload/input types → DayDelta fields +
counters.go $inc + DayCounters → timeline fact + claim (if needed) → service → handler → derived
fields in reports + DTOs → docs/api-contracts.md → new e2e checks (copy the "rejected BEFORE placed" block).

Acceptance: `go run ./play/e2e` green, including your new out-of-order checks.
```

### H3. core-service publishes `rbac.permissions_changed`

```text
Goal: core emits the event analytics and order-service already consume.

Prove understanding first:
- Show core-service lib/events/{event-types,outbox.repo,outbox-drain}.ts and one existing call site of
  insertOutboxEvent in core. Show order-service's PermissionCacheService.handlePermissionsChanged and
  analytics' rbac.Cache.HandlePermissionsChanged. What payload do both expect?
- Core has no API that changes role permissions today. Propose the smallest one (owner-only,
  PUT /api/rbac/roles/:role/permissions) and wait for my approval.

Then: the mutation and the outbox row go in ONE knex trx (explicit begin/commit/rollback, per CLAUDE.md).
Acceptance: call the new core endpoint; analytics logs "rbac.permissions_changed -> permission cache cleared";
the next analytics request triggers a core permissions fetch.
```

### H4. Bucket the hot `agg_platform_day` row

```text
Goal: remove WriteConflict retries on the platform row under transactions.

Prove understanding first: explain why every event touches the same platform document, why that
is cheap without transactions and expensive with them, and what the driver does on WriteConflict.
Then measure first: publish 2,000 order.placed events concurrently (extend play/publish-test with
-n and -concurrency) and report the consumer's p95 apply time with transactions on vs off.

Only if the numbers justify it: key (date, currency, bucket = restaurant_id % 16), sum buckets in
PlatformDayRepo.FindRange with a $group, update indexes.go with a comment. Backfill -reset rebuilds it.
Acceptance: e2e green; the same load test shows the improvement (paste both runs).
```

### H5. DLQ replay tool

```text
Goal: play/replay-dlq moves messages from a DLQ back to their exchange with the ORIGINAL routing key.

Prove understanding first: where is the original routing key on a dead-lettered message (x-death)?
Why does a replay work for an event that failed (tx rollback or Forget) but is skipped for one that
was applied? Why is replaying a malformed envelope pointless?

Acceptance: publish -raw garbage and an invalid payload (DLQ +2); `replay-dlq -dry-run` lists both with
their routing keys; `replay-dlq` cycles them (they land back in the DLQ, still invalid); a fixed
event replays and is applied once.
```

### H6. order-service export endpoint for backfill

```text
Goal: GET /internal/events?after=<id>&limit=<n> on order-service (api-key guarded, per region), and
cmd/backfill-aggs -source http that pages through it instead of reading a file.

Prove understanding first: show docs/backfill.md's psql export and lib/boot/backfill.go. Where does
the input reading end and Dispatcher.Apply begin? The new source must plug in there without touching
the dispatcher. Mirror order-service's internal route conventions (CLAUDE.md §8 "Internal
service-to-service auth": no parallel internal/ module, caller-agnostic repo names).

Acceptance: a rebuild through -source http equals a rebuild from the psql file (compare snapshots,
the same way play/e2e does).
```

## 3. When the AI goes off the rails

- **It rewrites infrastructure** ("I refactored the broker to…"). Stop it: "Revert changes outside the files listed in your plan."
- **It invents a convention.** Ask: "Where in order-service or this repo is this pattern used? Quote it." No quote means no new convention.
- **It argues with a rule.** Fine, if the argument is good. Ask it to write the argument as a proposed edit to `CLAUDE.md` §4 ("when to break the mapping") and decide yourself. Don't let it break the rule silently in code.
- **It loops on a failing build.** Ask for the exact compiler error, the file and line, and its hypothesis *before* the next edit.
- **It says done.** Ask for the acceptance output. "I built the consumer" and "I published an event and saw the Mongo document change" are different claims.
