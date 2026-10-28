# Node → Go mapping

You already know how `core-service` and `order-service` are built. This service is the **same architecture in a different language**. This doc puts the two side by side, layer by layer, using real code from both repos, so that when you see a Go file you can say "oh, that's `errorHandler.ts`".

How to read it:

1. **§1 Idiom table**: language-level translations (class → struct, Promise → error return, …).
2. **§2 Layer by layer**: for each layer, the Node code you know, then the Go code in this repo, then the *why*.
3. **§3 Gotchas**: things that bite people porting TS to Go.
4. **§4 When to break the mapping**: places where copying Node literally would be wrong in Go, and what we did instead.

---

## 1. Idiom table

### Language

| TypeScript / Node | Go | Notes |
| --- | --- | --- |
| `class OrderService { constructor(private repo) {} }` | `type Service struct{ days RestaurantDayStore }` + `func New(days …) *Service` | No classes. A struct holds state; a constructor function builds it. |
| method `async getX(): Promise<T>` | `func (s *Service) GetX(ctx context.Context) (T, error)` | Methods have an explicit receiver. Pointer receiver (`*Service`) for anything with state. |
| `Promise<T>` / `await` | `(T, error)` return; plain sequential call | Go is synchronous-looking; concurrency comes from goroutines, not from `async`. |
| `throw new AppError(...)` | `return analytics.ErrInvalidDateRange` | Errors are values. Every call that can fail returns `error` as its last result. |
| `try { … } catch (e) { … }` | `if err != nil { return err }` | Handle or return at every call site. No unwinding. |
| `err instanceof AppError` | `errors.As(err, &appErr)` | Works through wrapping (`%w`). |
| `err === NotFoundError` | `errors.Is(err, apperr.ErrNotFound)` | `AppError.Is` matches by `Code`, so copies match their sentinel. |
| `interface IMessageBroker {…}` + `implements` | `type Broker interface {…}`, no `implements` | Satisfaction is **implicit** (structural). Define interfaces where they're **used**, not where they're implemented. |
| `export` / not exported | Capitalized / lowercase identifier | `Service` is public; `avgMinor` is package-private. |
| module file = unit of privacy | **package** (directory) = unit of privacy | Everything in `app/analytics/service/` sees everything else in it. |
| `import {x} from "../../lib/logger"` | `import ".../lib/logger"` then `logger.FromContext` | Always qualified by package name. |
| decorators (`@IsString()`, `@injectable()`) | struct tags (`validate:"required"`, `json:"from"`, `bson:"date"`, `env:"PORT"`) | Tags are inert strings read by libraries via reflection. |
| `string \| undefined` / optional `?` | zero value (`""`, `0`, `nil`) or a pointer (`*int64`) | Use a pointer only when "absent" ≠ "zero" (e.g. `RestaurantID *int64` in claims). |
| `number` | `int64` for ids and money, `int` for counts and sizes, `float64` almost never | JSON numbers decode into whichever type you declare. |
| `Date` | `time.Time` (+ `.UTC()`) | Format with layouts based on the reference time `2006-01-02T15:04:05Z07:00`. |
| `JSON.parse` / `JSON.stringify` | `json.Unmarshal` / `json.Marshal` into **typed structs** | Unknown fields are ignored; missing fields become zero values. |
| `Map<string, T>` | `map[string]T` | Not safe for concurrent writes; guard with `sync.Mutex`/`RWMutex`. |
| `as const` object of strings | `const ( EventOrderPlaced = "order.placed" )` | |
| `enum Status { … }` | `type Status string` + consts | We use plain string consts here; no enums needed in the slice. |
| generics `Envelope<T>` | `Envelope[T any]` | Since Go 1.18. Use sparingly. |
| `setTimeout`, `setInterval` | `time.After`, `time.NewTicker` | |
| event loop concurrency | goroutines + channels | Handlers **really run in parallel**. Shared state needs locks. |
| `process.on("SIGTERM")` | `signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)` | |
| `process.env.X` | `os.Getenv` (**only** inside `lib/config`) | Everything else receives config via constructors. |
| `req` / `res` (Express) | `r *http.Request` / `w http.ResponseWriter` | |
| `req.user` | `appcontext.ClaimsFrom(r.Context())` | Request-scoped values ride on `context.Context`. |
| `next()` middleware | `func(next http.Handler) http.Handler` | Wrap, don't call-and-continue. |

### Libraries

| Concern | Node (core/order) | Go (this service) |
| --- | --- | --- |
| HTTP framework | `express` v5 | `net/http` + `chi/v5` |
| Validation | `class-validator` decorators | `validator/v10` struct tags |
| Env | `zod` schema + `dotenv` | `caarlos0/env/v11` struct tags + `lib/config/dotenv.go` |
| DB | `knex` (query builder, not ORM) | `mongo-driver` (driver, not ODM) |
| DI | `tsyringe` container + `TOKENS` | none: constructors called in `lib/boot` |
| Logging | custom `Logger` → `console.log(JSON)` | `log/slog` JSON handler |
| JWT | `jsonwebtoken` | `golang-jwt/jwt/v5` |
| AMQP | `amqplib` + `amqp-connection-manager` | `amqp091-go` + our reconnect loop |
| IDs | `uuid` | `google/uuid` |
| HTTP client | `fetch` + `pkg/utils/retry.ts` | `pkg/httpclient` (net/http + retry) |

---

## 2. Layer by layer

### 2.1 Config

**Node: `order-service/src/lib/config/env.ts`**

```ts
config({path: path.resolve(__dirname, "../../../.env")});
const baseSchema = z.object({
    PORT: z.string().default("4000"),
    ACCESS_SECRET: z.string(),
    RABBITMQ_URL: z.string(),
    RABBITMQ_CORE_EVENTS_BINDINGS: z.string().default("product.#,branch.#,restaurant.#,rbac.#"),
});
const parsed = baseSchema.parse(process.env);
export const env = {
    port: Number(parsed.PORT),
    rabbit: { url: parsed.RABBITMQ_URL, bindings: parsed.RABBITMQ_CORE_EVENTS_BINDINGS.split(",") },
};
```

