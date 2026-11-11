// Package backup implements scheduled and on-demand instance backups. It is the
// native replacement for the backup webhook that used to run inside the tenant
// PocketBase process, and keeps the same zip naming scheme.
package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/incurl/pocketghost/internal/config"
	"github.com/incurl/pocketghost/internal/logging"
)

// Pauser stops an instance for a consistent snapshot and resumes it afterwards.
type Pauser interface {
	WithInstanceStopped(ctx context.Context, id string, fn func() error) (bool, error)
}

// Manager creates, lists and restores instance backups.
type Manager struct {
	cfg    *config.Config
	log    *logging.Logger
	retain int
	pauser Pauser
}

// New creates a backup manager.
func New(cfg *config.Config, logger *logging.Logger, pauser Pauser) *Manager {
	retain := cfg.BackupRetainCount
	if retain <= 0 {
		retain = 7
	}
	return &Manager{cfg: cfg, log: logger.Create("backup"), retain: retain, pauser: pauser}
}

func (m *Manager) dir(instanceID string) string { return m.cfg.BackupPath(instanceID) }

// Path returns the on-disk path for a backup, or an error when it is missing.
func (m *Manager) Path(instanceID, name string) (string, error) {
	if !strings.HasSuffix(name, ".zip") || strings.Contains(name, "/") || strings.Contains(name, "..") {
		return "", fmt.Errorf("backup: invalid name %q", name)
	}
	p := filepath.Join(m.dir(instanceID), name)
	if _, err := os.Stat(p); err != nil {
		return "", err
	}
	return p, nil
}

// Read returns the raw bytes of a backup.
func (m *Manager) Read(instanceID, name string) ([]byte, error) {
	p, err := m.Path(instanceID, name)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

// List returns backup names, newest first.
func (m *Manager) List(instanceID string) ([]string, error) {
	entries, err := os.ReadDir(m.dir(instanceID))
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	type item struct {
		name string
		mod  time.Time
	}
	var items []item
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".zip") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, item{e.Name(), info.ModTime()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mod.After(items[j].mod) })

	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.name
	}
	return out, nil
}

// Create writes a new backup for an instance, stopping it for consistency.
func (m *Manager) Create(ctx context.Context, instanceID string) (string, error) {
	name := fmt.Sprintf("@auto_pb_backup_ph_%s.zip", time.Now().UTC().Format("20060102150405"))
	if err := os.MkdirAll(m.dir(instanceID), 0o755); err != nil {
		return "", err
	}
	outPath := filepath.Join(m.dir(instanceID), name)

	write := func() error { return zipDir(m.cfg.InstanceDataPath(instanceID), outPath) }

	if m.pauser != nil {
		if _, err := m.pauser.WithInstanceStopped(ctx, instanceID, write); err != nil {
			_ = os.Remove(outPath)
			return "", err
		}
	} else if err := write(); err != nil {
		_ = os.Remove(outPath)
		return "", err
	}

	m.log.Infof("backup created for %s: %s", instanceID, name)
	m.prune(instanceID)
	return name, nil
}

// Restore replaces an instance's working directory with a backup.
func (m *Manager) Restore(ctx context.Context, instanceID, name string) error {
	src, err := m.Path(instanceID, name)
	if err != nil {
		return err
	}
	dest := m.cfg.InstanceDataPath(instanceID)

	restore := func() error {
		entries, err := os.ReadDir(dest)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		for _, e := range entries {
			if e.Name() == "logs" {
				continue // keep operator logs across a restore
			}
			if err := os.RemoveAll(filepath.Join(dest, e.Name())); err != nil {
				return err
			}
		}
		return unzipTo(src, dest)
	}

	if m.pauser != nil {
		if _, err := m.pauser.WithInstanceStopped(ctx, instanceID, restore); err != nil {
			return err
		}
	} else if err := restore(); err != nil {
		return err
	}
	m.log.Infof("restored %s from %s", instanceID, name)
	return nil
}

// prune enforces the retention policy (newest `retain` zips are kept).
func (m *Manager) prune(instanceID string) {
	names, err := m.List(instanceID)
	if err != nil || len(names) <= m.retain {
		return
	}
	for _, old := range names[m.retain:] {
		p := filepath.Join(m.dir(instanceID), old)
		if err := os.Remove(p); err != nil {
			m.log.Warnf("could not prune %s: %v", p, err)
			continue
		}
		m.log.Debugf("pruned old backup %s", p)
	}
}
