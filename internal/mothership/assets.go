// Package mothership boots the PocketHost control plane inside the pure-Go
// PocketBase SDK, running the *original* mothership application hooks.
//
// The upstream deployment ran the mothership as a PocketBase process with
// JavaScript hooks (packages/server/src/mothership-app/pb_hooks). Those hooks
// only use PocketBase's own $app/$os/$apis globals — they never touch Node
// built-ins — so they execute unchanged inside PocketBase's goja-based JSVM.
//
// That means the whole 32-route control plane (instance CRUD, auth, billing
// hooks, Cloudflare sweeps, notifications, realtime) keeps working without
// Node.js, TypeScript or npm, and without Docker.
package mothership

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Assets is the embedded copy of the original mothership application:
// pb_hooks (the compiled JS bundle), pb_migrations and pb_admin_ext.
//
//go:embed all:assets
var Assets embed.FS

// AssetDirs are the sub-directories PocketBase expects next to its data dir.
var AssetDirs = []string{"pb_hooks", "pb_migrations", "pb_admin_ext"}

// Materialize writes the embedded assets to disk and returns the root
// directory that contains them.
//
// PocketBase's JSVM and migration runner both read hooks and migrations from
// the filesystem, so the embedded copies are written out once at boot (and
// refreshed when their content changed) before the app is started.
func Materialize(root string) (string, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("mothership: create %s: %w", root, err)
	}
	for _, dir := range AssetDirs {
		if err := copyDir(filepath.Join(root, dir), filepath.Join("assets", dir)); err != nil {
			return "", err
		}
	}
	return root, nil
}

// copyDir copies an embedded directory to disk, skipping files whose content
// already matches (so restarts do not rewrite files needlessly).
func copyDir(dst, src string) error {
	entries, err := fs.ReadDir(Assets, src)
	if err != nil {
		return fmt.Errorf("mothership: read embedded %s: %w", src, err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("mothership: create %s: %w", dst, err)
	}
	for _, e := range entries {
		srcPath := filepath.Join(src, e.Name())
		dstPath := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyDir(dstPath, srcPath); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(dstPath, srcPath); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(dst, src string) error {
	data, err := Assets.ReadFile(src)
	if err != nil {
		return fmt.Errorf("mothership: read embedded %s: %w", src, err)
	}
	if existing, err := os.ReadFile(dst); err == nil && string(existing) == string(data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
