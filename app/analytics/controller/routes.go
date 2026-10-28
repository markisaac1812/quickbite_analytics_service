package controller

import (
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/quickbite/analytics-service/app/analytics"
	"github.com/quickbite/analytics-service/lib/auth"
	apperr "github.com/quickbite/analytics-service/lib/errors"
	"github.com/quickbite/analytics-service/lib/rbac"
)

// Mount registers the module's routes under the router it is given
// (lib/boot mounts it at /api/v1). Go analogue of app/<module>/routes.ts.
//
// Restaurant-scoped (authenticate → analytics:read → own restaurant):
//
//	GET /analytics/restaurants/{restaurantId}/days?from=&to=
//	GET /analytics/restaurants/{restaurantId}/failures?from=&to=
//	GET /analytics/restaurants/{restaurantId}/delivery-avg?from=&to=
//	GET /analytics/restaurants/{restaurantId}/payments?from=&to=
//	GET /analytics/restaurants/{restaurantId}/products/top?from=&to=&limit=
//	GET /analytics/restaurants/{restaurantId}/branches/{branchId}/days?from=&to=   (+ own branch)
//
// Platform-wide (authenticate → system_admin):
//
//	GET /analytics/platform/days?from=&to=
//	GET /analytics/platform/active-restaurants?from=&to=
//	GET /analytics/platform/top-restaurants?from=&to=&currency=&limit=
func Mount(r chi.Router, ctrl *Controller, authn *auth.Middleware, guard *rbac.Guard, log *slog.Logger) {
	r.Route("/analytics", func(r chi.Router) {
		r.Use(authn.Authenticate)

		r.Route("/restaurants/{restaurantId}", func(r chi.Router) {
			r.Use(guard.Require(analytics.PermAnalyticsRead), guard.RequireRestaurantAccess("restaurantId"))

			r.Get("/days", apperr.Wrap(log, ctrl.ListRestaurantDays))
			r.Get("/failures", apperr.Wrap(log, ctrl.RestaurantFailures))
			r.Get("/delivery-avg", apperr.Wrap(log, ctrl.RestaurantDeliveryAvg))
			r.Get("/payments", apperr.Wrap(log, ctrl.RestaurantPayments))
			r.Get("/products/top", apperr.Wrap(log, ctrl.TopProducts))
			r.With(guard.RequireBranchAccess("branchId")).
				Get("/branches/{branchId}/days", apperr.Wrap(log, ctrl.ListBranchDays))
		})

		r.Route("/platform", func(r chi.Router) {
			r.Use(guard.RequireRole(rbac.RoleSystemAdmin))

			r.Get("/days", apperr.Wrap(log, ctrl.PlatformDays))
			r.Get("/active-restaurants", apperr.Wrap(log, ctrl.ActiveRestaurants))
			r.Get("/top-restaurants", apperr.Wrap(log, ctrl.TopRestaurants))
		})
	})
}
