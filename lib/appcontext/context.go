// Package appcontext holds the typed request-scoped values every layer may
// read from a context.Context. Go analogue of lib/types/express.d.ts, where
// Node hangs `req.user` and `req.correlationId` off the request object.
package appcontext

import "context"

// Claims is the verified JWT payload — the same contract core-service signs
// and order-service reads (req.user on the Node side).
type Claims struct {
	UserID         int64   `json:"userId"`
	Role           string  `json:"role"`
	Email          string  `json:"email,omitempty"`
	RestaurantID   *int64  `json:"restaurantId,omitempty"`
	RestaurantRole string  `json:"restaurantRole,omitempty"`
	BranchIDs      []int64 `json:"branchIds,omitempty"`
}

// Unexported key type: no other package can collide with (or forge) our keys.
type ctxKey int

const (
	claimsKey ctxKey = iota
	correlationIDKey
)

// WithClaims stores the authenticated user's claims.
func WithClaims(ctx context.Context, c Claims) context.Context {
	return context.WithValue(ctx, claimsKey, c)
}

// ClaimsFrom returns the claims and whether the request is authenticated.
func ClaimsFrom(ctx context.Context) (Claims, bool) {
	c, ok := ctx.Value(claimsKey).(Claims)
	return c, ok
}

// WithCorrelationID stores the request/event correlation id.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationIDKey, id)
}

// CorrelationID returns the correlation id, or "" if none was set.
func CorrelationID(ctx context.Context) string {
	id, _ := ctx.Value(correlationIDKey).(string)
	return id
}
