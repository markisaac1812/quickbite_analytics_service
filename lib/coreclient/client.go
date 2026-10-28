// Package coreclient is the sync HTTP client to core-service's /internal API.
// Go analogue of order-service's lib/core-client/.
package coreclient

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/quickbite/analytics-service/lib/appcontext"
	apperr "github.com/quickbite/analytics-service/lib/errors"
	"github.com/quickbite/analytics-service/pkg/httpclient"
)

var (
	// ErrCoreUnavailable: core is down / timing out / 5xx after retries.
	ErrCoreUnavailable = apperr.New("UPSTREAM_UNAVAILABLE", http.StatusServiceUnavailable, "core-service is unavailable, try again later")
	// ErrCoreUpstream: core answered 4xx to an internal call — a contract or
	// config bug on our side (wrong api-key, bad path), never the end user's fault.
	ErrCoreUpstream = apperr.New("UPSTREAM_ERROR", http.StatusBadGateway, "core-service rejected an internal request")
)

// Client wraps pkg/httpclient with core's base URL, the internal api-key, and
// correlation-id forwarding.
type Client struct {
	http *httpclient.Client
}

// New builds a core client. attempts includes the first try.
func New(baseURL, apiKey string, timeout time.Duration, attempts int) (*Client, error) {
	hc, err := httpclient.New(httpclient.Config{
		BaseURL:        baseURL,
		Timeout:        timeout,
		Attempts:       attempts,
		InitialBackoff: 50 * time.Millisecond,
		MaxBackoff:     500 * time.Millisecond,
		Headers:        map[string]string{"api-key": apiKey},
	})
	if err != nil {
		return nil, err
	}
	return &Client{http: hc}, nil
}

// get performs a GET and maps transport/status failures onto AppErrors.
func (c *Client) get(ctx context.Context, req httpclient.Request, out any) error {
	req.Method = http.MethodGet
	if id := appcontext.CorrelationID(ctx); id != "" {
		req.Headers = map[string]string{"X-CorrelationId": id}
	}

	err := c.http.DoJSON(ctx, req, out)
	if err == nil {
		return nil
	}
	var se *httpclient.StatusError
	if errors.As(err, &se) && se.StatusCode < 500 {
		return ErrCoreUpstream.WithCause(err)
	}
	return ErrCoreUnavailable.WithCause(err)
}
