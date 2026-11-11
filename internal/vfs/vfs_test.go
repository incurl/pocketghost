package vfs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVirtualNormalisation(t *testing.T) {
	cases := map[string]string{
		"":         "/",
		"/":        "/",
		"a/b":      "/a/b",
		"/a/../b":  "/b",
		"a/./b/":   "/a/b",
		"//a//b//": "/a/b",
	}
	for in, want := range cases {
		got, err := Virtual(in)
		if err != nil {
			t.Fatalf("Virtual(%q) error: %v", in, err)
		}
		if got != want {
			t.Errorf("Virtual(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTraversalStaysInsideRoot(t *testing.T) {
	root := t.TempDir()
	fsys, err := New(filepath.Join(root, "jail"))
	if err != nil {
		t.Fatal(err)
	}
	// A chroot-like normalisation keeps `..` inside the jail; what matters is
	// that the resolved host path never escapes the root.
	for _, p := range []string{"/../../etc/passwd", "/../secret", "a/../../b"} {
		real, err := fsys.Real(p)
		if err != nil {
			continue // rejecting outright is also acceptable
		}
		if !filepath.HasPrefix(real, fsys.Root+string(filepath.Separator)) {
			t.Fatalf("Real(%q) = %q escaped root %q", p, real, fsys.Root)
		}
	}
}

func TestSymlinkEscapeRejected(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	jail := filepath.Join(root, "jail")
	fsys, err := New(jail)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(jail, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := fsys.Real("/escape"); err == nil {
		t.Fatal("expected symlink escape rejection")
	}
}

func TestFileOps(t *testing.T) {
	fsys, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := fsys.Mkdir("/pb_hooks", 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := fsys.Create("/pb_hooks/main.pb.js")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("console.log('hi')"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	info, err := fsys.Stat("/pb_hooks/main.pb.js")
	if err != nil || info.Size() == 0 {
		t.Fatalf("stat: %v size=%d", err, info.Size())
	}

	entries, err := fsys.ReadDir("/pb_hooks")
	if err != nil || len(entries) != 1 {
		t.Fatalf("readdir: %v entries=%d", err, len(entries))
	}

	if err := fsys.Rename("/pb_hooks/main.pb.js", "/pb_hooks/app.pb.js"); err != nil {
		t.Fatal(err)
	}
	if err := fsys.Remove("/pb_hooks/app.pb.js"); err != nil {
		t.Fatal(err)
	}
	if err := fsys.Rmdir("/pb_hooks"); err != nil {
		t.Fatal(err)
	}
}
