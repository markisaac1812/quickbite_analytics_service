package testkit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
)

// CoreStub stands in for core-service's internal API — only the endpoint
// analytics calls (GET /api/internal/rbac/permissions). Responses use core's
// {success, data} envelope. Go analogue of tests/helpers/stubs/core-stub.ts.
type CoreStub struct {
	srv *httptest.Server

	mu          sync.Mutex
	permissions map[string][]string
	failStatus  int // >0: answer every call with this status
	calls       []StubCall
}

// StubCall is one request the stub received.
type StubCall struct {
	Path   string
	Query  string
	APIKey string
	CorrID string
}

// NewCoreStub starts the stub on a random local port. Call Close when done.
func NewCoreStub() *CoreStub {
	s := &CoreStub{}
	s.Reset()
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// URL is the stub's base URL (CORE_SERVICE_BASE_URL).
func (s *CoreStub) URL() string { return s.srv.URL }

func (s *CoreStub) Close() { s.srv.Close() }

// Reset restores the default permissions and forgets recorded calls.
func (s *CoreStub) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.permissions = RolePermissions()
	s.failStatus = 0
	s.calls = nil
}

// SetPermissions replaces one role's permissions.
func (s *CoreStub) SetPermissions(role string, perms ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.permissions[role] = perms
}

// FailWith makes every following call answer status (0 = stop failing).
func (s *CoreStub) FailWith(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failStatus = status
}

// Calls returns a copy of the recorded requests.
func (s *CoreStub) Calls() []StubCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]StubCall(nil), s.calls...)
}

func (s *CoreStub) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.calls = append(s.calls, StubCall{
		Path: r.URL.Path, Query: r.URL.RawQuery,
		APIKey: r.Header.Get("api-key"), CorrID: r.Header.Get("X-CorrelationId"),
	})
	fail := s.failStatus
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if fail > 0 {
		w.WriteHeader(fail)
		_, _ = w.Write([]byte(`{"error":"stubbed failure"}`))
		return
	}
	if r.URL.Path != "/api/internal/rbac/permissions" {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"NotFound"}`))
		return
	}
	if r.Header.Get("api-key") != InternalAPIKey {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"Invalid api key"}`))
		return
	}

	role := r.URL.Query().Get("role")
	s.mu.Lock()
	perms := s.permissions[role]
	s.mu.Unlock()
	if perms == nil {
		perms = []string{}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"data":    map[string]any{"role": role, "permissions": perms},
	})
}
