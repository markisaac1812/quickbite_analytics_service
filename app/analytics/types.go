package analytics

import (
	"context"
	"time"

	"github.com/quickbite/analytics-service/app/analytics/entity"
)

// ── inbound event payloads (owned by order-service; mirrored, not shared) ──

// OrderPlacedPayload is the `payload` of order.placed, as built by
// buildOrderPlacedPayload() in order-service's order.service.ts and by
// kashier-webhook.service.ts. Money is integer minor units.
type OrderPlacedPayload struct {
	OrderID       string            `json:"orderId"`
	Region        string            `json:"region"`
	CountryCode   string            `json:"countryCode"`
	RestaurantID  int64             `json:"restaurantId"`
	BranchID      int64             `json:"branchId"`
	CustomerID    int64             `json:"customerId"`
	Status        string            `json:"status"`
	PaymentMethod string            `json:"paymentMethod"`
	Subtotal      int64             `json:"subtotal"`
	DeliveryFee   int64             `json:"deliveryFee"`
	ServiceFee    int64             `json:"serviceFee"`
	Total         int64             `json:"total"`
	Currency      string            `json:"currency"`
	Items         []OrderPlacedItem `json:"items"`
	PlacedAt      string            `json:"placedAt"`
}

type OrderPlacedItem struct {
	ProductID int64 `json:"productId"`
	Quantity  int64 `json:"quantity"`
	UnitPrice int64 `json:"unitPrice"`
	LineTotal int64 `json:"lineTotal"`
}

// OrderDeliveredPayload is built in order-service's settlement.service.ts.
// Note: it carries deliveredAt but NOT placedAt — see OrderTimelineStore.
type OrderDeliveredPayload struct {
	OrderID         string `json:"orderId"`
	Region          string `json:"region"`
	RestaurantID    int64  `json:"restaurantId"`
	BranchID        int64  `json:"branchId"`
	CustomerID      int64  `json:"customerId"`
	DeliveryAgentID int64  `json:"deliveryAgentId"`
	Total           int64  `json:"total"`
	Currency        string `json:"currency"`
	DeliveredAt     string `json:"deliveredAt"`
}

// OrderTransitionPayload is buildOrderTransitionPayload() in order.service.ts,
// shared by order.accepted / rejected / cancelled. occurredAt = order.updatedAt.
type OrderTransitionPayload struct {
	OrderID      string  `json:"orderId"`
	Region       string  `json:"region"`
	RestaurantID int64   `json:"restaurantId"`
	BranchID     int64   `json:"branchId"`
	CustomerID   int64   `json:"customerId"`
	Status       string  `json:"status"`
	Reason       *string `json:"reason"`
	Actor        string  `json:"actor"`
	OccurredAt   string  `json:"occurredAt"`
}

// PaymentCompletedPayload is built in kashier-webhook.service.ts (online
// captures only — COD never emits payment.completed).
type PaymentCompletedPayload struct {
	OrderID             string `json:"orderId"`
	Region              string `json:"region"`
	RestaurantID        int64  `json:"restaurantId"`
	BranchID            int64  `json:"branchId"`
	CustomerID          int64  `json:"customerId"`
	Provider            string `json:"provider"`
	ProviderReferenceID string `json:"providerReferenceId"`
	Amount              int64  `json:"amount"`
	Currency            string `json:"currency"`
	Method              string `json:"method"`
	CompletedAt         string `json:"completedAt"`
}

// ── service inputs (decoded, typed, transport stripped) ──

type OnOrderPlacedInput struct {
	EventID      string
	OrderID      string
	RestaurantID int64
	BranchID     int64
	Currency     string
	TotalMinor   int64
	Lines        []OrderLine
	PlacedAt     time.Time
}

// OrderLine is one item of a placed order, as the product rollup needs it.
type OrderLine struct {
	ProductID      int64
	Quantity       int64
	LineTotalMinor int64
}

type OnOrderDeliveredInput struct {
	OrderID      string
	RestaurantID int64
	BranchID     int64
	DeliveredAt  time.Time
}

type OnOrderRejectedInput struct {
	OrderID      string
	RestaurantID int64
	BranchID     int64
	RejectedAt   time.Time
}

type OnPaymentCompletedInput struct {
	OrderID      string
	RestaurantID int64
	BranchID     int64
	AmountMinor  int64
	Currency     string
	CompletedAt  time.Time
}

// RangeInput: inclusive UTC day range.
type RangeInput struct {
	From time.Time
	To   time.Time
}

// ListRestaurantDaysInput: one restaurant over a range (days, failures,
// delivery-avg, payments, top products).
type ListRestaurantDaysInput struct {
	RestaurantID int64
	From         time.Time
	To           time.Time
}

type ListBranchDaysInput struct {
	RestaurantID int64
	BranchID     int64
	From         time.Time
	To           time.Time
}

type TopProductsInput struct {
	RestaurantID int64
	From         time.Time
	To           time.Time
	Limit        int
}

type TopRestaurantsInput struct {
	From     time.Time
	To       time.Time
	Currency string
	Limit    int
}

// ── service read models (derived values computed here, never stored) ──

// RestaurantDayRow is one day of one restaurant (also used for branches).
type RestaurantDayRow struct {
	Date          string
	OrdersCount   int64
	RevenueMinor  int64
	Currency      string
	AvgOrderMinor int64
}

