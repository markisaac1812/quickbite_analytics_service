package auth

import "github.com/golang-jwt/jwt/v5"

// jwtClaims is the wire shape core-service signs (see core's
// app/auth/utils.ts JwtPayload) plus the registered claims (exp, iat).
type jwtClaims struct {
	UserID         int64   `json:"userId"`
	Role           string  `json:"role"`
	Email          string  `json:"email,omitempty"`
	RestaurantID   *int64  `json:"restaurantId,omitempty"`
	RestaurantRole string  `json:"restaurantRole,omitempty"`
	BranchIDs      []int64 `json:"branchIds,omitempty"`
	jwt.RegisteredClaims
}
