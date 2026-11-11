package api

import (
	"net/http"
	"strings"

	"github.com/incurl/pocketghost/internal/store"
)

// handleVersions serves `GET /api/versions`, returning the selectable PocketBase
// versions (newest first) exactly like the mothership hook.
func (s *Server) handleVersions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"versions": s.bins.Versions(),
		"latest":   firstOrEmpty(s.bins.Versions()),
	})
}

func firstOrEmpty(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

// handleUserToken serves `GET /api/userToken/{id}` for superusers. The daemon
// uses it to seed ADMIN_SYNC on a tenant.
func (s *Server) handleUserToken(w http.ResponseWriter, r *http.Request) {
	u, err := s.repo.GetUser(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "User not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":           u.ID,
		"email":        u.Email,
		"tokenKey":     u.TokenKey,
		"passwordHash": u.PasswordHash,
	})
}

// handleStatsJSON serves the public `/stats.json` counters the splash page reads.
func (s *Server) handleStatsJSON(w http.ResponseWriter, r *http.Request) {
	instances, _ := s.repo.ListInstances(r.Context(), "")
	users, _ := s.repo.ListUsers(r.Context())

	running := 0
	byStatus := map[string]int{}
	for _, inst := range instances {
		byStatus[string(inst.Status)]++
		if s.svc != nil && s.svc.Supervisor().IsRunning(inst.ID) {
			running++
		}
	}
	verified, unverified := 0, 0
	for _, u := range users {
		if u.Verified {
			verified++
		} else {
			unverified++
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"instances":        len(instances),
		"instancesRunning": running,
		"statusCounts":     byStatus,
		"users":            len(users),
		"verifiedUsers":    verified,
		"unverifiedUsers":  unverified,
		"cachedVersions":   s.bins.Versions(),
		"updatedAt":        store.NowString(),
	})
}

// handleSignupCheck serves `GET /api/signup?instanceName=...`.
func (s *Server) handleSignupCheck(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("instanceName")))
	if name == "" {
		name = strings.ToLower(strings.TrimSpace(r.URL.Query().Get("subdomain")))
	}
	if name == "" {
		writeError(w, http.StatusBadRequest, "instanceName is required")
		return
	}
	if msg := instanceNameError(name); msg != "" {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "error": msg})
		return
	}
	_, err := s.repo.GetInstanceBySubdomain(r.Context(), name)
	writeJSON(w, http.StatusOK, map[string]any{"available": err == store.ErrNotFound, "subdomain": name})
}

// handleSignupConfirm serves `POST /api/signup` and creates the user and their
// first instance in one step (the "instance generator" flow).
func (s *Server) handleSignupConfirm(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email        string `json:"email"`
		Password     string `json:"password"`
		InstanceName string `json:"instanceName"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	name := strings.ToLower(strings.TrimSpace(body.InstanceName))
	if msg := instanceNameError(name); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if _, err := s.repo.GetInstanceBySubdomain(r.Context(), name); err == nil {
		writeError(w, http.StatusBadRequest, "Instance name "+name+" is not available.")
		return
	}

	// Reuse the account when the email already exists (signup is idempotent).
	u, err := s.repo.GetUserByEmail(r.Context(), body.Email)
	if err == store.ErrNotFound {
		hash, herr := hashPassword(body.Password)
		if herr != nil {
			writeError(w, http.StatusInternalServerError, "Password hashing failed")
			return
		}
		u = &store.User{
			Email:                body.Email,
			Username:             "users" + store.NewID()[:6],
			TokenKey:             randomTokenKey(),
			PasswordHash:         hash,
			Subscription:         store.SubscriptionFree,
			SubscriptionQuantity: 1,
			EmailVisibility:      true,
		}
		if cerr := s.repo.CreateUser(r.Context(), u); cerr != nil {
			writeError(w, http.StatusInternalServerError, cerr.Error())
			return
		}
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	version := firstOrEmpty(s.bins.Versions())
	if version == "" {
		version = s.cfg.MothershipSemver
	}
	inst := &store.Instance{
		Subdomain:  name,
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
	_ = ensureInstanceDirs(s, inst.ID)
	_ = s.repo.Audit(r.Context(), u.ID, "signup", name)

	writeJSON(w, http.StatusOK, map[string]any{"user": u, "instance": inst})
}
