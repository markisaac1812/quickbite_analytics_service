package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/quickbite/analytics-service/internal/testkit/assert"
	httpx "github.com/quickbite/analytics-service/lib/http"
)

func TestSendSuccess(t *testing.T) {
	// Arrange
	rec := httptest.NewRecorder()

	// Act
	err := httpx.SendSuccess(rec, http.StatusOK, map[string]int{"n": 1})

	// Assert
	assert.NoErr(t, err)
	assert.Equal(t, rec.Code, http.StatusOK)
	assert.Equal(t, rec.Header().Get("Content-Type"), "application/json; charset=utf-8")
	assert.Equal(t, rec.Body.String(), `{"success":true,"data":{"n":1}}`+"\n")
}

func TestSendPaginated(t *testing.T) {
	t.Run("renders a nil page as [] with its meta", func(t *testing.T) {
		// Arrange
		rec := httptest.NewRecorder()
		next := "c2"

		// Act
		err := httpx.SendPaginated[int](rec, nil, httpx.PaginationMeta{NextCursor: &next, HasMore: true})

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, rec.Body.String(), `{"success":true,"data":[],"meta":{"nextCursor":"c2","hasMore":true,"count":0}}`+"\n")
	})

	t.Run("renders the items", func(t *testing.T) {
		// Arrange
		rec := httptest.NewRecorder()

		// Act
		err := httpx.SendPaginated(rec, []int{1, 2}, httpx.PaginationMeta{Count: 2})

		// Assert
		assert.NoErr(t, err)
		assert.Equal(t, rec.Body.String(), `{"success":true,"data":[1,2],"meta":{"nextCursor":null,"hasMore":false,"count":2}}`+"\n")
	})
}

func TestSendError(t *testing.T) {
	// Arrange
	rec := httptest.NewRecorder()

	// Act
	err := httpx.SendError(rec, http.StatusTeapot, "TEAPOT", "short and stout")

	// Assert
	assert.NoErr(t, err)
	assert.Equal(t, rec.Code, http.StatusTeapot)
	assert.Equal(t, rec.Body.String(), `{"success":false,"error":{"code":"TEAPOT","message":"short and stout"}}`+"\n")
}
