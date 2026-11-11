package tlscert

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"

	"github.com/incurl/pocketghost/internal/config"
	"github.com/incurl/pocketghost/internal/logging"
)

func TestSelfSignedGenerationAndReuse(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{
		ApexDomain: "example.test",
		SSLHome:    filepath.Join(home, "ssl"),
		SSLKey:     filepath.Join(home, "ssl", "tls.key"),
		SSLCert:    filepath.Join(home, "ssl", "tls.cert"),
	}

	mgr, err := New(cfg, logging.Discard())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if !mgr.Enabled() {
		t.Fatal("expected TLS to be enabled")
	}
	conf := mgr.TLSConfig()
	if conf.MinVersion != tls.VersionTLS12 {
		t.Fatalf("min version = %x", conf.MinVersion)
	}
	if len(conf.Certificates) != 1 {
		t.Fatalf("certificates = %d", len(conf.Certificates))
	}
	leaf := conf.Certificates[0].Leaf
	if leaf == nil {
		parsed := conf.Certificates[0].Certificate[0]
		if len(parsed) == 0 {
			t.Fatal("empty certificate")
		}
	}

	// Cert and key must be on disk with restrictive permissions.
	for _, p := range []string{cfg.SSLCert, cfg.SSLKey} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("%s permissions = %v", p, info.Mode().Perm())
		}
	}

	// A second manager reuses the same files.
	mgr2, err := New(cfg, logging.Discard())
	if err != nil {
		t.Fatalf("second new: %v", err)
	}
	c1, _ := os.ReadFile(cfg.SSLCert)
	mgr3, err := New(cfg, logging.Discard())
	if err != nil {
		t.Fatal(err)
	}
	c2, _ := os.ReadFile(cfg.SSLCert)
	if string(c1) != string(c2) {
		t.Fatal("certificate was regenerated instead of reused")
	}
	_ = mgr2
	_ = mgr3
}

func TestWildcardMatching(t *testing.T) {
	if !matchesWildcard("demo.example.test", "example.test") {
		t.Fatal("expected subdomain match")
	}
	if matchesWildcard("example.test", "example.test") {
		t.Fatal("apex should not be treated as a subdomain")
	}
	if matchesWildcard("evil-example.test", "example.test") {
		t.Fatal("suffix must be dot-delimited")
	}
}
