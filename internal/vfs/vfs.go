// Package vfs confines file operations to a per-user root and maps virtual
// paths to the on-disk instance layout. It is shared by the FTP and SFTP
// services so both expose exactly the same view of a tenant's files.
package vfs

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ErrTraversal is returned when a virtual path escapes the root.
var ErrTraversal = errors.New("vfs: path escapes root")

// FS is a rooted, virtual filesystem.
type FS struct {
	Root string // absolute host directory
}

// New creates a rooted filesystem.
func New(root string) (*FS, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, err
	}
	return &FS{Root: abs}, nil
}

// Virtual normalises a client-supplied path into a clean virtual path
// (`/`-prefixed, no `..` segments).
func Virtual(p string) (string, error) {
	if p == "" {
		return "/", nil
	}
	p = strings.ReplaceAll(p, "\\", "/")
	clean := path.Clean("/" + p)
	if clean == "/.." || strings.HasPrefix(clean, "/../") {
		return "", ErrTraversal
	}
	return clean, nil
}

// Real maps a virtual path to a host path guaranteed to live under Root.
func (f *FS) Real(virtual string) (string, error) {
	v, err := Virtual(virtual)
	if err != nil {
		return "", err
	}
	rel := strings.TrimPrefix(v, "/")
	real := filepath.Join(f.Root, filepath.FromSlash(rel))

	// Resolve symlinks on the deepest existing ancestor to defeat link escapes.
	check := real
	for {
		if resolved, err := filepath.EvalSymlinks(check); err == nil {
			if resolved != f.Root && !strings.HasPrefix(resolved, f.Root+string(filepath.Separator)) {
				return "", ErrTraversal
			}
			break
		}
		parent := filepath.Dir(check)
		if parent == check {
			break
		}
		check = parent
	}

	if real != f.Root && !strings.HasPrefix(real, f.Root+string(filepath.Separator)) {
		return "", ErrTraversal
	}
	return real, nil
}

// Open opens a file read-only.
func (f *FS) Open(virtual string) (*os.File, error) {
	real, err := f.Real(virtual)
	if err != nil {
		return nil, err
	}
	return os.Open(real)
}

// Create creates or truncates a file for writing.
func (f *FS) Create(virtual string) (*os.File, error) {
	real, err := f.Real(virtual)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(real, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
}

// OpenWrite opens a file for random-access writing (SFTP WRITE).
func (f *FS) OpenWrite(virtual string, create bool) (*os.File, error) {
	real, err := f.Real(virtual)
	if err != nil {
		return nil, err
	}
	if create {
		if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
			return nil, err
		}
		return os.OpenFile(real, os.O_CREATE|os.O_WRONLY, 0o644)
	}
	return os.OpenFile(real, os.O_WRONLY, 0o644)
}

// Stat returns file info.
func (f *FS) Stat(virtual string) (os.FileInfo, error) {
	real, err := f.Real(virtual)
	if err != nil {
		return nil, err
	}
	return os.Stat(real)
}

// Lstat returns file info without following symlinks.
func (f *FS) Lstat(virtual string) (os.FileInfo, error) {
	real, err := f.Real(virtual)
	if err != nil {
		return nil, err
	}
	return os.Lstat(real)
}

// ReadDir lists a directory.
func (f *FS) ReadDir(virtual string) ([]os.DirEntry, error) {
	real, err := f.Real(virtual)
	if err != nil {
		return nil, err
	}
	return os.ReadDir(real)
}

// Mkdir creates a directory.
func (f *FS) Mkdir(virtual string, perm os.FileMode) error {
	real, err := f.Real(virtual)
	if err != nil {
		return err
	}
	if perm == 0 {
		perm = 0o755
	}
	return os.MkdirAll(real, perm)
}

// Remove deletes a file.
func (f *FS) Remove(virtual string) error {
	real, err := f.Real(virtual)
	if err != nil {
		return err
	}
	return os.Remove(real)
}

// Rmdir deletes an empty directory.
func (f *FS) Rmdir(virtual string) error {
	real, err := f.Real(virtual)
	if err != nil {
		return err
	}
	return os.Remove(real)
}

// Rename moves a file.
func (f *FS) Rename(from, to string) error {
	realFrom, err := f.Real(from)
	if err != nil {
		return err
	}
	realTo, err := f.Real(to)
	if err != nil {
		return err
	}
	return os.Rename(realFrom, realTo)
}

// Chmod changes file permissions.
func (f *FS) Chmod(virtual string, mode os.FileMode) error {
	real, err := f.Real(virtual)
	if err != nil {
		return err
	}
	return os.Chmod(real, mode)
}

// Truncate resizes a file.
func (f *FS) Truncate(virtual string, size int64) error {
	real, err := f.Real(virtual)
	if err != nil {
		return err
	}
	return os.Truncate(real, size)
}