// FailureDayRow: failureRate = rejected / orders (0 when no orders).
type FailureDayRow struct {
	Date          string
	OrdersCount   int64
	RejectedCount int64
	FailureRate   float64
}

type FailureReport struct {
	OrdersCount   int64
	RejectedCount int64
	FailureRate   float64
	Days          []FailureDayRow
}

// DeliveryDayRow: avgDeliveryMs = delivery_ms_sum / delivery_ms_count.
type DeliveryDayRow struct {
	Date           string
	DeliveredCount int64
	AvgDeliveryMs  int64
}

type DeliveryReport struct {
	DeliveredCount int64
	AvgDeliveryMs  int64
	Days           []DeliveryDayRow
}

type PaymentDayRow struct {
	Date          string
	PaymentsCount int64
	PaymentsMinor int64
	Currency      string
}

type PaymentsReport struct {
	PaymentsCount int64
	PaymentsMinor int64
	Currency      string
	Days          []PaymentDayRow
}

type ProductRankRow struct {
	ProductID    int64
	UnitsSold    int64
	RevenueMinor int64
	OrdersCount  int64
	Currency     string
}

type PlatformDayRow struct {
	Date           string
	Currency       string
	OrdersCount    int64
	RevenueMinor   int64
	AvgOrderMinor  int64
	RejectedCount  int64
	FailureRate    float64
	DeliveredCount int64
	AvgDeliveryMs  int64
	PaymentsCount  int64
	PaymentsMinor  int64
}

type ActiveRestaurantsDay struct {
	Date              string
	ActiveRestaurants int64
}

type ActiveRestaurantsReport struct {
	ActiveRestaurants int64 // distinct over the whole range
	Days              []ActiveRestaurantsDay
}

type RestaurantRankRow struct {
	RestaurantID  int64
	OrdersCount   int64
	RevenueMinor  int64
	AvgOrderMinor int64
	Currency      string
}

// ── repository contract ──

// DayDelta is one associative change to a day row. Every counter is $inc'ed
// (zeros included, so documents always have the same shape).
type DayDelta struct {
	Orders          int64
	Revenue         int64
	DeliveryMs      int64
	Deliveries      int64
	Rejected        int64
	RejectedRevenue int64
	Payments        int64
	PaymentsSum     int64
}

type RestaurantDayStore interface {
	Increment(ctx context.Context, restaurantID int64, date, currency string, d DayDelta, at time.Time) error
	FindRange(ctx context.Context, restaurantID int64, fromDate, toDate string) ([]entity.RestaurantDay, error)
	ActiveRestaurants(ctx context.Context, fromDate, toDate string) (total int64, days []entity.DateCount, err error)
	TopRestaurants(ctx context.Context, fromDate, toDate, currency string, limit int) ([]entity.RestaurantTotal, error)
}

type BranchDayStore interface {
	Increment(ctx context.Context, branchID, restaurantID int64, date, currency string, d DayDelta, at time.Time) error
	FindRange(ctx context.Context, restaurantID, branchID int64, fromDate, toDate string) ([]entity.BranchDay, error)
}

type ProductDayStore interface {
	// IncrementLines applies every line in ONE BulkWrite (one round trip per order).
	IncrementLines(ctx context.Context, restaurantID int64, date, currency string, lines []OrderLine, at time.Time) error
	TopProducts(ctx context.Context, restaurantID int64, fromDate, toDate string, limit int) ([]entity.ProductTotal, error)
}

type PlatformDayStore interface {
	Increment(ctx context.Context, date, currency string, d DayDelta, at time.Time) error
	FindRange(ctx context.Context, fromDate, toDate string) ([]entity.PlatformDay, error)
}

// OrderTimelineStore makes order-scoped metrics order-independent.
// order.delivered has no placedAt, and events can arrive in any order
// (payment.completed even precedes order.placed in the outbox). Each event
// records its fact on the order's timeline; a metric that needs two facts is
// applied exactly once by whichever event completes the pair, via an atomic
// claim (findOneAndUpdate on an *_applied flag).
type OrderTimelineStore interface {
	RecordPlaced(ctx context.Context, in TimelinePlaced) error
	RecordDelivered(ctx context.Context, orderID string, restaurantID, branchID int64, deliveredAt, at time.Time) error
	RecordRejected(ctx context.Context, orderID string, restaurantID, branchID int64, rejectedAt, at time.Time) error
	// ClaimDelivery returns the timeline iff placed+delivered are both known and
	// the delivery metric was not applied yet (and marks it applied). nil otherwise.
	ClaimDelivery(ctx context.Context, orderID string) (*entity.OrderTimeline, error)
	ClaimRejection(ctx context.Context, orderID string) (*entity.OrderTimeline, error)
}

type TimelinePlaced struct {
	OrderID      string
	RestaurantID int64
	BranchID     int64
	Currency     string
	TotalMinor   int64
	PlacedAt     time.Time
	PlacedDate   string
	At           time.Time
}

// Stores groups the service's persistence dependencies (one constructor arg
// instead of five positional ones).
type Stores struct {
	Restaurants RestaurantDayStore
	Branches    BranchDayStore
	Products    ProductDayStore
	Platform    PlatformDayStore
	Timelines   OrderTimelineStore
}
