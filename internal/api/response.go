// Package api implements the control-plane HTTP API. It serves both the
// PocketBase-compatible routes the Svelte dashboard calls and the daemon admin
// API used by operators and the CLI.
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

func writeValidationError(w http.ResponseWriter, fields map[string]any) {
	writeJSON(w, http.StatusBadRequest, APIError{
		Code:    400,
		Message: "Failed to create record.",
		Data:    fields,
	})
}

// decodeJSON decodes a request body into v, capping the size to keep a single
// bad request from exhausting the 512 MB edge budget.
func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 8<<20))
	return dec.Decode(v)
}

// ListResult is PocketBase's list envelope.
type ListResult struct {
	Page       int `json:"page"`
	PerPage    int `json:"perPage"`
	TotalItems int `json:"totalItems"`
	TotalPages int `json:"totalPages"`
	Items      any `json:"items"`
}

func newListResult(items any, total, page, perPage int) ListResult {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 30
	}
	pages := (total + perPage - 1) / perPage
	if pages < 1 {
		pages = 1
	}
	return ListResult{Page: page, PerPage: perPage, TotalItems: total, TotalPages: pages, Items: items}
}
