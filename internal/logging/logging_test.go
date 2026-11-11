package logging

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppendAndReadTail(t *testing.T) {
	store := NewLogStore(t.TempDir(), 1<<20, Discard())
	w := store.Writer("inst1", "exec")
	w.Info("hello")
	w.Error("boom")

	entries, err := store.ReadTail("inst1", "exec", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d", len(entries))
	}
	if entries[0].Message != "hello" || entries[0].Stream != "stdout" {
		t.Fatalf("first entry = %+v", entries[0])
	}
	if entries[1].Stream != "stderr" {
		t.Fatalf("second entry stream = %q", entries[1].Stream)
	}
	if _, err := time.Parse(time.RFC3339Nano, entries[0].Time); err != nil {
		t.Fatalf("timestamp not RFC3339: %v", err)
	}
}

func TestMissingLogIsNotAnError(t *testing.T) {
	store := NewLogStore(t.TempDir(), 1<<20, Discard())
	entries, err := store.ReadTail("nope", "exec", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no entries, got %d", len(entries))
	}
}

func TestRotation(t *testing.T) {
	dir := t.TempDir()
	store := NewLogStore(dir, 200, Discard())
	w := store.Writer("rot", "exec")
	for i := 0; i < 50; i++ {
		w.Info("this is a fairly long log line to force rotation")
	}
	if _, err := os.Stat(store.Path("rot", "exec") + ".1"); err != nil {
		t.Fatalf("expected rotated file: %v", err)
	}
	entries, err := store.ReadTail("rot", "exec", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("rotated log is empty")
	}
}

func TestFollowStreamsLiveEntries(t *testing.T) {
	store := NewLogStore(t.TempDir(), 1<<20, Discard())
	w := store.Writer("live", "exec")
	w.Info("before")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out := make(chan Entry, 16)
	go func() { _ = store.Follow(ctx, "live", "exec", 10, out) }()

	// Replayed history first.
	select {
	case e := <-out:
		if e.Message != "before" {
			t.Fatalf("replay = %+v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for replay")
	}

	w.Info("after")
	select {
	case e := <-out:
		if e.Message != "after" {
			t.Fatalf("live = %+v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for live entry")
	}
}

func TestEnsureInstanceDirStructure(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureInstanceDirStructure(dir, Discard()); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"pb_data", "pb_migrations", "pb_public", "logs", "pb_hooks"} {
		info, err := os.Stat(filepath.Join(dir, sub))
		if err != nil || !info.IsDir() {
			t.Fatalf("%s: %v", sub, err)
		}
	}
}

func TestLoggerLevels(t *testing.T) {
	var buf writeBuffer
	l := New(Options{Writer: &buf, Level: LevelWarn})
	l.Debug("hidden")
	l.Info("hidden")
	l.Warn("shown")
	out := buf.String()
	if out == "" || !contains(out, "shown") || contains(out, "hidden") {
		t.Fatalf("level filtering failed: %q", out)
	}
}

func TestBreadcrumbs(t *testing.T) {
	var buf writeBuffer
	l := New(Options{Writer: &buf, Level: LevelInfo, Namespace: "root"})
	l.Create("svc").Breadcrumb("inst1", "0.22.0").Info("hi")
	if !contains(buf.String(), "root/svc inst1 0.22.0") {
		t.Fatalf("breadcrumb missing: %q", buf.String())
	}
}

type writeBuffer struct{ b []byte }

func (w *writeBuffer) Write(p []byte) (int, error) {
	w.b = append(w.b, p...)
	return len(p), nil
}
func (w *writeBuffer) String() string { return string(w.b) }

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
