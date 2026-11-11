package api

import (
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

// handleDashboard serves the embedded Svelte build with an SPA fallback to
// 404.html (the adapter-static fallback the dashboard was built with).
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if s.dash == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"service": "pocketghost",
			"version": Version,
			"note":    "dashboard assets were not embedded into this build",
		})
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" || name == "." {
		name = "index.html"
	}

	if st, err := fs.Stat(s.dash, name); err == nil && !st.IsDir() {
		s.serveDashboardFile(w, r, name, st)
		return
	}

	// SPA fallback: serve 404.html so client-side routing works.
	if st, err := fs.Stat(s.dash, "404.html"); err == nil {
		s.serveDashboardFile(w, r, "404.html", st)
		return
	}

	writeError(w, http.StatusNotFound, "Not found")
}

func (s *Server) serveDashboardFile(w http.ResponseWriter, r *http.Request, name string, st fs.FileInfo) {
	f, err := s.dash.Open(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not open asset")
		return
	}
	defer f.Close()

	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	switch {
	case name == "index.html" || name == "404.html":
		w.Header().Set("Cache-Control", "no-cache")
	case strings.HasPrefix(name, "assets/"), strings.HasPrefix(name, "_app/"), strings.HasPrefix(name, "immutable/"):
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	default:
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}

	if rs, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, name, st.ModTime(), rs)
		return
	}
	data, err := io.ReadAll(f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read asset")
		return
	}
	_, _ = w.Write(data)
}
