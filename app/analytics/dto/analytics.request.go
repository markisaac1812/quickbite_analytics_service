// Package dto holds the wire shapes: request DTOs (validated on the way in)
// and response DTOs (the only thing a controller is allowed to send).
package dto

// Path + query are copied into these structs by the controller, then validated
// with go-playground/validator tags (the Go analogue of class-validator
// decorators). Unparseable numbers are copied as -1/0 so the tag reports them.

// RestaurantRangeRequest: /restaurants/{restaurantId}/{days|failures|delivery-avg|payments}?from=&to=
type RestaurantRangeRequest struct {
	RestaurantID int64  `json:"restaurantId" validate:"gt=0"`
	From         string `json:"from" validate:"required,datetime=2006-01-02"`
	To           string `json:"to" validate:"required,datetime=2006-01-02"`
}

// BranchDaysRequest: /restaurants/{restaurantId}/branches/{branchId}/days?from=&to=
type BranchDaysRequest struct {
	RestaurantID int64  `json:"restaurantId" validate:"gt=0"`
	BranchID     int64  `json:"branchId" validate:"gt=0"`
	From         string `json:"from" validate:"required,datetime=2006-01-02"`
	To           string `json:"to" validate:"required,datetime=2006-01-02"`
}

// TopProductsRequest: /restaurants/{restaurantId}/products/top?from=&to=&limit=
// limit is optional (0 → default 10).
type TopProductsRequest struct {
	RestaurantID int64  `json:"restaurantId" validate:"gt=0"`
	From         string `json:"from" validate:"required,datetime=2006-01-02"`
	To           string `json:"to" validate:"required,datetime=2006-01-02"`
	Limit        int    `json:"limit" validate:"gte=0,lte=100"`
}

// PlatformRangeRequest: /platform/{days|active-restaurants}?from=&to=
type PlatformRangeRequest struct {
	From string `json:"from" validate:"required,datetime=2006-01-02"`
	To   string `json:"to" validate:"required,datetime=2006-01-02"`
}

// TopRestaurantsRequest: /platform/top-restaurants?from=&to=&currency=&limit=
// currency is required — revenue in different currencies can't be ranked together.
type TopRestaurantsRequest struct {
	From     string `json:"from" validate:"required,datetime=2006-01-02"`
	To       string `json:"to" validate:"required,datetime=2006-01-02"`
	Currency string `json:"currency" validate:"required,len=3,uppercase"`
	Limit    int    `json:"limit" validate:"gte=0,lte=100"`
}
