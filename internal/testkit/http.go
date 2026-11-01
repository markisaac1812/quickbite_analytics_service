package testkit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/quickbite/analytics-service/lib/appcontext"
)

// Response is a decoded {success, data | error} envelope (≈ supertest's res).
type Response struct {
	Status  int
	Header  http.Header
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// DataAs decodes the data member into out.
func (r Response) DataAs(t testing.TB, out any) {
	t.Helper()
	if err := json.Unmarshal(r.Data, out); err != nil {
		t.Fatalf("decode data %s: %v", r.Data, err)
	}
}

// Get serves GET path on h in-process, authenticated as caller (nil = anonymous).
func Get(t testing.TB, h http.Handler, path string, caller *appcontext.Claims) Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if caller != nil {
		req.AddCookie(AuthCookie(*caller))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return decode(t, rec.Code, rec.Header(), rec.Body.Bytes())
}

// GetURL performs a real GET against a running server (baseURL + path).
func GetURL(t testing.TB, baseURL, path string, caller *appcontext.Claims) Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if caller != nil {
		req.AddCookie(AuthCookie(*caller))
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer res.Body.Close()
	var body json.RawMessage
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("GET %s: decode: %v", path, err)
	}
	return decode(t, res.StatusCode, res.Header, body)
}

func decode(t testing.TB, status int, h http.Header, body []byte) Response {
	t.Helper()
	r := Response{Status: status, Header: h}
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatalf("response is not a JSON envelope (%d): %s", status, body)
	}
	return r
}
