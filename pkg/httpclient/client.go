// Package httpclient is a thin net/http wrapper: base URL, per-request
// timeout, JSON in/out, and retry with exponential backoff on 5xx/network
// errors. No app types, no env, no globals.
package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Config for a Client. Zero values get sensible defaults.
type Config struct {
	BaseURL        string
	Timeout        time.Duration // per attempt
	Attempts       int           // total tries including the first
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	Headers        map[string]string // sent on every request (e.g. api-key)
}

// Request describes one call. Body, when non-nil, is JSON-encoded.
type Request struct {
	Method  string
	Path    string
	Query   url.Values
	Headers map[string]string
	Body    any
}

// StatusError is returned for any non-2xx response.
type StatusError struct {
	StatusCode int
	Body       string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("http %d: %s", e.StatusCode, e.Body)
}

// IsServerError reports whether err is a 5xx StatusError.
func IsServerError(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.StatusCode >= 500
}

// Client is safe for concurrent use.
type Client struct {
	cfg  Config
	base *url.URL
	http *http.Client
}

// New validates the base URL and applies defaults.
func New(cfg Config) (*Client, error) {
	base, err := url.Parse(cfg.BaseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("httpclient: invalid base url %q", cfg.BaseURL)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 3 * time.Second
	}
	if cfg.Attempts <= 0 {
		cfg.Attempts = 3
	}
	if cfg.InitialBackoff <= 0 {
		cfg.InitialBackoff = 50 * time.Millisecond
	}
	if cfg.MaxBackoff < cfg.InitialBackoff {
		cfg.MaxBackoff = 500 * time.Millisecond
	}
	return &Client{cfg: cfg, base: base, http: &http.Client{}}, nil
}

// DoJSON performs the request and decodes a 2xx JSON body into out (if non-nil).
// 5xx and transport errors are retried; 4xx is returned immediately as *StatusError.
func (c *Client) DoJSON(ctx context.Context, req Request, out any) error {
	target := c.base.ResolveReference(&url.URL{Path: req.Path, RawQuery: req.Query.Encode()})

	var payload []byte
	if req.Body != nil {
		b, err := json.Marshal(req.Body)
		if err != nil {
			return fmt.Errorf("httpclient: encode body: %w", err)
		}
		payload = b
	}

	delay := c.cfg.InitialBackoff
	var lastErr error
	for attempt := 1; attempt <= c.cfg.Attempts; attempt++ {
		lastErr = c.once(ctx, req, target.String(), payload, out)
		if lastErr == nil || !retryable(ctx, lastErr) || attempt == c.cfg.Attempts {
			break
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
		delay = min(delay*2, c.cfg.MaxBackoff)
	}
	return lastErr
}

func (c *Client) once(ctx context.Context, req Request, target string, payload []byte, out any) error {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, target, body)
	if err != nil {
		return fmt.Errorf("httpclient: build request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	if payload != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	for k, v := range c.cfg.Headers {
		httpReq.Header.Set(k, v)
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	res, err := c.http.Do(httpReq)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode > 299 {
		snippet, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return &StatusError{StatusCode: res.StatusCode, Body: string(snippet)}
	}
	if out == nil || res.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, res.Body)
		return nil
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("httpclient: decode response: %w", err)
	}
	return nil
}

// retryable: 5xx and transport errors yes; 4xx, decode errors and a cancelled
// parent context no.
func retryable(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	var se *StatusError
	if errors.As(err, &se) {
		return se.StatusCode >= 500
	}
	var urlErr *url.Error
	return errors.As(err, &urlErr)
}
