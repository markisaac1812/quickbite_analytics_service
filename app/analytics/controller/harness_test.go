package controller_test

import (
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/quickbite/analytics-service/app/analytics/controller"
	"github.com/quickbite/analytics-service/app/analytics/service"
	"github.com/quickbite/analytics-service/internal/testkit"
	"github.com/quickbite/analytics-service/lib/auth"
	"github.com/quickbite/analytics-service/lib/coreclient"
	"github.com/quickbite/analytics-service/lib/rbac"
)

const rng = "from=2026-10-01&to=2026-10-31"

// harness is the module mounted exactly as lib/boot mounts it (auth → RBAC →
// controller), backed by fake stores and the core-service stub. No Mongo,
// no broker: these are unit tests of the HTTP layer.
type harness struct {
	h     http.Handler
	fakes *testkit.FakeStores
	core  *testkit.CoreStub
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	core := testkit.NewCoreStub()
	t.Cleanup(core.Close)

	client, err := coreclient.New(core.URL(), testkit.InternalAPIKey, time.Second, 1)
	if err != nil {
		t.Fatal(err)
	}
	fakes := testkit.NewFakeStores()
	ctrl := controller.New(service.New(fakes.Stores()))
	authn := auth.NewMiddleware(auth.NewVerifier(testkit.AccessSecret), log)
	guard := rbac.NewGuard(rbac.NewCache(client, time.Minute), log)

	r := chi.NewRouter()
	r.Route("/api/v1", func(api chi.Router) { controller.Mount(api, ctrl, authn, guard, log) })
	return &harness{h: r, fakes: fakes, core: core}
}

func (h *harness) get(t *testing.T, path string, caller *testkit.Caller) testkit.Response {
	t.Helper()
	return testkit.Get(t, h.h, "/api/v1/analytics"+path, caller)
}
