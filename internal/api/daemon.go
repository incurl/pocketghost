package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/incurl/pocketghost/internal/logging"
	"github.com/incurl/pocketghost/internal/store"
)

func (s *Server) handleProcesses(w http.ResponseWriter, r *http.Request) {
	running := s.svc.Supervisor().Snapshot()
	persisted, err := s.repo.ListProcesses(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"running":   running,
		"persisted": persisted,
	})
}

func (s *Server) handleDaemonInstances(w http.ResponseWriter, r *http.Request) {
	instances, err := s.repo.ListInstances(r.Context(), "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type row struct {
		*store.Instance
		Running  bool   `json:"running"`
		PID      int    `json:"pid"`
		Port     int    `json:"port"`
		URL      string `json:"internal_url"`
		Hostname string `json:"hostname"`
	}
	out := make([]row, 0, len(instances))
	for _, inst := range instances {
		rw := row{Instance: inst, Hostname: s.cfg.InstanceHostname(inst.Subdomain)}
		if p, ok := s.svc.Supervisor().Get(inst.ID); ok {
			rw.Running = true
			rw.PID = p.PID()
			rw.Port = p.Port()
			rw.URL = p.InternalURL()
		}
		out = append(out, rw)
	}
	writeJSON(w, http.StatusOK, map[string]any{"instances": out})
}

func (s *Server) instanceOr404(w http.ResponseWriter, r *http.Request) (*store.Instance, bool) {
	inst, err := s.repo.GetInstance(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return nil, false
	}
	return inst, true
}

func (s *Server) handleInstanceStart(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceOr404(w, r)
	if !ok {
		return
	}
	rt, err := s.svc.EnsureRunning(r.Context(), inst)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "running", "pid": rt.PID, "port": rt.Port, "internalUrl": rt.InternalURL,
	})
}

func (s *Server) handleInstanceStop(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceOr404(w, r)
	if !ok {
		return
	}
	if err := s.svc.Stop(r.Context(), inst.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "idle"})
}

func (s *Server) handleInstanceRestart(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceOr404(w, r)
	if !ok {
		return
	}
	rt, err := s.svc.Restart(r.Context(), inst)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "running", "pid": rt.PID, "port": rt.Port, "internalUrl": rt.InternalURL,
	})
}

// handleInstanceLogs returns the last N parsed log entries.
func (s *Server) handleInstanceLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	target := r.URL.Query().Get("target")
	if target == "" {
		target = "exec"
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	if n <= 0 {
		n = 200
	}
	entries, err := s.logs.ReadTail(id, target, n)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "target": target})
}

// handleInstanceLogsStream streams log lines as server-sent events. The
// dashboard log tab consumes `data: {...}` frames.
func (s *Server) handleInstanceLogsStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	target := r.URL.Query().Get("target")
	if target == "" {
		target = "exec"
	}
	lines, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	if lines <= 0 {
		lines = 100
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	out := make(chan logging.Entry, 128)
	go func() {
		_ = s.logs.Follow(r.Context(), id, target, lines, out)
	}()

	enc := json.NewEncoder(w)
	for {
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-out:
			if !ok {
				return
			}
			_, _ = fmt.Fprint(w, "data: ")
			_ = enc.Encode(e)
			_, _ = fmt.Fprint(w, "\n")
			flusher.Flush()
		}
	}
}

func (s *Server) handleInstanceBackup(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		writeError(w, http.StatusNotImplemented, "backup service not configured")
		return
	}
	id := r.PathValue("id")
	name, err := s.backups.Create(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "name": name})
}

func (s *Server) handleInstanceBackupList(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		writeJSON(w, http.StatusOK, map[string]any{"backups": []string{}})
		return
	}
	names, err := s.backups.List(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"backups": names})
}

// handleBackupDownload serves `GET /_ph/backup/{id}/{zip}`.
func (s *Server) handleBackupDownload(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		writeError(w, http.StatusNotImplemented, "backup service not configured")
		return
	}
	id := r.PathValue("id")
	name := r.PathValue("zip")
	if strings.Contains(name, "..") || strings.Contains(name, "/") {
		writeError(w, http.StatusBadRequest, "invalid backup name")
		return
	}

	path, err := s.backups.Path(id, name)
	if err != nil {
		writeError(w, http.StatusNotFound, "backup not found")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(path)+`"`)
	http.ServeFile(w, r, path)
}

// ensureInstanceDirs creates the legacy on-disk layout for a new instance.
func ensureInstanceDirs(s *Server, instanceID string) error {
	dir := s.cfg.InstanceDataPath(instanceID)
	if err := logging.EnsureInstanceDirStructure(dir, s.log); err != nil {
		return err
	}
	for _, sub := range []string{"pb_hooks", "pb_migrations"} {
		p := filepath.Join(dir, sub, ".gitkeep")
		if _, err := os.Stat(p); err != nil {
			_ = os.WriteFile(p, nil, 0o644)
		}
	}
	return nil
}
