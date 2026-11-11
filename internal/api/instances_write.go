package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/incurl/pocketghost/internal/store"
)

// handleInstanceCreate serves `POST /api/instance`.
func (s *Server) handleInstanceCreate(w http.ResponseWriter, r *http.Request) {
	u := s.userFromRequest(r)
	if u == nil {
		writeError(w, http.StatusUnauthorized, "The request requires valid record authorization token.")
		return
	}
	var body struct {
		Subdomain string `json:"subdomain"`
		Version   string `json:"version"`
		Region    string `json:"region"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	subdomain := strings.ToLower(strings.TrimSpace(body.Subdomain))
	if msg := instanceNameError(subdomain); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if _, err := s.repo.GetInstanceBySubdomain(r.Context(), subdomain); err == nil {
		writeError(w, http.StatusBadRequest, "Instance name "+subdomain+" is not available.")
		return
	}

	count, err := s.repo.CountInstancesByUID(r.Context(), u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if u.SubscriptionQuantity > 0 && count >= u.SubscriptionQuantity {
		writeError(w, http.StatusBadRequest,
			"Your plan includes "+strconv.Itoa(u.SubscriptionQuantity)+" instance(s). Upgrade to create more.")
		return
	}

	version := body.Version
	if version == "" {
		if versions := s.bins.Versions(); len(versions) > 0 {
			version = versions[0]
		} else {
			version = s.cfg.MothershipSemver
		}
	}

	inst := &store.Instance{
		Subdomain:  subdomain,
		UID:        u.ID,
		Power:      true,
		Status:     store.StatusIdle,
		Version:    version,
		SyncAdmin:  true,
		AutoVacuum: true,
		Dev:        true,
		Secrets:    map[string]string{},
		Edge:       s.cfg.EdgeID,
	}
	if err := s.repo.CreateInstance(r.Context(), inst); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.repo.Audit(r.Context(), u.ID, "instance.create", subdomain)
	_ = ensureInstanceDirs(s, inst.ID)

	writeJSON(w, http.StatusOK, map[string]any{"instance": inst})
}

// handleInstanceUpdate serves `PUT /api/instance/{id}`.
func (s *Server) handleInstanceUpdate(w http.ResponseWriter, r *http.Request) {
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
		writeError(w, http.StatusForbidden, "Only the record owner can update this record.")
		return
	}

	var body struct {
		ID     string         `json:"id"`
		Fields map[string]any `json:"fields"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Renaming, version bumps, admin-sync/dev toggles and domains need a power-off.
	advanced := false
	for _, k := range []string{"subdomain", "version", "syncAdmin", "autoVacuum", "dev", "cname"} {
		if _, ok := body.Fields[k]; ok {
			advanced = true
		}
	}
	if advanced && (inst.Power || inst.Status != store.StatusIdle) {
		writeError(w, http.StatusBadRequest, "Instance must be powered off first.")
		return
	}

	if raw, ok := body.Fields["subdomain"]; ok {
		next := strings.ToLower(strings.TrimSpace(asString(raw)))
		if next != inst.Subdomain {
			if msg := instanceNameError(next); msg != "" {
				writeError(w, http.StatusBadRequest, msg)
				return
			}
			if _, err := s.repo.GetInstanceBySubdomain(r.Context(), next); err == nil {
				writeError(w, http.StatusBadRequest, "Instance name "+next+" is not available.")
				return
			}
		}
	}
	if raw, ok := body.Fields["secrets"]; ok && raw != nil {
		if m, isMap := raw.(map[string]any); isMap {
			for k := range m {
				if !secretKeyOK(k) {
					writeError(w, http.StatusBadRequest,
						"Invalid secret name "+k+": use A-Z, 0-9 and _, starting with a letter.")
					return
				}
			}
		}
	}

	if raw, ok := body.Fields["power"]; ok && asBool(raw) && !inst.Power {
		running, err := s.repo.CountRunningInstancesByUID(r.Context(), u.ID)
		if err == nil && u.SubscriptionQuantity > 0 && running >= u.SubscriptionQuantity {
			writeError(w, http.StatusBadRequest,
				"Your plan allows "+strconv.Itoa(u.SubscriptionQuantity)+" running instance(s). Upgrade or power off another instance.")
			return
		}
	}

	if err := s.repo.PatchInstanceFields(r.Context(), inst.ID, body.Fields); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if raw, ok := body.Fields["power"]; ok && s.svc != nil {
		if asBool(raw) {
			if fresh, err := s.repo.GetInstance(r.Context(), inst.ID); err == nil {
				go func() {
					_, _ = s.svc.EnsureRunning(context.Background(), fresh)
				}()
			}
		} else {
			_ = s.svc.Stop(r.Context(), inst.ID)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}
