package dto

import "github.com/quickbite/analytics-service/app/analytics"

// Money is integer minor units with the currency next to it. Durations are
// integer milliseconds. Rates are 0..1 with 4 decimals. Dates are YYYY-MM-DD
// (UTC day). Every list is non-nil so it renders `[]`, never `null`.

// ── /restaurants/{id}/days and /restaurants/{id}/branches/{bid}/days ──

type RestaurantDayResponse struct {
	Date          string `json:"date"`
	OrdersCount   int64  `json:"ordersCount"`
	RevenueMinor  int64  `json:"revenueMinor"`
	Currency      string `json:"currency"`
	AvgOrderMinor int64  `json:"avgOrderMinor"`
}

func RestaurantDayFrom(row analytics.RestaurantDayRow) RestaurantDayResponse {
	return RestaurantDayResponse{
		Date:          row.Date,
		OrdersCount:   row.OrdersCount,
		RevenueMinor:  row.RevenueMinor,
		Currency:      row.Currency,
		AvgOrderMinor: row.AvgOrderMinor,
	}
}

func RestaurantDaysFrom(rows []analytics.RestaurantDayRow) []RestaurantDayResponse {
	out := make([]RestaurantDayResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, RestaurantDayFrom(r))
	}
	return out
}

// ── /restaurants/{id}/failures ──

type FailureDayResponse struct {
	Date          string  `json:"date"`
	OrdersCount   int64   `json:"ordersCount"`
	RejectedCount int64   `json:"rejectedCount"`
	FailureRate   float64 `json:"failureRate"`
}

type FailureReportResponse struct {
	From          string               `json:"from"`
	To            string               `json:"to"`
	OrdersCount   int64                `json:"ordersCount"`
	RejectedCount int64                `json:"rejectedCount"`
	FailureRate   float64              `json:"failureRate"`
	Days          []FailureDayResponse `json:"days"`
}

func FailureReportFrom(from, to string, r analytics.FailureReport) FailureReportResponse {
	days := make([]FailureDayResponse, 0, len(r.Days))
	for _, d := range r.Days {
		days = append(days, FailureDayResponse{Date: d.Date, OrdersCount: d.OrdersCount, RejectedCount: d.RejectedCount, FailureRate: d.FailureRate})
	}
	return FailureReportResponse{From: from, To: to, OrdersCount: r.OrdersCount, RejectedCount: r.RejectedCount, FailureRate: r.FailureRate, Days: days}
}

// ── /restaurants/{id}/delivery-avg ──

type DeliveryDayResponse struct {
	Date           string `json:"date"`
	DeliveredCount int64  `json:"deliveredCount"`
	AvgDeliveryMs  int64  `json:"avgDeliveryMs"`
}

type DeliveryReportResponse struct {
	From           string                `json:"from"`
	To             string                `json:"to"`
	DeliveredCount int64                 `json:"deliveredCount"`
	AvgDeliveryMs  int64                 `json:"avgDeliveryMs"`
	Days           []DeliveryDayResponse `json:"days"`
}

func DeliveryReportFrom(from, to string, r analytics.DeliveryReport) DeliveryReportResponse {
	days := make([]DeliveryDayResponse, 0, len(r.Days))
	for _, d := range r.Days {
		days = append(days, DeliveryDayResponse{Date: d.Date, DeliveredCount: d.DeliveredCount, AvgDeliveryMs: d.AvgDeliveryMs})
	}
	return DeliveryReportResponse{From: from, To: to, DeliveredCount: r.DeliveredCount, AvgDeliveryMs: r.AvgDeliveryMs, Days: days}
}

// ── /restaurants/{id}/payments ──

type PaymentDayResponse struct {
	Date          string `json:"date"`
	PaymentsCount int64  `json:"paymentsCount"`
	PaymentsMinor int64  `json:"paymentsMinor"`
	Currency      string `json:"currency"`
}

type PaymentsReportResponse struct {
	From          string               `json:"from"`
	To            string               `json:"to"`
	PaymentsCount int64                `json:"paymentsCount"`
	PaymentsMinor int64                `json:"paymentsMinor"`
	Currency      string               `json:"currency,omitempty"`
	Days          []PaymentDayResponse `json:"days"`
}

