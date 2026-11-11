package phome

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	env := "FOO=bar\nexport BAZ='qux'\n# comment\nEMPTY=\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	loadFile(filepath.Join(dir, ".env"))

	if got := os.Getenv("FOO"); got != "bar" {
		t.Fatalf("FOO = %q", got)
	}
	if got := os.Getenv("BAZ"); got != "qux" {
		t.Fatalf("BAZ = %q", got)
	}
}

func TestLoadFileDoesNotOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("PRESET=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PRESET", "from-env")
	loadFile(path)
	if got := os.Getenv("PRESET"); got != "from-env" {
		t.Fatalf("PRESET = %q", got)
	}
}

func TestUserDataDir(t *testing.T) {
	if UserDataDir() == "" {
		t.Fatal("expected a data dir")
	}
}
