package boot

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	apperr "github.com/quickbite/analytics-service/lib/errors"
	httpx "github.com/quickbite/analytics-service/lib/http"
	"github.com/quickbite/analytics-service/lib/middleware"
)

// newRouter composes global middleware, /health, and the versioned API.
// Go analogue of app.ts + routes.ts. mountAPI is where modules register.
func newRouter(log *slog.Logger, health healthDeps, mountAPI func(api chi.Router)) http.Handler {
	r := chi.NewRouter()

	// Order matters: correlation id first so every later log line carries it.
	r.Use(middleware.Correlation(log))
	r.Use(middleware.AccessLog)
	r.Use(apperr.Recoverer(log))

	r.NotFound(apperr.NotFoundHandler())
	r.MethodNotAllowed(apperr.MethodNotAllowedHandler())

	r.Get("/health", apperr.Wrap(log, healthHandler(health)))
	r.Route("/api/v1", mountAPI)

	return r
}

// healthHandler: 200 when Mongo answers a ping and the AMQP connection is
// open; otherwise 503 UNHEALTHY so an orchestrator can pull the instance.
func healthHandler(d healthDeps) apperr.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		res := healthResponse{Status: "ok", Mongo: "up", Rabbit: "up"}
		if err := d.pingMongo(ctx); err != nil {
			res.Mongo, res.Status = "down", "degraded"
		}
		if !d.rabbitConnected() {
			res.Rabbit, res.Status = "down", "degraded"
		}
		if res.Status != "ok" {
			return apperr.ErrUnhealthy.WithMessage("mongo=" + res.Mongo + " rabbit=" + res.Rabbit)
		}
		return httpx.SendSuccess(w, http.StatusOK, res)
	}
}
