package httpclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quickbite/analytics-service/internal/testkit/assert"
	"github.com/quickbite/analytics-service/pkg/httpclient"
)

// script answers each call with the next status in statuses (the last one repeats).
func script(t *testing.T, statuses ...int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := int(calls.Add(1)) - 1
		w.WriteHeader(statuses[min(n, len(statuses)-1)])
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func newClient(t *testing.T, base string, attempts int) *httpclient.Client {
	t.Helper()
	c, err := httpclient.New(httpclient.Config{
		BaseURL: base, Attempts: attempts, Timeout: time.Second,
		InitialBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond,
		Headers: map[string]string{"api-key": "k"},
	})
	assert.NoErr(t, err)
	return c
}

func TestNew(t *testing.T) {
	t.Run("rejects a base url without scheme or host", func(t *testing.T) {
		for _, base := range []string{"", "localhost:3000", "http://", "::bad"} {
			// Arrange — base above

			// Act
			_, err := httpclient.New(httpclient.Config{BaseURL: base})

			// Assert
			assert.Err(t, err, base)
		}
	})

	t.Run("applies defaults to zero values", func(t *testing.T) {
		// Arrange
		srv, calls := script(t, http.StatusServiceUnavailable)
		c, err := httpclient.New(httpclient.Config{BaseURL: srv.URL, InitialBackoff: time.Millisecond})
		assert.NoErr(t, err)

		// Act
		err = c.DoJSON(context.Background(), httpclient.Request{Method: http.MethodGet, Path: "/x"}, nil)

		// Assert — default Attempts = 3
		assert.True(t, httpclient.IsServerError(err))
		assert.Equal(t, calls.Load(), int32(3))
	})
}

func TestDoJSON(t *testing.T) {
	t.Run("sends method, path, query, headers and a JSON body; decodes the reply", func(t *testing.T) {
		// Arrange
		var got struct {
			method, path, query, apiKey, extra, ctype string
			body                                      map[string]any
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got.method, got.path, got.query = r.Method, r.URL.Path, r.URL.RawQuery
			got.apiKey, got.extra, got.ctype = r.Header.Get("api-key"), r.Header.Get("X-Extra"), r.Header.Get("Content-Type")
			_ = json.NewDecoder(r.Body).Decode(&got.body)
			_, _ = w.Write([]byte(`{"answer":42}`))
		}))
		defer srv.Close()
		c := newClient(t, srv.URL, 1)
		var out struct{ Answer int }

		// Act
		err := c.DoJSON(context.Background(), httpclient.Request{
			Method: http.MethodPost, Path: "/api/things", Query: url.Values{"role": {"owner"}},
			Headers: map[string]string{"X-Extra": "1"}, Body: map[string]string{"name": "x"},
		}, &out)

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, out.Answer, 42)
		assert.Equal(t, got.method, http.MethodPost)
		assert.Equal(t, got.path, "/api/things")
		assert.Equal(t, got.query, "role=owner")
		assert.Equal(t, got.apiKey, "k")
		assert.Equal(t, got.extra, "1")
		assert.Equal(t, got.ctype, "application/json")
		assert.Equal(t, got.body, map[string]any{"name": "x"})
	})

	t.Run("ignores the body of a 204 or when out is nil", func(t *testing.T) {
		// Arrange
		srv, _ := script(t, http.StatusNoContent)
		c := newClient(t, srv.URL, 1)
		var out map[string]any

		// Act
		err204 := c.DoJSON(context.Background(), httpclient.Request{Method: http.MethodGet}, &out)
		errNil := c.DoJSON(context.Background(), httpclient.Request{Method: http.MethodGet}, nil)

		// Assert
		assert.NoErr(t, err204)
		assert.NoErr(t, errNil)
		assert.True(t, out == nil)
	})

	t.Run("returns a 4xx immediately as a StatusError", func(t *testing.T) {
		// Arrange
		srv, calls := script(t, http.StatusNotFound)
		c := newClient(t, srv.URL, 3)

		// Act
		err := c.DoJSON(context.Background(), httpclient.Request{Method: http.MethodGet}, nil)

		// Assert
		var se *httpclient.StatusError
		assert.True(t, errors.As(err, &se))
		assert.Equal(t, se.StatusCode, http.StatusNotFound)
		assert.Equal(t, se.Error(), `http 404: {"ok":true}`)
		assert.False(t, httpclient.IsServerError(err))
		assert.Equal(t, calls.Load(), int32(1))
	})

	t.Run("retries a 5xx and succeeds on a later attempt", func(t *testing.T) {
		// Arrange
		srv, calls := script(t, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusOK)
		c := newClient(t, srv.URL, 3)

		// Act
		err := c.DoJSON(context.Background(), httpclient.Request{Method: http.MethodGet}, nil)

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, calls.Load(), int32(3))
	})

	t.Run("retries network errors and returns the last one", func(t *testing.T) {
		// Arrange
		srv := httptest.NewServer(http.NotFoundHandler())
		base := srv.URL
		srv.Close()
		c := newClient(t, base, 2)

		// Act
		err := c.DoJSON(context.Background(), httpclient.Request{Method: http.MethodGet}, nil)

		// Assert
		var ue *url.Error
		assert.True(t, errors.As(err, &ue))
	})

	t.Run("does not retry a decode error", func(t *testing.T) {
		// Arrange
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			_, _ = w.Write([]byte(`not json`))
		}))
		defer srv.Close()
		c := newClient(t, srv.URL, 3)
		var out map[string]any

		// Act
		err := c.DoJSON(context.Background(), httpclient.Request{Method: http.MethodGet}, &out)

		// Assert
		assert.Contains(t, err.Error(), "decode response")
		assert.Equal(t, calls.Load(), int32(1))
	})

	t.Run("stops retrying once the caller's context is cancelled", func(t *testing.T) {
		// Arrange
		ctx, cancel := context.WithCancel(context.Background())
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			cancel()
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer srv.Close()
		c, err := httpclient.New(httpclient.Config{BaseURL: srv.URL, Attempts: 5, InitialBackoff: time.Hour, MaxBackoff: time.Hour})
		assert.NoErr(t, err)

		// Act
		err = c.DoJSON(ctx, httpclient.Request{Method: http.MethodGet}, nil)

		// Assert — the 503 is not retried because ctx is already done
		assert.Err(t, err)
	})

	t.Run("aborts the backoff wait when the context is cancelled", func(t *testing.T) {
		// Arrange
		srv, _ := script(t, http.StatusServiceUnavailable)
		c, err := httpclient.New(httpclient.Config{BaseURL: srv.URL, Attempts: 5, InitialBackoff: time.Hour, MaxBackoff: time.Hour})
		assert.NoErr(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		// Act
		start := time.Now()
		err = c.DoJSON(ctx, httpclient.Request{Method: http.MethodGet}, nil)

		// Assert
		assert.ErrIs(t, err, context.DeadlineExceeded)
		assert.True(t, time.Since(start) < 5*time.Second)
	})

	t.Run("fails before sending when the body cannot be encoded", func(t *testing.T) {
		// Arrange
		srv, calls := script(t, http.StatusOK)
		c := newClient(t, srv.URL, 1)

		// Act
		err := c.DoJSON(context.Background(), httpclient.Request{Method: http.MethodPost, Body: make(chan int)}, nil)

		// Assert
		assert.Contains(t, err.Error(), "encode body")
		assert.Equal(t, calls.Load(), int32(0))
	})

	t.Run("fails on a method net/http cannot build", func(t *testing.T) {
		// Arrange
		srv, _ := script(t, http.StatusOK)
		c := newClient(t, srv.URL, 1)

		// Act
		err := c.DoJSON(context.Background(), httpclient.Request{Method: "BAD METHOD"}, nil)

		// Assert
		assert.Contains(t, err.Error(), "build request")
	})
}
