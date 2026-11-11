package api

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/incurl/pocketghost/internal/store"
)

var subdomainRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,30}[a-z0-9])$`)

// reservedSubdomains mirrors the mothership's instance policy denylist.
var reservedSubdomains = map[string]bool{
	"www": true, "app": true, "api": true, "admin": true, "mail": true,
	"mothership": true, "ftp": true, "sftp": true, "docs": true, "blog": true,
	"support": true, "status": true, "static": true, "assets": true, "cdn": true,
}

func instanceNameError(name string) string {
	if name == "" {
		return "Instance name is required."
	}
	if !subdomainRe.MatchString(name) {
		return "Instance names must be 3-32 characters, lowercase letters, numbers and dashes, and may not start or end with a dash."
	}
	if reservedSubdomains[name] {
		return "Instance name " + name + " is reserved."
	}
	return ""
}

// handleInstanceList serves `GET /api/collections/instances/records`.
func (s *Server) handleInstanceList(w http.ResponseWriter, r *http.Request) {
	u := s.userFromRequest(r)
	if u == nil {
		writeError(w, http.StatusUnauthorized, "The request requires valid record authorization token.")
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	perPage, _ := strconv.Atoi(r.URL.Query().Get("perPage"))
	if perPage <= 0 || perPage > 500 {
		perPage = 500
	}

	uid := u.ID
	// The dashboard passes `filter=(uid='<id>')`; superusers may ask for others.
	if f := r.URL.Query().Get("filter"); f != "" && s.isSuperuser(r) {
		if q := strings.Index(f, "'"); q >= 0 {
			rest := f[q+1:]
			if end := strings.Index(rest, "'"); end > 0 {
				uid = rest[:end]
			}
		}
	}

	all, err := s.repo.ListInstances(r.Context(), uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if r.URL.Query().Get("expand") == "uid" {
		for _, inst := range all {
			inst.Expand = map[string]any{"uid": u}
		}
	}

	start := (page - 1) * perPage
	if start < 0 {
		start = 0
	}
	end := start + perPage
	if end > len(all) {
		end = len(all)
	}
	items := []*store.Instance{}
	if start < len(all) {
		items = all[start:end]
	}

	writeJSON(w, http.StatusOK, newListResult(items, len(all), page, perPage))
}

// handleInstanceView serves `GET /api/collections/instances/records/{id}`.
func (s *Server) handleInstanceView(w http.ResponseWriter, r *http.Request) {
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
		writeError(w, http.StatusForbidden, "Only the record owner can view this record.")
		return
	}
	writeJSON(w, http.StatusOK, inst)
}
