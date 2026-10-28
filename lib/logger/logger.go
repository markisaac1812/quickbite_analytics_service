// Package logger builds the service's structured JSON logger (stdlib slog)
// and carries a request-scoped child logger through context.Context.
//
// Node analogue: lib/logger/logger.ts (JSON lines to stdout). The difference
// is that Go passes a *slog.Logger explicitly (or via ctx) instead of
// importing a module-level singleton.
package logger

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

type ctxKey struct{}

// New returns a JSON logger writing to stdout at the given level, tagged
// with the service name and environment.
func New(level, appEnv string) *slog.Logger {
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parseLevel(level)})
	return slog.New(h).With("service", "analytics-service", "env", appEnv)
}

// WithContext returns a ctx carrying l. Middleware stores a logger already
// tagged with correlation_id so every downstream log line carries it.
func WithContext(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext returns the request-scoped logger, or slog.Default() (which
// boot sets to the service logger) when none was attached.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
