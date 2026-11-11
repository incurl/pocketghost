package api

import (
	"errors"
	"net/http"

	"github.com/incurl/pocketghost/internal/store"
)

// handleInstanceDelete serves `DELETE /api/instance/{id}`.
func (s *Server) handleInstanceDelete(w http.ResponseWriter, r *http.Request) {
	u := s.userFromRequest(r)
	if u == nil {
		writeError(w, http.StatusUnauthorized, "The request requires valid record authorization token.")
		return
	}
	inst, err := s.repo.GetInstance(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "The requested resource wasn't found.")
		return
	}
	if inst.UID != u.ID && !s.isSuperuser(r) {
		writeError(w, http.StatusForbidden, "Only the record owner can delete this record.")
		return
	}
	if inst.Status != store.StatusIdle {
		writeError(w, http.StatusBadRequest, "Instance must be shut down first.")
		return
	}
	if s.svc != nil {
		_ = s.svc.Stop(r.Context(), inst.ID)
	}
	if err := s.repo.DeleteInstance(r.Context(), inst.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.repo.Audit(r.Context(), u.ID, "instance.delete", inst.Subdomain)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func asBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "true" || x == "1"
	case float64:
		return x != 0
	case int:
		return x != 0
	}
	return false
}

func secretKeyOK(k string) bool {
	if k == "" || k[0] < 'A' || k[0] > 'Z' {
		return false
	}
	for i := 1; i < len(k); i++ {
		c := k[i]
		if (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			continue
		}
		return false
	}
	return true
}
