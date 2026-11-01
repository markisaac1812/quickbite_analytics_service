package middleware_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/quickbite/analytics-service/internal/testkit/assert"
	"github.com/quickbite/analytics-service/lib/appcontext"
	"github.com/quickbite/analytics-service/lib/logger"
	"github.com/quickbite/analytics-service/lib/middleware"
)

// echoCorrelation writes the correlation id it finds in ctx and logs a line.
func echoCorrelation() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.FromContext(r.Context()).Info("inside")
		_, _ = w.Write([]byte(appcontext.CorrelationID(r.Context())))
	})
}

func TestCorrelation(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		want    func(t *testing.T, id string)
	}{
		{"reuses an incoming X-CorrelationId", map[string]string{"X-CorrelationId": "abc-123"},
			func(t *testing.T, id string) { assert.Equal(t, id, "abc-123") }},
		{"accepts the X-Correlation-Id spelling", map[string]string{"X-Correlation-Id": "def-456"},
			func(t *testing.T, id string) { assert.Equal(t, id, "def-456") }},
		{"mints a UUID when none is sent", nil,
			func(t *testing.T, id string) { _, err := uuid.Parse(id); assert.NoErr(t, err) }},
		{"replaces an oversized id with a UUID", map[string]string{"X-CorrelationId": strings.Repeat("x", 129)},
			func(t *testing.T, id string) { _, err := uuid.Parse(id); assert.NoErr(t, err) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			var buf bytes.Buffer
			h := middleware.Correlation(slog.New(slog.NewJSONHandler(&buf, nil)))(echoCorrelation())
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			for k, v := range c.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()

			// Act
			h.ServeHTTP(rec, req)

			// Assert
			id := rec.Body.String()
			c.want(t, id)
			assert.Equal(t, rec.Header().Get(middleware.CorrelationHeader), id, "echoed on the response")
			assert.Contains(t, buf.String(), `"correlation_id":"`+id+`"`, "ctx logger is tagged")
		})
	}
}

func TestAccessLog(t *testing.T) {
	cases := []struct {
		name   string
		inner  http.HandlerFunc
		status float64
	}{
		{"logs the status the handler wrote", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }, 418},
		{"logs 200 when the handler writes nothing", func(http.ResponseWriter, *http.Request) {}, 200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			var buf bytes.Buffer
			log := slog.New(slog.NewJSONHandler(&buf, nil))
			h := middleware.AccessLog(c.inner)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
			req = req.WithContext(logger.WithContext(req.Context(), log))

			// Act
			h.ServeHTTP(httptest.NewRecorder(), req)

			// Assert
			var line map[string]any
			assert.NoErr(t, json.Unmarshal(buf.Bytes(), &line))
			assert.Equal(t, line["msg"], any("http request"))
			assert.Equal(t, line["path"], any("/api/v1/x"))
			assert.Equal(t, line["status"], any(c.status))
		})
	}
}