**Go: `lib/config/env.go`**

```go
type Config struct {
    Port   int `env:"PORT" envDefault:"4002"`
    JWT    JWTConfig
    Rabbit RabbitConfig
}
type JWTConfig struct {
    AccessSecret string `env:"ACCESS_SECRET,required,notEmpty"`
}
type RabbitConfig struct {
    URL      string   `env:"RABBITMQ_URL,required,notEmpty"`
    Bindings []string `env:"RABBITMQ_ORDER_EVENTS_BINDINGS" envDefault:"order.#,payment.#" envSeparator:","`
}

func Load() (Config, error) {
    if err := loadDotEnv(".env"); err != nil { return Config{}, err }
    var cfg Config
    if err := env.Parse(&cfg); err != nil { return Config{}, fmt.Errorf("config: %w", err) }
    return cfg, nil
}
```

**Why:** the zod schema and the `env` object are two things in Node (parse, then reshape). In Go the struct is **both** the schema and the typed result. The `Number(...)` and `.split(",")` conversions disappear: the library converts to the field's type (`int`, `[]string`, `time.Duration` such as `"5m"`). There is no global `env` object; `boot` passes `cfg.X` into each constructor, so a package's dependencies are visible in its constructor signature.

---

### 2.2 Logger

**Node: `lib/logger/logger.ts`**

```ts
log(level, message, metadata = {}) {
    console.log(JSON.stringify({level, message, timestamp: Date.now(), ...metadata}));
}
export const logger = new Logger();
// usage
logger.error("outbox publish failed", {region, id: row.id, error: msg});
```

**Go: `lib/logger/logger.go`**

```go
func New(level, appEnv string) *slog.Logger {
    h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parseLevel(level)})
    return slog.New(h).With("service", "analytics-service", "env", appEnv)
}
func FromContext(ctx context.Context) *slog.Logger { … }
// usage
logger.FromContext(ctx).Info("agg_restaurant_day incremented", "restaurant_id", id, "date", date)
```

**Why:** `slog` is the stdlib structured logger: JSON out, levels, attributes. Metadata is passed as alternating key/value args instead of an object literal. The important upgrade is **`FromContext`**: the correlation middleware stores a child logger already tagged `correlation_id`, so every log line a request causes, deep in the service, carries it without being passed around. Node would need `AsyncLocalStorage` for that.

---

### 2.3 Request context (`express.d.ts`)

**Node: `lib/types/express.d.ts`**

```ts
declare namespace Express {
    interface Request {
        correlationId?: string;
        user?: { userId: number; role: string; restaurantId?: number; restaurantRole?: string; };
    }
}
// write: req.user = verifyAccessToken(token)      read: req.user!.restaurantRole
```

**Go: `lib/appcontext/context.go`**

```go
type Claims struct {
    UserID         int64  `json:"userId"`
    Role           string `json:"role"`
    RestaurantID   *int64 `json:"restaurantId,omitempty"`
    RestaurantRole string `json:"restaurantRole,omitempty"`
}
type ctxKey int
const ( claimsKey ctxKey = iota; correlationIDKey )

func WithClaims(ctx context.Context, c Claims) context.Context { return context.WithValue(ctx, claimsKey, c) }
func ClaimsFrom(ctx context.Context) (Claims, bool) { c, ok := ctx.Value(claimsKey).(Claims); return c, ok }
```

**Why:** Go's `*http.Request` can't be monkey-patched with new fields. Request-scoped data rides on `r.Context()`. The **unexported key type** guarantees no other package can read or overwrite our values by accident, which is the type-safety `declare namespace` gives you in TS. The same `ctx` flows into services and repositories, so cancellation (client disconnected, shutdown) reaches Mongo too.

---

### 2.4 Errors

**Node: `lib/error/AppError.ts`, `app/order/errors.ts`, `lib/error/errorHandler.ts`**

```ts
export class AppError extends Error { constructor(message: string, public statusCode = 500, public isOperational = true) {…} }
export const OrderNotFoundError = new AppError("OrderNotFound", 404);

// service
if (!order) throw OrderNotFoundError;

// app.ts — last middleware
export function errorHandler(err, req, res, _next) {
    const appErr = err instanceof AppError ? err : new AppError(err.message, 500, false);
    if (appErr.isOperational) return res.status(appErr.statusCode).json({error: appErr.message});
    return res.status(500).json({error: "Something went wrong"});
}
```

**Go: `lib/errors/apperror.go`, `app/analytics/errors.go`, `lib/errors/handler.go`**

```go
type AppError struct { Code string; Status int; Message string; Err error }
func New(code string, status int, msg string) *AppError

var ErrInvalidDateRange = apperr.New("ANALYTICS_INVALID_DATE_RANGE", 400, "from must be on or before to")

// service
if in.From.After(in.To) { return nil, analytics.ErrInvalidDateRange }

// routes.go
r.Get("/restaurants/{restaurantId}/days", apperr.Wrap(log, ctrl.ListRestaurantDays))

func Wrap(log *slog.Logger, h HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if err := h(w, r); err != nil { Write(w, r, log, err) }  // AppError → its status; else 500
    }
}
```

**Why:** Express has a global error middleware because `throw` unwinds to it. Go has no throw, so the controller **returns** the error and `Wrap` is the error middleware, applied per route. "Operational vs not" becomes "is it an `*AppError`?": anything else is a bug and renders a generic 500 while the real error is logged. We added a stable machine-readable **`Code`** that Node lacks; it's what the envelope's `error.code` shows and what clients branch on. Need a more specific message? `ErrValidation.WithMessage("from is required")` returns a **copy**. Never mutate a package-level sentinel: it's shared by every concurrent request.

---

### 2.5 Response envelope

**Node: `lib/http/response.ts`**

```ts
export function sendSuccess<T>(res: Response, data: T, statusCode = 200, meta?: object) {
    res.status(statusCode).json({success: true, data, ...(meta && {meta})});
}
```

**Go: `lib/http/response.go` (package `httpx`)**

