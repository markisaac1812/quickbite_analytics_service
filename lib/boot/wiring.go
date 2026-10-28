package boot

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/quickbite/analytics-service/app/analytics"
	"github.com/quickbite/analytics-service/app/analytics/eventhandlers"
	"github.com/quickbite/analytics-service/app/analytics/repository"
	"github.com/quickbite/analytics-service/app/analytics/service"
	"github.com/quickbite/analytics-service/lib/config"
	"github.com/quickbite/analytics-service/lib/coreevents"
	mongopkg "github.com/quickbite/analytics-service/pkg/mongo"
)

// analyticsModule builds the analytics module's repositories, service and
// event dispatcher. Shared by the API process (Run) and cmd/backfill-aggs
// (RunBackfill), so both apply events through the exact same code.
func analyticsModule(cfg config.Config, db *mongopkg.Database, tx coreevents.TxRunner, log *slog.Logger) (*service.Service, *coreevents.Dispatcher, error) {
	svc := service.New(analytics.Stores{
		Restaurants: repository.NewRestaurantDayRepo(db),
		Branches:    repository.NewBranchDayRepo(db),
		Products:    repository.NewProductDayRepo(db),
		Platform:    repository.NewPlatformDayRepo(db),
		Timelines:   repository.NewOrderTimelineRepo(db),
	})

	dispatcher := coreevents.NewDispatcher(repository.NewEventIDsRepo(db), tx, cfg.Events.HandlerTimeout, log)
	if err := eventhandlers.Register(dispatcher, svc); err != nil {
		return nil, nil, err
	}
	return svc, dispatcher, nil
}

// txRunner resolves MONGO_TRANSACTIONS (auto|on|off) against the server.
// Returns nil when applies must run without transactions.
func txRunner(ctx context.Context, mode string, client *mongopkg.Client, log *slog.Logger) (coreevents.TxRunner, error) {
	if mode == "off" {
		log.Warn("mongo transactions disabled (MONGO_TRANSACTIONS=off): multi-collection applies are not atomic")
		return nil, nil
	}
	ok, err := mongopkg.SupportsTransactions(ctx, client)
	if err != nil {
		return nil, err
	}
	switch {
	case ok:
		log.Info("mongo transactions enabled: each event applies exactly once, atomically")
		return mongopkg.NewTxRunner(client), nil
	case mode == "on":
		return nil, fmt.Errorf("MONGO_TRANSACTIONS=on but the server is a standalone mongod (needs a replica set)")
	default:
		log.Warn("mongo is standalone: transactions unavailable, multi-collection applies are not atomic " +
			"(run mongod as a replica set for exactly-once)")
		return nil, nil
	}
}
