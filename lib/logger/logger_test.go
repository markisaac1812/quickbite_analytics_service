package logger_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/quickbite/analytics-service/internal/testkit/assert"
	"github.com/quickbite/analytics-service/lib/appcontext"
	"github.com/quickbite/analytics-service/lib/logger"
)

func TestNew(t *testing.T) {
	cases := []struct {
		level   string
		enabled slog.Level
		below   slog.Level
	}{
		{"debug", slog.LevelDebug, slog.LevelDebug - 1},
		{"info", slog.LevelInfo, slog.LevelDebug},
		{"WARN", slog.LevelWarn, slog.LevelInfo},
		{"warning", slog.LevelWarn, slog.LevelInfo},
		{"error", slog.LevelError, slog.LevelWarn},
		{"nonsense", slog.LevelInfo, slog.LevelDebug},
	}
	for _, c := range cases {
		t.Run("level "+c.level, func(t *testing.T) {
			// Arrange
			ctx := context.Background()

			// Act
			l := logger.New(c.level, "test")

			// Assert
			assert.True(t, l.Enabled(ctx, c.enabled))
			assert.False(t, l.Enabled(ctx, c.below))
		})
	}
}

func TestContextLogger(t *testing.T) {
	t.Run("returns the logger stored in ctx", func(t *testing.T) {
		// Arrange
		l := slog.New(slog.DiscardHandler)
		ctx := logger.WithContext(context.Background(), l)

		// Act
		got := logger.FromContext(ctx)

		// Assert
		assert.True(t, got == l)
	})

	t.Run("falls back to slog.Default", func(t *testing.T) {
		// Arrange
		ctx := context.Background()

		// Act
		got := logger.FromContext(ctx)

		// Assert
		assert.True(t, got == slog.Default())
	})
}

func TestAppContext(t *testing.T) {
	t.Run("round-trips claims and correlation id", func(t *testing.T) {
		// Arrange
		claims := appcontext.Claims{UserID: 1, Role: "system_admin"}

		// Act
		ctx := appcontext.WithCorrelationID(appcontext.WithClaims(context.Background(), claims), "c-1")
		got, ok := appcontext.ClaimsFrom(ctx)

		// Assert
		assert.True(t, ok)
		assert.Equal(t, got, claims)
		assert.Equal(t, appcontext.CorrelationID(ctx), "c-1")
	})

	t.Run("reports an anonymous ctx", func(t *testing.T) {
		// Arrange
		ctx := context.Background()

		// Act
		_, ok := appcontext.ClaimsFrom(ctx)

		// Assert
		assert.False(t, ok)
		assert.Equal(t, appcontext.CorrelationID(ctx), "")
	})
}
