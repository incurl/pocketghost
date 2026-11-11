package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsAndPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PH_HOME", home)
	t.Setenv("DATA_ROOT", "")
	t.Setenv("NODE_ENV", "development")
	t.Setenv("APEX_DOMAIN", "example.test")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ApexDomain != "example.test" {
		t.Fatalf("apex = %q", cfg.ApexDomain)
	}
	if !cfg.IsDev {
		t.Fatal("expected dev mode")
	}
	if cfg.Secret != "dev" {
		t.Fatalf("dev secret = %q", cfg.Secret)
	}
	if cfg.DataRoot != filepath.Join(home, "data") {
		t.Fatalf("data root = %q", cfg.DataRoot)
	}
	for _, dir := range []string{cfg.Home, cfg.DataRoot, cfg.PocketBaseRoot, cfg.SSLHome, cfg.BackupRoot} {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("expected %s to exist: %v", dir, err)
		}
	}
	if cfg.InstanceHostname("demo") != "demo.example.test" {
		t.Fatalf("hostname = %q", cfg.InstanceHostname("demo"))
	}
	if cfg.InstanceDataPath("abc", "pb_data") != filepath.Join(cfg.DataRoot, "instances", "abc", "pb_data") {
		t.Fatalf("instance data path = %q", cfg.InstanceDataPath("abc", "pb_data"))
	}
	if cfg.InstanceCanonicalHostname("abc", " custom.org ") != " custom.org " {
		t.Fatalf("canonical hostname = %q", cfg.InstanceCanonicalHostname("abc", " custom.org "))
	}
	if cfg.InstanceCanonicalHostname("abc", "") != "abc.example.test" {
		t.Fatal("fallback canonical hostname")
	}
}

func TestSubdomainFromHost(t *testing.T) {
	cfg := &Config{ApexDomain: "pockethost.io"}
	cases := []struct {
		host string
		sub  string
		ok   bool
	}{
		{"demo.pockethost.io", "demo", true},
		{"DEMO.pockethost.io:443", "demo", true},
		{"pockethost.io", "", false},
		{"a.b.pockethost.io", "", false},
		{"other.example.com", "", false},
	}
	for _, c := range cases {
		sub, ok := cfg.SubdomainFromHost(c.host)
		if sub != c.sub || ok != c.ok {
			t.Errorf("SubdomainFromHost(%q) = (%q,%v), want (%q,%v)", c.host, sub, ok, c.sub, c.ok)
		}
	}
}

func TestParseHelpers(t *testing.T) {
	t.Setenv("X_INT", "42")
	t.Setenv("X_BAD", "not-a-number")
	t.Setenv("X_BOOL", "yes")
	t.Setenv("X_CSV", " a, b ,, c ")

	if EnvInt("X_INT", 1) != 42 {
		t.Fatal("EnvInt")
	}
	if EnvInt("X_BAD", 7) != 7 {
		t.Fatal("EnvInt fallback")
	}
	if !EnvBool("X_BOOL", false) {
		t.Fatal("EnvBool")
	}
	if got := EnvCSV("X_CSV", nil); len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Fatalf("EnvCSV = %#v", got)
	}
}

func TestValidation(t *testing.T) {
	cfg := &Config{ApexDomain: "x", DaemonPort: 3000, PortPoolMin: 100, PortPoolMax: 200, InstanceMemoryMB: 64}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	bad := *cfg
	bad.PortPoolMax = 100
	if err := bad.Validate(); err == nil {
		t.Fatal("expected port range validation error")
	}
	bad = *cfg
	bad.DaemonPort = 70000
	if err := bad.Validate(); err == nil {
		t.Fatal("expected daemon port validation error")
	}
}
