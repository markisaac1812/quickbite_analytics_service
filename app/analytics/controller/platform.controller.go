package controller

import (
	"net/http"

	"github.com/quickbite/analytics-service/app/analytics"
	"github.com/quickbite/analytics-service/app/analytics/dto"
	httpx "github.com/quickbite/analytics-service/lib/http"
	"github.com/quickbite/analytics-service/lib/validation"
)

// PlatformDays handles GET /platform/days.
func (c *Controller) PlatformDays(w http.ResponseWriter, r *http.Request) error {
	in, _, err := platformRange(r)
	if err != nil {
		return err
	}
	rows, err := c.svc.PlatformDays(r.Context(), in)
	if err != nil {
		return err
	}
	return httpx.SendSuccess(w, http.StatusOK, dto.PlatformDaysFrom(rows))
}

// ActiveRestaurants handles GET /platform/active-restaurants.
func (c *Controller) ActiveRestaurants(w http.ResponseWriter, r *http.Request) error {
	in, req, err := platformRange(r)
	if err != nil {
		return err
	}
	rep, err := c.svc.ActiveRestaurants(r.Context(), in)
	if err != nil {
		return err
	}
	return httpx.SendSuccess(w, http.StatusOK, dto.ActiveRestaurantsFrom(req.From, req.To, rep))
}

// TopRestaurants handles GET /platform/top-restaurants.
func (c *Controller) TopRestaurants(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	req := dto.TopRestaurantsRequest{
		From:     q.Get("from"),
		To:       q.Get("to"),
		Currency: q.Get("currency"),
		Limit:    parseLimit(q.Get("limit")),
	}
	if err := validation.Struct(req); err != nil {
		return err
	}
	from, to := parseDates(req.From, req.To)
	rows, err := c.svc.TopRestaurants(r.Context(), analytics.TopRestaurantsInput{
		From: from, To: to, Currency: req.Currency, Limit: req.Limit,
	})
	if err != nil {
		return err
	}
	return httpx.SendSuccess(w, http.StatusOK, dto.RestaurantRanksFrom(rows))
}

func platformRange(r *http.Request) (analytics.RangeInput, dto.PlatformRangeRequest, error) {
	q := r.URL.Query()
	req := dto.PlatformRangeRequest{From: q.Get("from"), To: q.Get("to")}
	if err := validation.Struct(req); err != nil {
		return analytics.RangeInput{}, req, err
	}
	from, to := parseDates(req.From, req.To)
	return analytics.RangeInput{From: from, To: to}, req, nil
}
