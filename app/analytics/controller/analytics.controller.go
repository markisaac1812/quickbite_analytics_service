// Package controller adapts HTTP to the service: parse → validate → call
// service → map to response DTO → send. No business logic here.
//
// Files: analytics.controller.go (restaurant-scoped endpoints),
// platform.controller.go (system_admin endpoints), routes.go.
package controller

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/quickbite/analytics-service/app/analytics"
	"github.com/quickbite/analytics-service/app/analytics/dto"
	"github.com/quickbite/analytics-service/app/analytics/service"
	httpx "github.com/quickbite/analytics-service/lib/http"
	"github.com/quickbite/analytics-service/lib/validation"
)

type Controller struct {
	svc *service.Service
}

func New(svc *service.Service) *Controller {
	return &Controller{svc: svc}
}

// ListRestaurantDays handles GET /restaurants/{restaurantId}/days.
func (c *Controller) ListRestaurantDays(w http.ResponseWriter, r *http.Request) error {
	in, _, err := restaurantRange(r)
	if err != nil {
		return err
	}
	rows, err := c.svc.ListRestaurantDays(r.Context(), in)
	if err != nil {
		return err
	}
	return httpx.SendSuccess(w, http.StatusOK, dto.RestaurantDaysFrom(rows))
}

// RestaurantFailures handles GET /restaurants/{restaurantId}/failures.
func (c *Controller) RestaurantFailures(w http.ResponseWriter, r *http.Request) error {
	in, req, err := restaurantRange(r)
	if err != nil {
		return err
	}
	rep, err := c.svc.RestaurantFailures(r.Context(), in)
	if err != nil {
		return err
	}
	return httpx.SendSuccess(w, http.StatusOK, dto.FailureReportFrom(req.From, req.To, rep))
}

// RestaurantDeliveryAvg handles GET /restaurants/{restaurantId}/delivery-avg.
func (c *Controller) RestaurantDeliveryAvg(w http.ResponseWriter, r *http.Request) error {
	in, req, err := restaurantRange(r)
	if err != nil {
		return err
	}
	rep, err := c.svc.RestaurantDeliveryAvg(r.Context(), in)
	if err != nil {
		return err
	}
	return httpx.SendSuccess(w, http.StatusOK, dto.DeliveryReportFrom(req.From, req.To, rep))
}

// RestaurantPayments handles GET /restaurants/{restaurantId}/payments.
func (c *Controller) RestaurantPayments(w http.ResponseWriter, r *http.Request) error {
	in, req, err := restaurantRange(r)
	if err != nil {
		return err
	}
	rep, err := c.svc.RestaurantPayments(r.Context(), in)
	if err != nil {
		return err
	}
	return httpx.SendSuccess(w, http.StatusOK, dto.PaymentsReportFrom(req.From, req.To, rep))
}

// TopProducts handles GET /restaurants/{restaurantId}/products/top.
func (c *Controller) TopProducts(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	req := dto.TopProductsRequest{
		RestaurantID: parseID(chi.URLParam(r, "restaurantId")),
		From:         q.Get("from"),
		To:           q.Get("to"),
		Limit:        parseLimit(q.Get("limit")),
	}
	if err := validation.Struct(req); err != nil {
		return err
	}
	from, to := parseDates(req.From, req.To)
	rows, err := c.svc.TopProducts(r.Context(), analytics.TopProductsInput{
		RestaurantID: req.RestaurantID, From: from, To: to, Limit: req.Limit,
	})
	if err != nil {
		return err
	}
	return httpx.SendSuccess(w, http.StatusOK, dto.ProductRanksFrom(rows))
}

// ListBranchDays handles GET /restaurants/{restaurantId}/branches/{branchId}/days.
func (c *Controller) ListBranchDays(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	req := dto.BranchDaysRequest{
		RestaurantID: parseID(chi.URLParam(r, "restaurantId")),
		BranchID:     parseID(chi.URLParam(r, "branchId")),
		From:         q.Get("from"),
		To:           q.Get("to"),
	}
	if err := validation.Struct(req); err != nil {
		return err
	}
	from, to := parseDates(req.From, req.To)
	rows, err := c.svc.ListBranchDays(r.Context(), analytics.ListBranchDaysInput{
		RestaurantID: req.RestaurantID, BranchID: req.BranchID, From: from, To: to,
	})
	if err != nil {
		return err
	}
	return httpx.SendSuccess(w, http.StatusOK, dto.RestaurantDaysFrom(rows))
}

// restaurantRange parses + validates {restaurantId}?from=&to=.
func restaurantRange(r *http.Request) (analytics.ListRestaurantDaysInput, dto.RestaurantRangeRequest, error) {
	q := r.URL.Query()
	req := dto.RestaurantRangeRequest{
		RestaurantID: parseID(chi.URLParam(r, "restaurantId")),
		From:         q.Get("from"),
		To:           q.Get("to"),
	}
	if err := validation.Struct(req); err != nil {
		return analytics.ListRestaurantDaysInput{}, req, err
	}
	from, to := parseDates(req.From, req.To)
	return analytics.ListRestaurantDaysInput{RestaurantID: req.RestaurantID, From: from, To: to}, req, nil
}

// parseID returns 0 for anything that isn't a base-10 int64, so the
// validator reports it as "<field> must be a positive integer".
func parseID(raw string) int64 {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// parseLimit: "" → 0 (use default); not a number → -1 (validator rejects it).
func parseLimit(raw string) int {
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return -1
	}
	return n
}

// parseDates: formats are already validated, so Parse cannot fail here.
func parseDates(from, to string) (time.Time, time.Time) {
	f, _ := time.Parse(analytics.DateLayout, from)
	t, _ := time.Parse(analytics.DateLayout, to)
	return f, t
}
