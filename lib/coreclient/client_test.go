package coreclient_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/quickbite/analytics-service/internal/testkit"
	"github.com/quickbite/analytics-service/internal/testkit/assert"
	"github.com/quickbite/analytics-service/lib/appcontext"
	"github.com/quickbite/analytics-service/lib/coreclient"
)

func newClient(t *testing.T, baseURL, apiKey string) *coreclient.Client {
	t.Helper()
	c, err := coreclient.New(baseURL, apiKey, time.Second, 2)
	assert.NoErr(t, err)
	return c
}

func TestPermissionsByRole(t *testing.T) {
	t.Run("returns the role's permissions, sending the api-key and correlation id", func(t *testing.T) {
		// Arrange
		core := testkit.NewCoreStub()
		defer core.Close()
		c := newClient(t, core.URL(), testkit.InternalAPIKey)
		ctx := appcontext.WithCorrelationID(context.Background(), "corr-1")

		// Act
		perms, err := c.PermissionsByRole(ctx, "owner")

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, perms, []string{"analytics:read", "orders:read"})
		assert.Equal(t, core.Calls(), []testkit.StubCall{{
			Path: "/api/internal/rbac/permissions", Query: "role=owner", APIKey: testkit.InternalAPIKey, CorrID: "corr-1",
		}})
	})

	t.Run("returns an empty, non-nil list when core sends null", func(t *testing.T) {
		// Arrange
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"success":true,"data":{"role":"ghost","permissions":null}}`))
		}))
		defer srv.Close()
		c := newClient(t, srv.URL, "k")

		// Act
		perms, err := c.PermissionsByRole(context.Background(), "ghost")

		// Assert
		assert.NoErr(t, err)
		assert.True(t, perms != nil && len(perms) == 0)
	})

	t.Run("maps a 4xx from core to UPSTREAM_ERROR without retrying", func(t *testing.T) {
		// Arrange
		core := testkit.NewCoreStub()
		defer core.Close()
		c := newClient(t, core.URL(), "wrong-key")

		// Act
		_, err := c.PermissionsByRole(context.Background(), "owner")

		// Assert
		assert.ErrIs(t, err, coreclient.ErrCoreUpstream)
		assert.Len(t, core.Calls(), 1)
	})

	t.Run("maps a 5xx from core to UPSTREAM_UNAVAILABLE after retrying", func(t *testing.T) {
		// Arrange
		core := testkit.NewCoreStub()
		defer core.Close()
		core.FailWith(http.StatusBadGateway)
		c := newClient(t, core.URL(), testkit.InternalAPIKey)

		// Act
		_, err := c.PermissionsByRole(context.Background(), "owner")

		// Assert
		assert.ErrIs(t, err, coreclient.ErrCoreUnavailable)
		assert.Len(t, core.Calls(), 2)
	})

	t.Run("maps an unreachable core to UPSTREAM_UNAVAILABLE", func(t *testing.T) {
		// Arrange
		core := testkit.NewCoreStub()
		url := core.URL()
		core.Close()
		c := newClient(t, url, testkit.InternalAPIKey)

		// Act
		_, err := c.PermissionsByRole(context.Background(), "owner")

		// Assert
		assert.ErrIs(t, err, coreclient.ErrCoreUnavailable)
	})
}

func TestNew(t *testing.T) {
	// Arrange
	badURL := "core-service:3000"

	// Act
	_, err := coreclient.New(badURL, "k", time.Second, 1)

	// Assert
	assert.Err(t, err)
}