```go
func SendSuccess(w http.ResponseWriter, status int, data any) error {
    return writeJSON(w, status, SuccessEnvelope{Success: true, Data: data})
}
```

**Why:** it's the same function. Two Go details: it returns the encode error so the controller can just `return httpx.SendSuccess(...)`; and the package is named `httpx` so it doesn't shadow `net/http`.

---

### 2.6 DTOs: request (validation)

**Node: `dto/*.request.dto.ts` + `validateBody`**

```ts
export class CreateOrderDTO {
    @IsInt() @Min(1) branchId!: number;
    @IsIn(["cod", "online"]) paymentMethod!: string;
}
const body = await validateBody(CreateOrderDTO, req.body);
```

**Go: `app/analytics/dto/analytics.request.go` + `lib/validation`**

```go
type ListRestaurantDaysRequest struct {
    RestaurantID int64  `json:"restaurantId" validate:"gt=0"`
    From         string `json:"from" validate:"required,datetime=2006-01-02"`
    To           string `json:"to"   validate:"required,datetime=2006-01-02"`
}
req := dto.ListRestaurantDaysRequest{RestaurantID: parseID(chi.URLParam(r, "restaurantId")), From: q.Get("from"), To: q.Get("to")}
if err := validation.Struct(req); err != nil { return err }  // 400 VALIDATION_ERROR: "from is required"
```

**Why:** decorators become struct tags. There's no `class-transformer` step: the controller copies path and query values into the struct explicitly (for a JSON body, `json.NewDecoder(r.Body).Decode(&req)`). `validation.Struct` reports fields by their **JSON** name, so error messages match the wire contract, not Go field names.

### 2.7 DTOs: response

**Node: `dto/*.response.dto.ts`**

```ts
export class OrderResponseDTO {
    id!: string; subtotal!: number; currency!: Currency;
    static from(order: OrderEntity, items: OrderItemEntity[]): OrderResponseDTO { … }
}
sendSuccess(res, OrderResponseDTO.from(order, items));
```

**Go: `app/analytics/dto/analytics.response.go`**

```go
type RestaurantDayResponse struct {
    Date          string `json:"date"`
    OrdersCount   int64  `json:"ordersCount"`
    RevenueMinor  int64  `json:"revenueMinor"`
    Currency      string `json:"currency"`
    AvgOrderMinor int64  `json:"avgOrderMinor"`
}
func RestaurantDayFrom(row analytics.RestaurantDayRow) RestaurantDayResponse { … }
func RestaurantDaysFrom(rows []analytics.RestaurantDayRow) []RestaurantDayResponse { out := make(…, 0, len(rows)); … }
```

**Why:** `static from()` becomes a package-level `XxxFrom()` function. `json:"…"` tags give the camelCase wire names while Go fields stay PascalCase. `make(..., 0, n)` guarantees `[]` rather than `null` for an empty list (see gotcha §3.4).

---

### 2.8 Entities

**Node: `entity/order.entity.ts`**

```ts
export class OrderEntity {
    id!: number; publicId!: string; total!: number; createdAt!: Date;
    constructor(data: Partial<OrderEntity>) { Object.assign(this, data); }
}
```

**Go: `app/analytics/entity/restaurant_day.entity.go`**

```go
type RestaurantDay struct {
    RestaurantID int64     `bson:"restaurant_id"`
    Date         string    `bson:"date"`
    OrdersCount  int64     `bson:"orders_count"`
    RevenueSum   int64     `bson:"revenue_sum"`
    UpdatedAt    time.Time `bson:"updated_at"`
}
```

**Why:** a plain struct. The `bson` tags do what Knex's `toEntity(row)` mapper did (snake_case column → camelCase field), declaratively. A struct literal (`entity.RestaurantDay{…}`) replaces `new X(partial)`.

---

### 2.9 Repositories

**Node: `repository/*.repo.ts` (functions + Knex)**

```ts
const ORDER_COLUMNS = ["id", "public_id", "status", "total", "created_at"];
export async function findByPublicId(publicId: string, conn: Knex = db): Promise<OrderEntity | null> {
    const row = await conn("orders").select(ORDER_COLUMNS).where({public_id: publicId}).first();
    return row ? toEntity(row) : null;
}
```

**Go: `app/analytics/repository/restaurant_day.repo.go` (struct + mongo-driver)**

```go
var restaurantDayProjection = bson.D{{Key: "_id", Value: 0}, {Key: "restaurant_id", Value: 1}, {Key: "date", Value: 1}, …}

type RestaurantDayRepo struct{ coll *mongo.Collection }
func NewRestaurantDayRepo(db *mongo.Database) *RestaurantDayRepo { return &RestaurantDayRepo{coll: db.Collection(CollRestaurantDay)} }

func (r *RestaurantDayRepo) FindRange(ctx context.Context, id int64, from, to string) ([]entity.RestaurantDay, error) {
    filter := bson.D{{Key: "restaurant_id", Value: id}, {Key: "date", Value: bson.D{{Key: "$gte", Value: from}, {Key: "$lte", Value: to}}}}
    cur, err := r.coll.Find(ctx, filter, options.Find().SetProjection(restaurantDayProjection).SetSort(bson.D{{Key: "date", Value: 1}}))
    if err != nil { return nil, fmt.Errorf("agg_restaurant_day find: %w", err) }
    rows := []entity.RestaurantDay{}
    return rows, cur.All(ctx, &rows)
}

func (r *RestaurantDayRepo) Increment(ctx context.Context, in analytics.IncRestaurantDayInput) error {
    _, err := r.coll.UpdateOne(ctx, filter, bson.D{
        {Key: "$inc", Value: bson.D{{Key: "orders_count", Value: in.OrdersDelta}, {Key: "revenue_sum", Value: in.RevenueDelta}}},
        {Key: "$setOnInsert", Value: bson.D{{Key: "currency", Value: in.Currency}}},
    }, options.Update().SetUpsert(true))
    …
}
```

