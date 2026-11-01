package rbac

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/quickbite/analytics-service/internal/testkit/assert"
	"github.com/quickbite/analytics-service/lib/coreevents"
)

// fakeFetcher is an in-memory PermissionFetcher that counts calls per role.
type fakeFetcher struct {
	mu    sync.Mutex
	perms map[string][]string
	err   error
	calls map[string]int
}

func newFakeFetcher() *fakeFetcher {
	return &fakeFetcher{
		perms: map[string][]string{"owner": {"analytics:read"}, "staff": {"orders:read"}},
		calls: map[string]int{},
	}
}

func (f *fakeFetcher) PermissionsByRole(_ context.Context, role string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[role]++
	if f.err != nil {
		return nil, f.err
	}
	return f.perms[role], nil
}

// newClockedCache returns a cache whose clock the test moves by hand.
func newClockedCache(f PermissionFetcher, ttl time.Duration) (*Cache, *time.Time) {
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	c := NewCache(f, ttl)
	c.now = func() time.Time { return now }
	return c, &now
}

func TestCache_Has(t *testing.T) {
	t.Run("reads through to core once and then serves from memory", func(t *testing.T) {
		// Arrange
		f := newFakeFetcher()
		c, _ := newClockedCache(f, time.Minute)

		// Act
		first, err1 := c.Has(context.Background(), "owner", "analytics:read")
		second, err2 := c.Has(context.Background(), "owner", "orders:write")

		// Assert
		assert.NoErr(t, err1)
		assert.NoErr(t, err2)
		assert.True(t, first)
		assert.False(t, second)
		assert.Equal(t, f.calls["owner"], 1)
	})

	t.Run("refetches once the entry is older than the TTL", func(t *testing.T) {
		// Arrange
		f := newFakeFetcher()
		c, now := newClockedCache(f, time.Minute)
		_, _ = c.Has(context.Background(), "owner", "analytics:read")
		f.perms["owner"] = nil // core revoked it
		*now = now.Add(time.Minute)

		// Act
		has, err := c.Has(context.Background(), "owner", "analytics:read")

		// Assert
		assert.NoErr(t, err)
		assert.False(t, has)
		assert.Equal(t, f.calls["owner"], 2)
	})

	t.Run("fails closed and does not cache a core error", func(t *testing.T) {
		// Arrange
		f := newFakeFetcher()
		f.err = errors.New("core down")
		c, _ := newClockedCache(f, time.Minute)

		// Act
		has, err := c.Has(context.Background(), "owner", "analytics:read")
		f.err = nil
		hasAfter, errAfter := c.Has(context.Background(), "owner", "analytics:read")

		// Assert
		assert.Err(t, err)
		assert.False(t, has)
		assert.NoErr(t, errAfter)
		assert.True(t, hasAfter)
		assert.Equal(t, f.calls["owner"], 2)
	})
}

func TestCache_Invalidate(t *testing.T) {
	t.Run("drops one role", func(t *testing.T) {
		// Arrange
		f := newFakeFetcher()
		c, _ := newClockedCache(f, time.Hour)
		_, _ = c.Has(context.Background(), "owner", "analytics:read")
		_, _ = c.Has(context.Background(), "staff", "analytics:read")

		// Act
		c.Invalidate("owner")
		_, _ = c.Has(context.Background(), "owner", "analytics:read")
		_, _ = c.Has(context.Background(), "staff", "analytics:read")

		// Assert
		assert.Equal(t, f.calls, map[string]int{"owner": 2, "staff": 1})
	})

	t.Run("drops every role when the role is empty", func(t *testing.T) {
		// Arrange
		f := newFakeFetcher()
		c, _ := newClockedCache(f, time.Hour)
		_, _ = c.Has(context.Background(), "owner", "analytics:read")
		_, _ = c.Has(context.Background(), "staff", "analytics:read")

		// Act
		c.Invalidate("")
		_, _ = c.Has(context.Background(), "owner", "analytics:read")
		_, _ = c.Has(context.Background(), "staff", "analytics:read")

		// Assert
		assert.Equal(t, f.calls, map[string]int{"owner": 2, "staff": 2})
	})
}

func TestCache_HandlePermissionsChanged(t *testing.T) {
	envelope := func(payload string) coreevents.Envelope {
		return coreevents.Envelope{EventID: "e-1", EventType: EventPermissionsChanged, Payload: json.RawMessage(payload)}
	}

	cases := map[string]struct {
		payload   string
		wantCalls map[string]int
	}{
		"invalidates the named role":        {`{"role":"owner"}`, map[string]int{"owner": 2, "staff": 1}},
		"invalidates every role when empty": {`{}`, map[string]int{"owner": 2, "staff": 2}},
		"treats a null payload as all":      {`null`, map[string]int{"owner": 2, "staff": 2}},
		"treats a missing payload as all":   {``, map[string]int{"owner": 2, "staff": 2}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			// Arrange
			f := newFakeFetcher()
			c, _ := newClockedCache(f, time.Hour)
			_, _ = c.Has(context.Background(), "owner", "x")
			_, _ = c.Has(context.Background(), "staff", "x")

			// Act
			err := c.HandlePermissionsChanged(context.Background(), envelope(tc.payload))
			_, _ = c.Has(context.Background(), "owner", "x")
			_, _ = c.Has(context.Background(), "staff", "x")

			// Assert
			assert.NoErr(t, err)
			assert.Equal(t, f.calls, tc.wantCalls)
		})
	}

	t.Run("dead-letters a malformed payload", func(t *testing.T) {
		// Arrange
		c, _ := newClockedCache(newFakeFetcher(), time.Hour)

		// Act
		err := c.HandlePermissionsChanged(context.Background(), envelope(`{"role":`))

		// Assert
		assert.Err(t, err)
		assert.Contains(t, err.Error(), "rbac.permissions_changed payload")
	})
}