func PaymentsReportFrom(from, to string, r analytics.PaymentsReport) PaymentsReportResponse {
	days := make([]PaymentDayResponse, 0, len(r.Days))
	for _, d := range r.Days {
		days = append(days, PaymentDayResponse{Date: d.Date, PaymentsCount: d.PaymentsCount, PaymentsMinor: d.PaymentsMinor, Currency: d.Currency})
	}
	return PaymentsReportResponse{From: from, To: to, PaymentsCount: r.PaymentsCount, PaymentsMinor: r.PaymentsMinor, Currency: r.Currency, Days: days}
}

// ── /restaurants/{id}/products/top ──

type ProductRankResponse struct {
	ProductID    int64  `json:"productId"`
	UnitsSold    int64  `json:"unitsSold"`
	RevenueMinor int64  `json:"revenueMinor"`
	OrdersCount  int64  `json:"ordersCount"`
	Currency     string `json:"currency"`
}

func ProductRanksFrom(rows []analytics.ProductRankRow) []ProductRankResponse {
	out := make([]ProductRankResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, ProductRankResponse{ProductID: r.ProductID, UnitsSold: r.UnitsSold, RevenueMinor: r.RevenueMinor, OrdersCount: r.OrdersCount, Currency: r.Currency})
	}
	return out
}

// ── /platform/days ──

type PlatformDayResponse struct {
	Date           string  `json:"date"`
	Currency       string  `json:"currency"`
	OrdersCount    int64   `json:"ordersCount"`
	RevenueMinor   int64   `json:"revenueMinor"`
	AvgOrderMinor  int64   `json:"avgOrderMinor"`
	RejectedCount  int64   `json:"rejectedCount"`
	FailureRate    float64 `json:"failureRate"`
	DeliveredCount int64   `json:"deliveredCount"`
	AvgDeliveryMs  int64   `json:"avgDeliveryMs"`
	PaymentsCount  int64   `json:"paymentsCount"`
	PaymentsMinor  int64   `json:"paymentsMinor"`
}

func PlatformDaysFrom(rows []analytics.PlatformDayRow) []PlatformDayResponse {
	out := make([]PlatformDayResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, PlatformDayResponse{
			Date: r.Date, Currency: r.Currency,
			OrdersCount: r.OrdersCount, RevenueMinor: r.RevenueMinor, AvgOrderMinor: r.AvgOrderMinor,
			RejectedCount: r.RejectedCount, FailureRate: r.FailureRate,
			DeliveredCount: r.DeliveredCount, AvgDeliveryMs: r.AvgDeliveryMs,
			PaymentsCount: r.PaymentsCount, PaymentsMinor: r.PaymentsMinor,
		})
	}
	return out
}

// ── /platform/active-restaurants ──

type ActiveRestaurantsDayResponse struct {
	Date              string `json:"date"`
	ActiveRestaurants int64  `json:"activeRestaurants"`
}

type ActiveRestaurantsResponse struct {
	From              string                         `json:"from"`
	To                string                         `json:"to"`
	ActiveRestaurants int64                          `json:"activeRestaurants"`
	Days              []ActiveRestaurantsDayResponse `json:"days"`
}

func ActiveRestaurantsFrom(from, to string, r analytics.ActiveRestaurantsReport) ActiveRestaurantsResponse {
	days := make([]ActiveRestaurantsDayResponse, 0, len(r.Days))
	for _, d := range r.Days {
		days = append(days, ActiveRestaurantsDayResponse{Date: d.Date, ActiveRestaurants: d.ActiveRestaurants})
	}
	return ActiveRestaurantsResponse{From: from, To: to, ActiveRestaurants: r.ActiveRestaurants, Days: days}
}

// ── /platform/top-restaurants ──

type RestaurantRankResponse struct {
	RestaurantID  int64  `json:"restaurantId"`
	OrdersCount   int64  `json:"ordersCount"`
	RevenueMinor  int64  `json:"revenueMinor"`
	AvgOrderMinor int64  `json:"avgOrderMinor"`
	Currency      string `json:"currency"`
}

func RestaurantRanksFrom(rows []analytics.RestaurantRankRow) []RestaurantRankResponse {
	out := make([]RestaurantRankResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, RestaurantRankResponse{RestaurantID: r.RestaurantID, OrdersCount: r.OrdersCount, RevenueMinor: r.RevenueMinor, AvgOrderMinor: r.AvgOrderMinor, Currency: r.Currency})
	}
	return out
}