| Knex idea | Mongo-driver equivalent |
| --- | --- |
| `ORDER_COLUMNS` | `SetProjection(bson.D{…})` |
| `.where({a: 1})` | `bson.D{{Key: "a", Value: 1}}` (ordered doc; use `bson.M` only when order doesn't matter) |
| `.whereBetween` | `{$gte, $lte}` |
| `.orderBy("date")` | `SetSort(bson.D{{Key: "date", Value: 1}})` |
| `INSERT … ON CONFLICT DO UPDATE SET n = n + 1` | `UpdateOne` + `$inc` + `SetUpsert(true)` |
| unique constraint violation `23505` | `mongo.IsDuplicateKeyError(err)` |
| migration `createIndex` | `repository/indexes.go` → `EnsureIndexes` on boot |
| `conn: Knex = db` (trx passthrough) | `ctx` — inside `TxRunner.InTx` the ctx *is* a `mongo.SessionContext`, so every repo call joins the transaction (§2.20) |
| `bulkInsertItems(rows)` (one multi-row INSERT) | `BulkWrite([]WriteModel)` (§2.21) |
| `SELECT … GROUP BY … ORDER BY … LIMIT` | `Aggregate(mongo.Pipeline{$match, $group, $sort, $limit})` |
| `UPDATE … WHERE flag = false RETURNING *` | `FindOneAndUpdate(filter{flag: {$ne: true}}, {$set: {flag: true}}, ReturnDocument(After))` (atomic claim) |

**Why a struct and not functions?** Knex has a global `db` you can default to. Here the collection handle is a dependency built at boot. A struct holding it is how Go injects without a container, and it lets the service depend on a small interface (`analytics.RestaurantDayStore`) that a fake can satisfy in tests.

---

### 2.10 Services

**Node: `service/order.service.ts`**

```ts
@injectable()
export class OrderService {
    constructor(@inject(TOKENS.PaymentService) private paymentService: PaymentService) {}
    async placeOrder(body: CreateOrderDTO, user: JWTPayload): Promise<OrderResponseDTO> {
        if (!branch.acceptOrders) throw BranchNotAcceptingOrdersError;
        …
    }
}
```

**Go: `app/analytics/service/analytics.service.go`**

```go
type Service struct {
    days analytics.RestaurantDayStore   // interface, declared in the parent package
    now  func() time.Time               // injectable clock
}
func New(days analytics.RestaurantDayStore) *Service { return &Service{days: days, now: time.Now} }

func (s *Service) ListRestaurantDays(ctx context.Context, in analytics.ListRestaurantDaysInput) ([]analytics.RestaurantDayRow, error) {
    if in.From.After(in.To) { return nil, analytics.ErrInvalidDateRange }
    docs, err := s.days.FindRange(ctx, in.RestaurantID, from, to)
    if err != nil { return nil, err }
    … AvgOrderMinor: avgMinor(d.RevenueSum, d.OrdersCount) …
}
```

**Why:** `@injectable()` + `@inject(TOKEN)` become "the constructor takes its dependencies as parameters". `ctx` is always the first parameter. The service returns **service-level rows** (`RestaurantDayRow`), not DTOs and not entities: the controller does the wire mapping, and the entity never leaves the service.

---

### 2.11 Controllers

**Node: `controller/order.controller.ts`**

```ts
@injectable()
export class OrderController {
    constructor(@inject(TOKENS.OrderService) private orderService: OrderService) {}
    getOrder = async (req: Request, res: Response, next: NextFunction) => {
        try {
            const dto = await this.orderService.getOrder(req.params.id, req.user!);
            sendSuccess(res, dto);
        } catch (err) { next(err); }
    };
}
```

**Go: `app/analytics/controller/analytics.controller.go`**

```go
type Controller struct{ svc *service.Service }
func New(svc *service.Service) *Controller { return &Controller{svc: svc} }

func (c *Controller) ListRestaurantDays(w http.ResponseWriter, r *http.Request) error {
    req := dto.ListRestaurantDaysRequest{…}
    if err := validation.Struct(req); err != nil { return err }
    rows, err := c.svc.ListRestaurantDays(r.Context(), analytics.ListRestaurantDaysInput{…})
    if err != nil { return err }
    return httpx.SendSuccess(w, http.StatusOK, dto.RestaurantDaysFrom(rows))
}
```

**Why:** the arrow-function-property trick (to keep `this` bound) doesn't exist in Go. Method values like `ctrl.ListRestaurantDays` are already bound to their receiver. `try/catch → next(err)` becomes `return err`.

---

### 2.12 Routes & middleware

**Node: `app/order/routes.ts` + `lib/auth/guard.ts` + `lib/auth/rbac.ts`**

```ts
const controller = container.resolve<OrderController>(TOKENS.OrderController);
router.get("/restaurants/:restaurantId/orders",
    authenticate, rbac({resource: "orders", action: "read"}), requireRestaurantMember(),
    controller.listForRestaurant);
```

**Go: `app/analytics/controller/routes.go`**

```go
func Mount(r chi.Router, ctrl *Controller, authn *auth.Middleware, guard *rbac.Guard, log *slog.Logger) {
    r.Route("/analytics", func(r chi.Router) {
        r.Use(authn.Authenticate)
        r.With(guard.Require(analytics.PermAnalyticsRead), guard.RequireRestaurantAccess("restaurantId")).
            Get("/restaurants/{restaurantId}/days", apperr.Wrap(log, ctrl.ListRestaurantDays))
    })
}
```

| Express | chi / net/http |
| --- | --- |
| `(req, res, next) => { …; next(); }` | `func(next http.Handler) http.Handler { return http.HandlerFunc(func(w, r) { …; next.ServeHTTP(w, r) }) }` |
| `req.params.restaurantId` | `chi.URLParam(r, "restaurantId")` |
| `:restaurantId` | `{restaurantId}` |
| `router.use(mw)` | `r.Use(mw)` |
| per-route middleware list | `r.With(mw1, mw2).Get(...)` |
| `app.use("/api", routes)` | `r.Route("/api/v1", mountAPI)` |
| `res.status(401).json(...)` inside middleware | `apperr.Write(w, r, log, apperr.ErrUnauthenticated); return` |

**Why:** middleware **wraps** the next handler rather than calling `next()`. To short-circuit, write the response and **don't** call `next.ServeHTTP`. Forgetting the `return` after writing an error is the classic bug.

---

### 2.13 Auth (JWT)

**Node: `lib/auth/jwt.ts` + `guard.ts`**

```ts
export function verifyAccessToken(token: string): JWTPayload {
    try { return jwt.verify(token, env.jwt.accessSecret) as JWTPayload; }
    catch { throw NotAuthenticated; }
}
export function authenticate(req, res, next) {
    const token = req.cookies?.access_token;
    if (!token) throw NotAuthenticated;
    req.user = verifyAccessToken(token); next();
}
```

**Go: `lib/auth/jwt.go` + `middleware.go`**

```go
parser: jwt.NewParser(jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())

func (v *Verifier) Verify(token string) (appcontext.Claims, error) {
    var c jwtClaims
    if _, err := v.parser.ParseWithClaims(token, &c, func(*jwt.Token) (any, error) { return v.secret, nil }); err != nil {
        return appcontext.Claims{}, apperr.ErrUnauthenticated.WithMessage("Invalid or expired token").WithCause(err)
    }
    …
}

func (m *Middleware) Authenticate(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        token := tokenFrom(r)               // cookie access_token, then Authorization: Bearer
        claims, err := m.verifier.Verify(token)
        if err != nil { apperr.Write(w, r, m.log, err); return }
        next.ServeHTTP(w, r.WithContext(appcontext.WithClaims(r.Context(), claims)))
    })
}
```

**Why:** the token shape is identical; core signs it, and every service verifies it with the shared `ACCESS_SECRET`. `WithValidMethods` pins the algorithm (`jsonwebtoken` infers it from the secret type; Go makes you say it). The claims struct embeds `jwt.RegisteredClaims` to get `exp`/`iat` validation.

### 2.14 RBAC cache

**Node: `lib/rbac/permission-cache.service.ts`**

```ts
private cache: Map<string, {permissions: string[]; cachedAt: number}> = new Map();
getPermissions = async (roleName: string) => {
    const cached = this.cache.get(roleName);
    if (cached && Date.now() - cached.cachedAt < this.TTL) return cached.permissions;
    const permissions = await getPermissionsByRole(roleName);
    this.cache.set(roleName, {permissions, cachedAt: Date.now()});
    return permissions;
};
```

**Go: `lib/rbac/cache.go`**

```go
type Cache struct {
    fetcher PermissionFetcher   // interface; coreclient.Client satisfies it
    ttl     time.Duration
    mu      sync.RWMutex
    entries map[string]cacheEntry
}
func (c *Cache) Has(ctx context.Context, role, perm string) (bool, error) {
    if perms, ok := c.fresh(role); ok { _, has := perms[perm]; return has, nil }   // RLock
    list, err := c.fetcher.PermissionsByRole(ctx, role)
    if err != nil { return false, err }                                            // fail closed, not cached
    c.mu.Lock(); c.entries[role] = cacheEntry{…}; c.mu.Unlock()
    …
}
```

**Why:** the same algorithm with one non-negotiable addition: **the mutex**. Node runs one callback at a time; Go runs HTTP handlers on many goroutines at once, and an unguarded map write panics (`concurrent map writes`). Permissions are stored as a set (`map[string]struct{}`) so `Has` is O(1).

---

### 2.15 Sync client to core

**Node: `lib/core-client/core-client.ts`**

```ts
return retry(async () => {
    const res = await fetch(url, {method, headers: {"api-key": this.apiKey, "X-CorrelationId": req.correlationId}});
    if (res.status >= 500) throw coreUnavailableError(res.status);
    if (!res.ok) throw coreUpstreamError(res.status, await res.text());
    return res.json();
}, {attempts: 3, initialDelayMs: 50, maxDelayMs: 500});
```

**Go: `pkg/httpclient` (generic) + `lib/coreclient` (core-specific)**

```go
hc, _ := httpclient.New(httpclient.Config{BaseURL: baseURL, Timeout: 3*time.Second, Attempts: 3,
    InitialBackoff: 50*time.Millisecond, MaxBackoff: 500*time.Millisecond, Headers: map[string]string{"api-key": apiKey}})

func (c *Client) PermissionsByRole(ctx context.Context, role string) ([]string, error) {
    var res Envelope[RolePermissionsResponse]
    err := c.get(ctx, httpclient.Request{Path: "/api/internal/rbac/permissions", Query: url.Values{"role": {role}}}, &res)
    …
}
```

**Why:** Node mixed transport (retry, fetch) and domain (core's paths, error mapping) in one class. Go splits them along the `pkg`/`lib` line: `pkg/httpclient` knows HTTP and retries and could be reused for any service; `lib/coreclient` knows core's URLs, its envelope, and how to map failures to `AppError`s. Each attempt has its **own** timeout through `context.WithTimeout`.

---

### 2.16 Messaging: broker

**Node: `pkg/messaging/rabbitmq/rabbitmq.client.ts`**

```ts
const ch = this.connection!.createChannel({
    setup: async (c: ConfirmChannel) => {
        await assertTopology(c, opts);
        await c.prefetch(opts.prefetch);
        await c.consume(opts.queue, (raw) => handleMessage(c, raw, handler), {noAck: false});
    },
});
// handleMessage: try { await handler(msg) } catch { msg.nack(false) }
```

**Go: `pkg/messaging/amqp.go`**

```go
deliveries, err := ch.Consume(t.Queue, tag, false /*autoAck*/, false, false, false, nil)
for range workers {                                   // workers = prefetch
    go func() { for d := range deliveries { b.dispatch(reg, d) } }()
}
func (b *AMQPBroker) dispatch(reg consumerReg, d amqp.Delivery) {
    if err := safeHandle(reg.ctx, reg.handler, msg); err != nil { _ = d.Nack(false, false); return }  // → DLQ
    _ = d.Ack(false)
}
// reconnect: watch(conn) waits on conn.NotifyClose, redials with backoff, attachLocked() replays every consumer
```

**Why:** `amqp-connection-manager` gives Node automatic reconnection plus `setup` replay. `amqp091-go` gives you neither, so `watch` + `attachLocked` rebuild that behaviour: store every `Consume` registration and replay it on each new connection. Prefetch becomes a real **worker pool**. In Node, prefetch 32 means 32 messages interleaved on one thread; in Go it's 32 goroutines actually running in parallel.

### 2.17 Messaging: consumer

**Node: `lib/core-events/consumer.ts`**

```ts
async function handleMessage(msg) {
    const envelope = parseEnvelope(msg); if (!envelope) return msg.nack(false);
    const fresh = await cacheProvider.trySet(`core-events:dedupe:${envelope.eventId}`, "1", DEDUPE_TTL_SEC);
    if (!fresh) { msg.ack(); return; }
    const handler = handlers.get(envelope.eventType);
    if (!handler) { logger.warn("no handler"); msg.ack(); return; }
    try { await handler(envelope.payload); msg.ack(); } catch { msg.nack(false); }
}
```

**Go: `lib/coreevents/consumer.go` + `dispatcher.go`**

```go
func (c *Consumer) handle(ctx context.Context, msg messaging.Message) error {
    env, err := Decode(msg.Body)                     // malformed → error → nack → DLQ
    if err != nil { return err }
    _, err = c.dispatcher.Apply(ctx, env)            // nil → ack, error → DLQ
    return err
}

func (d *Dispatcher) Apply(ctx context.Context, env Envelope) (Outcome, error) {
    handler, ok := d.handlers[env.EventType]
    if !ok { return OutcomeNoHandler, nil }                                  // log + ack
    run := func(ctx context.Context) error {
        fresh, err := d.deduper.MarkSeen(ctx, env.EventID, env.EventType)   // Mongo, not Redis
        if err != nil { return err }
        if !fresh { return errDuplicate }                                     // ack-skip
        return handler(ctx, env)
    }
    if d.tx != nil { err = d.tx.InTx(ctx, run) }                             // all-or-nothing
    else if err = run(ctx); err != nil { _ = d.deduper.Forget(ctx, env.EventID) } // replayable
    …
}
```

**Why:** the same pipeline, with deliberate differences (see §4): the **broker** acks/nacks based on the returned error, the handler lookup happens **before** dedupe, and the dedupe + handler run inside **one Mongo transaction** when the server supports it (otherwise a failed handler **forgets** its marker). `EventDeduper` and `TxRunner` are interfaces declared in `lib/coreevents` and implemented by `app/analytics/repository.EventIDsRepo` and `pkg/mongo.TxRunner`, which is how `lib/` stays free of `app/` imports.

The pipeline lives in a broker-free **`Dispatcher`**. `Consumer` only binds it to a queue, so `cmd/backfill-aggs` replays files through the exact same `Dispatcher.Apply`. Node had one function per queue; Go gets one dispatcher per queue (order events: Mongo dedupe + transactions; core events: `NoDedupe`, no transaction).

### 2.18 Event handler registration

**Node: `app/order/core-events.handlers.ts`**

```ts
export function registerOrderModuleCoreEventHandlers() {
    const coreData = container.resolve<CoreDataCacheService>(TOKENS.CoreDataCacheService);
    registerHandler("product.stock.changed", coreData.handleProductStockChanged);
}
```

**Go: `app/analytics/eventhandlers/handlers.go`**

```go
func Register(d *coreevents.Dispatcher, svc *service.Service) error {
    table := map[string]coreevents.EventHandler{
        analytics.EventOrderPlaced:      onOrderPlaced(svc),
        analytics.EventOrderDelivered:   onOrderDelivered(svc),
        analytics.EventOrderRejected:    onOrderRejected(svc),
        analytics.EventPaymentCompleted: onPaymentCompleted(svc),
    }
    for t, h := range table { if err := d.Register(t, h); err != nil { return err } }
    return nil
}
func onOrderPlaced(svc *service.Service) coreevents.EventHandler {
    return func(ctx context.Context, env coreevents.Envelope) error {
        var p analytics.OrderPlacedPayload
        if err := json.Unmarshal(env.Payload, &p); err != nil { return analytics.ErrInvalidEventPayload.WithCause(err) }
        return svc.OnOrderPlaced(ctx, analytics.OnOrderPlacedInput{…})
    }
}
```

**Why:** Node passed `payload: unknown` and the service cast it. Go decodes into a **typed payload struct** at the edge (`json.RawMessage` → `OrderPlacedPayload`), so the service only ever sees a validated, typed input. The registry is a map on a `Consumer` value, not a module-level `Map`: no global state.

---

### 2.19 DI & bootstrap

**Node: `lib/di/container.ts` + `server.ts`**

```ts
container.registerSingleton<OrderService>(TOKENS.OrderService, OrderService);
container.registerSingleton<OrderController>(TOKENS.OrderController, OrderController);
// server.ts
server.listen(env.port, async () => {
    registerOrderModuleCoreEventHandlers();
    messageBroker.connect().then(() => startCoreEventsConsumer(messageBroker));
});
process.on("SIGTERM", shutdown);
```

**Go: `cmd/api/main.go` + `lib/boot/boot.go`**

```go
// main.go
func main() { if err := boot.Run(); err != nil { os.Exit(1) } }

// boot.go: dependency order is just statement order
mongoClient, db, err := mongopkg.Connect(bootCtx, …)
repository.EnsureIndexes(bootCtx, db)
broker := messaging.NewAMQPBroker(…); broker.Connect(ctx)
core, _ := coreclient.New(…)
guard  := rbac.NewGuard(rbac.NewCache(core, cfg.RBAC.CacheTTL), log)
tx, _  := txRunner(ctx, cfg.Mongo.Transactions, mongoClient, log)   // nil on a standalone mongod
svc    := service.New(analytics.Stores{Restaurants: repository.NewRestaurantDayRepo(db), Branches: …, Products: …, Platform: …, Timelines: …})
ctrl   := controller.New(svc)
orderEvents := coreevents.NewDispatcher(repository.NewEventIDsRepo(db), tx, timeout, log)
eventhandlers.Register(orderEvents, svc)
coreevents.NewConsumer(broker, orderEvents, orderTopology, log).Start(ctx)
coreEvents := coreevents.NewDispatcher(coreevents.NoDedupe{}, nil, timeout, log)
coreEvents.Register(rbac.EventPermissionsChanged, permCache.HandlePermissionsChanged)
coreevents.NewConsumer(broker, coreEvents, coreTopology, log).Start(ctx)
srv.ListenAndServe()  … <-ctx.Done() … srv.Shutdown → broker.Close → mongo Disconnect
```

**Why:** a DI container solves "who builds what, in what order" at runtime by reflection. Go answers it at **compile time** with ordinary code: if `service.New` needs a store, you can't call it without one, and the compiler tells you. The container's one legitimate job, being the only place that knows every concrete type, is done by `lib/boot`, which is also why it's the one `lib/` package allowed to import `app/`.

### 2.20 Transactions

**Node: the explicit pattern from order-service's CLAUDE.md**

```ts
const trx = await db.transaction();
try {
    await someRepo(..., trx);      // every repo takes `conn: Knex = db`
    await anotherRepo(..., trx);
    await trx.commit();
} catch (err) {
    await trx.rollback();
    throw err;
}
```

**Go: `pkg/mongo/tx.go` + `lib/coreevents/dispatcher.go`**

```go
func (t *TxRunner) InTx(ctx context.Context, fn func(ctx context.Context) error) error {
    sess, _ := t.client.StartSession()
    defer sess.EndSession(context.WithoutCancel(ctx))
    _, err := sess.WithTransaction(ctx, func(sc mongo.SessionContext) (any, error) { return nil, fn(sc) })
    return err                      // fn's error → abort; nil → commit
}

// dispatcher: dedupe + handler in one transaction
err = d.tx.InTx(ctx, func(ctx context.Context) error {
    fresh, err := d.deduper.MarkSeen(ctx, env.EventID, env.EventType)   // joins the tx via ctx
    if !fresh { return errDuplicate }                                     // abort, report duplicate
    return handler(ctx, env)                                              // every repo write joins the tx
})
```

**Why:** in Knex the trx is an **argument** you thread through every repo call. In the Mongo driver the session rides on the **`ctx`**: `mongo.SessionContext` *is* a `context.Context`, so repositories written as `func (r *Repo) X(ctx, …)` join the transaction with zero code changes. Two consequences:
- Calling `context.Background()` inside a handler silently **escapes** the transaction.
- `WithTransaction` **retries** `fn` on transient errors (e.g. `WriteConflict`), so `fn` must have no side effects outside Mongo: no HTTP calls, no publishing.

Transactions need a replica set. `MONGO_TRANSACTIONS=auto` detects it at boot with the `hello` command and falls back (with a warning) on a standalone mongod.

### 2.21 Batch writes (line items)

**Node: order-service `bulkInsertItems`** is one multi-row `INSERT` instead of N inserts.

**Go: `repository/product_day.repo.go`**

```go
models := make([]mongo.WriteModel, 0, len(lines))
for _, l := range lines {   // lines already merged by productId in the service
    models = append(models, mongo.NewUpdateOneModel().
        SetFilter(bson.D{{Key: "product_id", Value: l.ProductID}, {Key: "date", Value: date}}).
        SetUpdate(bson.D{{Key: "$inc", Value: …}, {Key: "$setOnInsert", Value: …}}).
        SetUpsert(true))
}
_, err := r.coll.BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false))
```

**Why:** an order with 12 items is 1 round trip, not 12. `Ordered(false)` lets the server apply independent upserts without stopping at the first error. The whole event still fails, and rolls back in tx mode, if any model fails. Lines are merged by `productId` first, so no two models fight over the same row.

---

## 3. Common gotchas when porting

1. **Forgetting `return` after writing an error in middleware.** `apperr.Write(w, r, log, err)` without `return` falls through to `next.ServeHTTP`: a double write, and `http: superfluous response.WriteHeader` in the logs. Express's `throw` hid this from you.
2. **Ignoring an error.** `rows, _ := repo.Find(...)` compiles. Then a Mongo outage looks like "no data". Treat every `_` on an error as a code-review flag.
3. **Concurrent map access.** Node maps are safe because of the single thread. Go maps written from two goroutines **crash the process**. Any map touched by handlers needs a `sync.Mutex`/`RWMutex` (see `rbac.Cache`), or must be read-only after boot (see `Consumer.handlers`: registered before `Start`, then only read).
4. **`nil` slice → JSON `null`.** `var out []X; json.Marshal(out)` → `null`, not `[]`. Build response slices with `make([]X, 0, n)`.
5. **Zero values aren't "undefined".** A missing JSON field decodes to `0` / `""`. `restaurantId: 0` and "no restaurantId" look the same. Validate (`gt=0`, `required`) or use a pointer when absence matters.
6. **`int` vs `int64` and JSON numbers.** Use `int64` for ids and money. JS numbers are float64, so ids above 2^53 are already broken on the Node side; don't "fix" it by decoding into `float64`.
7. **Time zones.** `time.Now()` is local time. Day buckets must use `.UTC()` before `.Format("2006-01-02")`. The layout is the literal reference date, not `YYYY-MM-DD`.
8. **Loop variable capture.** Fixed in Go 1.22 (each iteration has its own variable), but older code and blog posts copy `v := v` everywhere. You don't need that here.
9. **`context.Background()` deep in the stack.** It drops cancellation and the correlation logger. Pass the `ctx` you received; create a fresh one only at true roots (`main`, a shutdown routine).
10. **Mutating shared sentinels.** `analytics.ErrInvalidDateRange.Message = "..."` changes it for every request in the process. Use `.WithMessage()`, which copies.
11. **Interface pollution.** Don't declare `IAnalyticsService` next to `Service` "because Node had interfaces". Declare an interface **at the consumer**, only when you need a seam (we have three: `RestaurantDayStore`, `EventDeduper`, `PermissionFetcher`).
12. **Import cycles are compile errors.** In TS a cycle half-works; in Go it doesn't build. That's why module types live in the parent `analytics` package and why `appcontext` (not `auth`) owns `Claims`.
13. **`defer` in loops.** `defer cur.Close()` inside a loop runs at function end, not per iteration. Use `cur.All` or close explicitly.
14. **`bson.M` order.** Maps are unordered. For sort specs and compound index keys, order matters: use `bson.D`.
15. **Struct tags are stringly typed.** A typo like `bson:"revenu_sum"` compiles and silently writes a new field. `go vet` catches malformed tags, not misspelled names. `play/check-mongo` is how you see what actually landed.
16. **Package names vs folder names.** `lib/errors` is `package apperr`. Always import it with an explicit alias so readers don't wonder where `apperr` came from.
17. **Exported means capitalized.** A lowercase struct field is invisible to `encoding/json` and `bson`: it silently never serializes.
18. **Events arrive out of order.** `payment.completed` is written to the outbox *before* the online `order.placed` (same transaction, lower id). `order.delivered` can be processed before its `order.placed` (parallel workers, DLQ replays, backfill files). Per-event `$inc`s don't care, because they're commutative. A metric that needs **two** events (delivery time = delivered − placed) must never be computed from one payload. Record each fact on `order_timelines` and apply the metric with an atomic claim (system-design §3).
19. **The transaction rides on `ctx`.** Inside `InTx`, the ctx is a `mongo.SessionContext`. Anything that swaps it (`context.Background()`, `context.WithoutCancel` *before* a write) silently runs outside the transaction.
20. **Transaction callbacks are retried.** `WithTransaction` re-runs `fn` on `WriteConflict`. No HTTP calls, publishes or in-memory counters inside `fn`: they would happen twice.
21. **One upsert per line item is a hidden N+1.** Merge lines by product, then one `BulkWrite` (§2.21). It's the Mongo version of order-service's "no N+1" rule.
22. **RabbitMQ queue arguments are immutable.** Changing a queue's DLX in config makes the next boot fail with `PRECONDITION_FAILED`. Delete the queue first. `amqplib` behaves the same; it just bites more often when you're iterating on topology.
23. **Interface-typed `nil`.** `var r *TxRunner = nil; var i TxRunner = r` gives a **non-nil** interface. Return a literal `nil` for "none" (see `lib/boot/wiring.go txRunner`), or `d.tx != nil` lies.

---

## 4. When to break the mapping

Mirroring is the default. These are the places where a literal port would be wrong, and what this service does instead.

| Node does | We do | Why |
| --- | --- | --- |
| **Redis `SETNX`** dedupe with 24h TTL | **Mongo `event_ids`** (unique index, TTL 7d) | Node's handlers are idempotent cache invalidations; ours are non-idempotent `$inc`s. The marker must be as durable as the data it protects, and in the same store so it can share a transaction. See `system-design.md` §6. |
| Every queue deduped the same way | **Per-queue policy**: order events → Mongo `event_ids` + transaction; `rbac.#` → `NoDedupe` | Dedupe cost should match how harmful a replay is. Cache invalidation is idempotent; `$inc` isn't. |
| Dedupe **before** the handler lookup | Handler lookup **first**, then dedupe | Bindings are wide (`order.#`, `payment.#`) and some events have no handler. A marker per ignored event is pure write amplification. |
| Mark seen, then a handler failure → DLQ (marker stays) | Replica set: **one transaction** for marker + writes (failure = nothing persisted). Standalone: **`Forget`** the marker → DLQ | With the marker kept, a DLQ replay would be skipped and the aggregate would never see the event. Our handlers write up to 6 documents, so only a transaction makes "failed" mean "nothing happened". |
| Handlers act on one event's payload | Two-event metrics go through `order_timelines` + atomic claims | `order.delivered` has no `placedAt`, and events of one order arrive in any order. |
| Handler calls `msg.ack()` / `msg.nack()` | Handler **returns `error`**; the broker settles | One owner for settlement means you can't forget to ack, and a panic still nacks. |
| Shared DLX per source exchange (`core.events.dlx`, DLQ bound `#`) | Service-scoped DLX per queue (`analytics-service.core-events.dlx`) | Two services' DLQs bound `#` on one DLX would each receive the other's dead letters. |
| One consumer function per queue | A broker-free **`Dispatcher`** + a thin `Consumer` | `cmd/backfill-aggs` replays files through the exact same pipeline, with no second implementation. |
| Broker connects lazily; boot continues if Rabbit is down | **Boot fails** if Mongo or Rabbit is unreachable after retries; *runtime* drops auto-reconnect | A consumer-only service that boots without its broker reports itself healthy while doing nothing. Orchestrators restart crashed processes. |
| Global error middleware via `throw` | `apperr.Wrap` per route + `Write` in middleware | Go has no throw. |
| DI container (`tsyringe`) | Constructors in `lib/boot` | Compile-time wiring; no reflection, no tokens. |
| `AppError(message, status)` | `AppError{Code, Status, Message, Err}` | Clients need a stable code to branch on. |
| Repository = exported functions with `conn = db` default | Repository = struct holding `*mongo.Collection`; the transaction rides on `ctx` | No global `db` to default to; the struct *is* the injection. |
| One `env` singleton imported everywhere | `config.Config` passed into constructors | Dependencies show in signatures. |
| 403 for an invalid token (core) / mixed `{error: "..."}` shapes | 401 `UNAUTHENTICATED`, one envelope everywhere | 401 is the correct status for bad credentials; one envelope for clients. |
| No access log | `middleware.AccessLog` | One line per request with status and duration is the minimum for a latency budget. |

The rule of thumb: **mirror the architecture, not the accidents.** Layering, envelopes, the auth contract, the event envelope, file roles and naming are architecture, so mirror them. A dedupe store chosen for a different kind of handler, or mixed error shapes, are accidents of the Node codebase or its constraints. Port the intent, not the line.
