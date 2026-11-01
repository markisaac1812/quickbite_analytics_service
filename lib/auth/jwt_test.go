package auth_test

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/quickbite/analytics-service/internal/testkit"
	"github.com/quickbite/analytics-service/internal/testkit/assert"
	"github.com/quickbite/analytics-service/lib/auth"
	apperr "github.com/quickbite/analytics-service/lib/errors"
)

func TestVerifier_Verify(t *testing.T) {
	v := auth.NewVerifier(testkit.AccessSecret)

	t.Run("returns the core-service claims of a valid token", func(t *testing.T) {
		// Arrange
		token := testkit.SignToken(testkit.BranchManager, time.Hour)

		// Act
		claims, err := v.Verify(token)

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, claims, testkit.BranchManager)
	})

	t.Run("rejects an expired token with UNAUTHENTICATED", func(t *testing.T) {
		// Arrange
		token := testkit.SignToken(testkit.Owner, -10*time.Second)

		// Act
		_, err := v.Verify(token)

		// Assert
		assert.ErrIs(t, err, apperr.ErrUnauthenticated)
	})

	t.Run("rejects a token signed with another secret", func(t *testing.T) {
		// Arrange
		token := testkit.SignTokenWith("not-our-secret", testkit.Owner, time.Hour)

		// Act
		_, err := v.Verify(token)

		// Assert
		assert.ErrIs(t, err, apperr.ErrUnauthenticated)
	})

	t.Run("rejects a non-HS256 algorithm", func(t *testing.T) {
		// Arrange
		token, _ := jwt.NewWithClaims(jwt.SigningMethodHS512, jwt.MapClaims{
			"userId": 1, "role": "system_admin", "exp": time.Now().Add(time.Hour).Unix(),
		}).SignedString([]byte(testkit.AccessSecret))

		// Act
		_, err := v.Verify(token)

		// Assert
		assert.ErrIs(t, err, apperr.ErrUnauthenticated)
	})

	t.Run("rejects a token without exp", func(t *testing.T) {
		// Arrange
		token, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"userId": 1, "role": "system_admin"}).
			SignedString([]byte(testkit.AccessSecret))

		// Act
		_, err := v.Verify(token)

		// Assert
		assert.ErrIs(t, err, apperr.ErrUnauthenticated)
	})

	t.Run("rejects a token missing userId or role", func(t *testing.T) {
		// Arrange
		token := testkit.SignToken(testkit.Caller{Role: "system_admin"}, time.Hour)

		// Act
		_, err := v.Verify(token)

		// Assert
		assert.ErrIs(t, err, apperr.ErrUnauthenticated)
		assert.Contains(t, err.Error(), "token missing userId/role")
	})

	t.Run("rejects garbage", func(t *testing.T) {
		// Arrange
		token := "not.a.jwt"

		// Act
		_, err := v.Verify(token)

		// Assert
		assert.ErrIs(t, err, apperr.ErrUnauthenticated)
	})
}
