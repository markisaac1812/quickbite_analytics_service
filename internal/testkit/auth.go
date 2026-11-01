package testkit

import (
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/quickbite/analytics-service/lib/appcontext"
)

// Restaurant / branch ids the fixtures belong to.
const (
	RestaurantID      int64 = 42
	OtherRestaurantID int64 = 99
	BranchID          int64 = 7
	OtherBranchID     int64 = 8
)

func ptr[T any](v T) *T { return &v }

// Caller is who a test request is authenticated as (the JWT claims).
type Caller = appcontext.Claims

// Caller fixtures (≈ tests/helpers/auth.ts on the Node side).
var (
	Owner = appcontext.Claims{
		UserID: 10, Role: "restaurant_user", Email: "owner@quickbite.test",
		RestaurantID: ptr(RestaurantID), RestaurantRole: "owner",
	}
	BranchManager = appcontext.Claims{
		UserID: 11, Role: "restaurant_user",
		RestaurantID: ptr(RestaurantID), RestaurantRole: "branch_manager", BranchIDs: []int64{BranchID},
	}
	Staff = appcontext.Claims{
		UserID: 12, Role: "restaurant_user",
		RestaurantID: ptr(RestaurantID), RestaurantRole: "staff", BranchIDs: []int64{BranchID},
	}
	OtherOwner = appcontext.Claims{
		UserID: 20, Role: "restaurant_user",
		RestaurantID: ptr(OtherRestaurantID), RestaurantRole: "owner",
	}
	Admin    = appcontext.Claims{UserID: 1, Role: "system_admin"}
	Customer = appcontext.Claims{UserID: 30, Role: "customer"}
)

// RolePermissions is what the core stub answers by default: the seed of core
// migration 20261028180000_seed_analytics_service_permissions.ts.
func RolePermissions() map[string][]string {
	return map[string][]string{
		"owner":          {"analytics:read", "orders:read"},
		"branch_manager": {"analytics:read"},
		"staff":          {"orders:read"},
	}
}

// SignToken signs claims the way core-service does (HS256, ACCESS_SECRET).
// A negative ttl yields an already-expired token.
func SignToken(c appcontext.Claims, ttl time.Duration) string {
	return SignTokenWith(AccessSecret, c, ttl)
}

// SignTokenWith signs with an arbitrary secret (e.g. to forge a bad signature).
func SignTokenWith(secret string, c appcontext.Claims, ttl time.Duration) string {
	claims := jwt.MapClaims{
		"userId": c.UserID,
		"role":   c.Role,
		"exp":    time.Now().Add(ttl).Unix(),
		"iat":    time.Now().Unix(),
	}
	if c.Email != "" {
		claims["email"] = c.Email
	}
	if c.RestaurantID != nil {
		claims["restaurantId"] = *c.RestaurantID
	}
	if c.RestaurantRole != "" {
		claims["restaurantRole"] = c.RestaurantRole
	}
	if len(c.BranchIDs) > 0 {
		claims["branchIds"] = c.BranchIDs
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		panic(err)
	}
	return s
}

// AuthCookie is the access_token cookie for c (≈ authCookie() in Node tests).
func AuthCookie(c appcontext.Claims) *http.Cookie {
	return &http.Cookie{Name: "access_token", Value: SignToken(c, time.Hour)}
}
