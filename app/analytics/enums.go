package analytics

// Permissions this module checks. The catalog itself lives in core-service
// (seeded by core migration 20261028180000_seed_analytics_service_permissions.ts);
// this service only references the string.
const (
	PermAnalyticsRead = "analytics:read"
)

// Inbound event types (= RabbitMQ routing keys on the order.events exchange).
// Must match order-service's lib/events/event-types.ts.
const (
	EventOrderPlaced      = "order.placed"
	EventOrderDelivered   = "order.delivered"
	EventOrderRejected    = "order.rejected"
	EventPaymentCompleted = "payment.completed"
)

const (
	// DateLayout is the Go reference-time layout for "YYYY-MM-DD" (UTC day keys).
	DateLayout = "2006-01-02"
	// MaxRangeDays caps a single range query so one request can't scan years
	// of per-day rows (performance budget, see CLAUDE.md §9).
	MaxRangeDays = 366
	// Top-N list sizes for the ranking endpoints.
	DefaultTopLimit = 10
	MaxTopLimit     = 100
)
