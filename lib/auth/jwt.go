// Package auth verifies core-issued access tokens and guards routes.
package auth

import (
	"errors"

	"github.com/golang-jwt/jwt/v5"

	"github.com/quickbite/analytics-service/lib/appcontext"
	apperr "github.com/quickbite/analytics-service/lib/errors"
)

// Verifier checks HS256 access tokens signed with ACCESS_SECRET — the same
// secret core-service signs with and order-service verifies with.
type Verifier struct {
	secret []byte
	parser *jwt.Parser
}

func NewVerifier(secret string) *Verifier {
	return &Verifier{
		secret: []byte(secret),
		parser: jwt.NewParser(
			jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), // reject alg=none / RS256 confusion
			jwt.WithExpirationRequired(),
		),
	}
}

// Verify parses and validates the token, returning the app-level claims.
// Any failure (bad signature, expired, malformed, missing userId/role) is
// UNAUTHENTICATED — the caller never learns which check failed.
func (v *Verifier) Verify(token string) (appcontext.Claims, error) {
	var c jwtClaims
	_, err := v.parser.ParseWithClaims(token, &c, func(*jwt.Token) (any, error) { return v.secret, nil })
	if err != nil {
		return appcontext.Claims{}, apperr.ErrUnauthenticated.WithMessage("Invalid or expired token").WithCause(err)
	}
	if c.UserID == 0 || c.Role == "" {
		return appcontext.Claims{}, apperr.ErrUnauthenticated.WithMessage("Invalid or expired token").
			WithCause(errors.New("token missing userId/role"))
	}
	return appcontext.Claims{
		UserID:         c.UserID,
		Role:           c.Role,
		Email:          c.Email,
		RestaurantID:   c.RestaurantID,
		RestaurantRole: c.RestaurantRole,
		BranchIDs:      c.BranchIDs,
	}, nil
}
