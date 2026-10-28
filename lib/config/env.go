// Package config parses the environment into a typed struct once, at boot.
// Go analogue of order-service's zod-parsed lib/config/env.ts: every env var
// the service reads is declared here, with its default, or Load fails.
package config

import (
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
)

// Config is the whole service configuration. Nested structs group concerns
// the same way the Node `env` object does (env.jwt, env.rabbit, env.core...).
type Config struct {
	Port            int           `env:"PORT" envDefault:"4002"`
	AppEnv          string        `env:"APP_ENV" envDefault:"development"`
	LogLevel        string        `env:"LOG_LEVEL" envDefault:"info"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"10s"`

	JWT        JWTConfig
	Internal   InternalConfig
	Mongo      MongoConfig
	Rabbit     RabbitConfig
	CoreEvents CoreEventsConfig
	Core       CoreConfig
	RBAC       RBACConfig
	Events     EventsConfig
}

type JWTConfig struct {
	// Same secret as core-service / order-service: tokens are minted by core.
	AccessSecret string `env:"ACCESS_SECRET,required,notEmpty"`
}

type InternalConfig struct {
	// Guards this service's own /internal routes (none in the video slice).
	APIKey string `env:"INTERNAL_API_KEY"`
}

type MongoConfig struct {
	URI            string        `env:"MONGO_URI" envDefault:"mongodb://localhost:27017"`
	Database       string        `env:"MONGO_DB" envDefault:"quickbite_analytics"`
	ConnectTimeout time.Duration `env:"MONGO_CONNECT_TIMEOUT" envDefault:"10s"`
	MaxPoolSize    uint64        `env:"MONGO_MAX_POOL_SIZE" envDefault:"50"`
	// auto: use multi-document transactions when the server supports them
	// (replica set / mongos), otherwise fall back with a warning.
	// on: require them (boot fails on a standalone). off: never use them.
	Transactions string `env:"MONGO_TRANSACTIONS" envDefault:"auto"`
}

type RabbitConfig struct {
	URL      string   `env:"RABBITMQ_URL,required,notEmpty"`
	Exchange string   `env:"RABBITMQ_ORDER_EVENTS_EXCHANGE" envDefault:"order.events"`
	Queue    string   `env:"RABBITMQ_ORDER_EVENTS_QUEUE" envDefault:"analytics-service.order-events"`
	Bindings []string `env:"RABBITMQ_ORDER_EVENTS_BINDINGS" envDefault:"order.#,payment.#" envSeparator:","`
	// Service-scoped DLX: a shared one (e.g. core.events.dlx, which order-service's
	// DLQ binds with "#") would copy our dead letters into another service's DLQ.
	DLX             string        `env:"RABBITMQ_ORDER_EVENTS_DLX" envDefault:"analytics-service.order-events.dlx"`
	DLQ             string        `env:"RABBITMQ_ORDER_EVENTS_DLQ" envDefault:"analytics-service.order-events.dlq"`
	Prefetch        int           `env:"RABBITMQ_PREFETCH" envDefault:"32"`
	ConnectAttempts int           `env:"RABBITMQ_CONNECT_ATTEMPTS" envDefault:"5"`
	ReconnectMax    time.Duration `env:"RABBITMQ_RECONNECT_MAX" envDefault:"15s"`
}

// CoreEventsConfig: inbound core.events (only rbac.# today) on a second queue.
type CoreEventsConfig struct {
	Enabled  bool     `env:"CORE_EVENTS_ENABLED" envDefault:"true"`
	Exchange string   `env:"RABBITMQ_CORE_EVENTS_EXCHANGE" envDefault:"core.events"`
	Queue    string   `env:"RABBITMQ_CORE_EVENTS_QUEUE" envDefault:"analytics-service.core-events"`
	Bindings []string `env:"RABBITMQ_CORE_EVENTS_BINDINGS" envDefault:"rbac.#" envSeparator:","`
	DLX      string   `env:"RABBITMQ_CORE_EVENTS_DLX" envDefault:"analytics-service.core-events.dlx"`
	DLQ      string   `env:"RABBITMQ_CORE_EVENTS_DLQ" envDefault:"analytics-service.core-events.dlq"`
	Prefetch int      `env:"RABBITMQ_CORE_EVENTS_PREFETCH" envDefault:"4"`
}

type CoreConfig struct {
	BaseURL        string        `env:"CORE_SERVICE_BASE_URL,required,notEmpty"`
	InternalAPIKey string        `env:"CORE_INTERNAL_API_KEY,required,notEmpty"`
	Timeout        time.Duration `env:"CORE_HTTP_TIMEOUT" envDefault:"3s"`
	Attempts       int           `env:"CORE_HTTP_ATTEMPTS" envDefault:"3"`
}

type RBACConfig struct {
	CacheTTL time.Duration `env:"RBAC_CACHE_TTL" envDefault:"5m"`
}

type EventsConfig struct {
	// Upper bound for one event handler (dedupe insert + aggregate upsert).
	HandlerTimeout time.Duration `env:"EVENT_HANDLER_TIMEOUT" envDefault:"10s"`
}

// IsProduction mirrors env.isProduction on the Node side.
func (c Config) IsProduction() bool { return c.AppEnv == "production" }

// Load reads .env (if present, never overriding real env vars) and parses
// the environment into Config. Any missing required var is a boot failure.
func Load() (Config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return Config{}, err
	}

	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	if cfg.Rabbit.Prefetch < 1 || cfg.CoreEvents.Prefetch < 1 {
		return Config{}, fmt.Errorf("config: RABBITMQ_PREFETCH and RABBITMQ_CORE_EVENTS_PREFETCH must be >= 1")
	}
	switch cfg.Mongo.Transactions {
	case "auto", "on", "off":
	default:
		return Config{}, fmt.Errorf("config: MONGO_TRANSACTIONS must be auto, on or off (got %q)", cfg.Mongo.Transactions)
	}
	return cfg, nil
}
