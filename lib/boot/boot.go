// Package boot wires every singleton by hand and runs the process until a
// signal arrives. It is the Go analogue of server.ts + lib/di/container.ts:
// no DI framework, just constructors called in dependency order.
//
// Layering exception: like container.ts on the Node side, boot is the ONE
// place in lib/ allowed to import app/<module> — it has to, to wire it.
package boot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/quickbite/analytics-service/app/analytics/controller"
	"github.com/quickbite/analytics-service/app/analytics/repository"
	"github.com/quickbite/analytics-service/lib/auth"
	"github.com/quickbite/analytics-service/lib/config"
	"github.com/quickbite/analytics-service/lib/coreclient"
	"github.com/quickbite/analytics-service/lib/coreevents"
	"github.com/quickbite/analytics-service/lib/logger"
	"github.com/quickbite/analytics-service/lib/rbac"
	"github.com/quickbite/analytics-service/pkg/messaging"
	mongopkg "github.com/quickbite/analytics-service/pkg/mongo"
)

// Run boots the service and blocks until SIGINT/SIGTERM, then shuts down
// gracefully. A non-nil error means boot (or shutdown) failed; it has
// already been logged.
func Run() error {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "error", err.Error())
		return err
	}

	log := logger.New(cfg.LogLevel, cfg.AppEnv)
	slog.SetDefault(log)

	if err := run(cfg, log); err != nil {
		log.Error("analytics-service stopped with error", "error", err.Error())
		return err
	}
	return nil
}

func run(cfg config.Config, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ── 1. MongoDB ─────────────────────────────────────────────────────────
	bootCtx, cancelBoot := context.WithTimeout(ctx, cfg.Mongo.ConnectTimeout+5*time.Second)
	defer cancelBoot()

	mongoClient, db, err := mongopkg.Connect(bootCtx, mongopkg.Config{
		URI:            cfg.Mongo.URI,
		Database:       cfg.Mongo.Database,
		ConnectTimeout: cfg.Mongo.ConnectTimeout,
		MaxPoolSize:    cfg.Mongo.MaxPoolSize,
		AppName:        "analytics-service",
	})
	if err != nil {
		return err
	}
	defer func() {
		dctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := mongopkg.Disconnect(dctx, mongoClient); err != nil {
			log.Warn("mongo disconnect error", "error", err.Error())
		}
		log.Info("mongo disconnected")
	}()
	log.Info("mongo connected", "database", cfg.Mongo.Database)

	if err := repository.EnsureIndexes(bootCtx, db); err != nil {
		return err
	}
	log.Info("mongo indexes ensured")

	// ── 2. RabbitMQ ────────────────────────────────────────────────────────
	broker := messaging.NewAMQPBroker(messaging.Config{
		URL:              cfg.Rabbit.URL,
		ReconnectInitial: 500 * time.Millisecond,
		ReconnectMax:     cfg.Rabbit.ReconnectMax,
		ConnectAttempts:  cfg.Rabbit.ConnectAttempts,
	}, log)
	if err := broker.Connect(ctx); err != nil {
		return err
	}
	log.Info("rabbit connected")

	tx, err := txRunner(bootCtx, cfg.Mongo.Transactions, mongoClient, log)
	if err != nil {
		return err
	}

	// ── 3. Cross-cutting singletons ───────────────────────────────────────
	core, err := coreclient.New(cfg.Core.BaseURL, cfg.Core.InternalAPIKey, cfg.Core.Timeout, cfg.Core.Attempts)
	if err != nil {
		return fmt.Errorf("core client: %w", err)
	}
	permCache := rbac.NewCache(core, cfg.RBAC.CacheTTL)
	guard := rbac.NewGuard(permCache, log)
	authn := auth.NewMiddleware(auth.NewVerifier(cfg.JWT.AccessSecret), log)

	// ── 4. analytics module ───────────────────────────────────────────────
	analyticsSvc, orderEvents, err := analyticsModule(cfg, db, tx, log)
	if err != nil {
		return err
	}
	analyticsCtrl := controller.New(analyticsSvc)

	// ── 5. Event consumers ────────────────────────────────────────────────
	// a) order.events → aggregates (Mongo dedupe, transactional when possible)
	orderConsumer := coreevents.NewConsumer(broker, orderEvents, messaging.Topology{
		Exchange:           cfg.Rabbit.Exchange,
		Queue:              cfg.Rabbit.Queue,
		BindingKeys:        cfg.Rabbit.Bindings,
		DeadLetterExchange: cfg.Rabbit.DLX,
		DeadLetterQueue:    cfg.Rabbit.DLQ,
		Prefetch:           cfg.Rabbit.Prefetch,
	}, log)
	if err := orderConsumer.Start(ctx); err != nil {
		_ = broker.Close(context.Background())
		return err
	}

	// b) core.events (rbac.#) → permission cache invalidation. Idempotent, so
	//    no dedupe store and no transaction.
	if cfg.CoreEvents.Enabled {
		coreEvents := coreevents.NewDispatcher(coreevents.NoDedupe{}, nil, cfg.Events.HandlerTimeout, log)
		if err := coreEvents.Register(rbac.EventPermissionsChanged, permCache.HandlePermissionsChanged); err != nil {
			return err
		}
		coreConsumer := coreevents.NewConsumer(broker, coreEvents, messaging.Topology{
			Exchange:           cfg.CoreEvents.Exchange,
			Queue:              cfg.CoreEvents.Queue,
			BindingKeys:        cfg.CoreEvents.Bindings,
			DeadLetterExchange: cfg.CoreEvents.DLX,
			DeadLetterQueue:    cfg.CoreEvents.DLQ,
			Prefetch:           cfg.CoreEvents.Prefetch,
		}, log)
		if err := coreConsumer.Start(ctx); err != nil {
			_ = broker.Close(context.Background())
			return err
		}
	}

	// ── 6. HTTP ───────────────────────────────────────────────────────────
	router := newRouter(log, healthDeps{
		pingMongo:       func(ctx context.Context) error { return mongopkg.Ping(ctx, mongoClient) },
		rabbitConnected: broker.IsConnected,
	}, func(api chi.Router) {
		controller.Mount(api, analyticsCtrl, authn, guard, log)
	})

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
		close(serveErr)
	}()
	log.Info("http listening", "port", cfg.Port)

	// ── 7. Wait, then shut down in reverse dependency order ──────────────
	var runErr error
	select {
	case <-ctx.Done():
		log.Info("shutdown requested")
	case err := <-serveErr:
		runErr = fmt.Errorf("http server: %w", err)
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	// a) stop accepting HTTP, let in-flight requests finish
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Warn("http shutdown error", "error", err.Error())
	}
	log.Info("http stopped")

	// b) stop consuming, let in-flight event handlers finish, close AMQP
	if err := broker.Close(shutCtx); err != nil {
		log.Warn("rabbit close error", "error", err.Error())
	}
	log.Info("rabbit closed")

	// c) mongo disconnects in the deferred func above — last, because a and b use it
	return runErr
}
