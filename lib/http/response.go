// Package httpx writes the service's JSON response envelopes. Go analogue of
// lib/http/response.ts (sendSuccess / sendPaginated).
//
// The folder is lib/http; the package is named httpx so it never shadows
// net/http at call sites.
package httpx

import (
	"encoding/json"
	"net/http"
)

// SendSuccess writes {"success": true, "data": data}. Pass a Response DTO
// (or a slice of them) — never an entity.
func SendSuccess(w http.ResponseWriter, status int, data any) error {
	return writeJSON(w, status, SuccessEnvelope{Success: true, Data: data})
}

// SendPaginated writes {"success": true, "data": [...], "meta": {...}}.
func SendPaginated[T any](w http.ResponseWriter, data []T, meta PaginationMeta) error {
	if data == nil {
		data = []T{} // never render `null` for an empty page
	}
	return writeJSON(w, http.StatusOK, SuccessEnvelope{Success: true, Data: data, Meta: meta})
}

// SendError writes {"success": false, "error": {"code", "message"}}.
// Handlers don't call this directly — they return an error and apperr.Wrap
// renders it. Middleware that cannot return an error uses apperr.Write.
func SendError(w http.ResponseWriter, status int, code, message string) error {
	return writeJSON(w, status, ErrorEnvelope{
		Success: false,
		Error:   ErrorBody{Code: code, Message: message},
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(body)
}
