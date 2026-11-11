// Package api implements the edge-specific HTTP API.
//
// The control-plane REST surface (users, instances, signup, versions, stats,
// realtime) is served by the original mothership hooks running inside the
// PocketBase SDK — see internal/mothership. This package only adds the routes
// that belong to the edge: the daemon admin API and the embedded dashboard.
package api

import (
	"encoding/json"
	"net/http"
)

// APIError is PocketBase's error envelope.
type APIError struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, APIError{Code: status, Message: msg, Data: map[string]any{}})
}

// decodeJSON decodes a request body into v, capping the size to keep a single
// bad request from exhausting the 512 MB edge budget.
func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 8<<20))
	return dec.Decode(v)
}
