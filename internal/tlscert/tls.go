// Package tlscert manages edge certificates. It replaces `devcert` for local
// development (self-signed wildcard) and uses ACME/Let's Encrypt in production.
package tlscert

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"

	"github.com/incurl/pocketghost/internal/config"
	"github.com/incurl/pocketghost/internal/logging"
)

// Manager owns the TLS configuration for the edge listener.
type Manager struct {
	cfg   *config.Config
	log   *logging.Logger
	acme  *autocert.Manager
	local *tls.Certificate
}

// New builds a TLS manager. When ACME is disabled (development) a self-signed
// wildcard certificate is generated once and cached on disk.
func New(cfg *config.Config, logger *logging.Logger) (*Manager, error) {
	m := &Manager{cfg: cfg, log: logger.Create("tls")}

	if cfg.ACME {
		cacheDir := filepath.Join(cfg.SSLHome, "acme")
		if err := os.MkdirAll(cacheDir, 0o700); err != nil {
			return nil, err
		}
		m.acme = &autocert.Manager{
			Prompt: autocert.AcceptTOS,
			Cache:  autocert.DirCache(cacheDir),
			HostPolicy: func(_ context.Context, host string) error {
				if host == cfg.ApexDomain || matchesWildcard(host, cfg.ApexDomain) {
					return nil
				}
				return fmt.Errorf("tls: host %q is not served by this edge", host)
			},
			Email: cfg.TLSEmail,
		}
		m.log.Infof("ACME enabled for %s and *.%s", cfg.ApexDomain, cfg.ApexDomain)
		return m, nil
	}

	cert, err := ensureSelfSigned(cfg)
	if err != nil {
		return nil, err
	}
	m.local = cert
	m.log.Infof("using self-signed development certificate %s", cfg.SSLCert)
	return m, nil
}

// TLSConfig returns the listener configuration.
func (m *Manager) TLSConfig() *tls.Config {
	if m.acme != nil {
		return &tls.Config{
			MinVersion:     tls.VersionTLS12,
			GetCertificate: m.acme.GetCertificate,
			NextProtos:     []string{acme.ALPNProto, "h2", "http/1.1"},
		}
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{*m.local},
		NextProtos:   []string{"h2", "http/1.1"},
	}
}

// HTTPHandler wraps an HTTP handler with ACME HTTP-01 challenge support.
func (m *Manager) HTTPHandler(fallback http.Handler) http.Handler {
	if m.acme == nil {
		return fallback
	}
	return m.acme.HTTPHandler(fallback)
}

// Enabled reports whether TLS can be served.
func (m *Manager) Enabled() bool { return m.acme != nil || m.local != nil }

func matchesWildcard(host, apex string) bool {
	if len(host) <= len(apex)+1 {
		return false
	}
	return host[len(host)-len(apex)-1:] == "."+apex
}

// ensureSelfSigned loads the cached dev certificate or creates a new one that is
// valid for the apex domain and one level of subdomains.
func ensureSelfSigned(cfg *config.Config) (*tls.Certificate, error) {
	if cert, err := tls.LoadX509KeyPair(cfg.SSLCert, cfg.SSLKey); err == nil {
		return &cert, nil
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}

	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "*." + cfg.ApexDomain,
			Organization: []string{"PocketHost Development"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(1, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{cfg.ApexDomain, "*." + cfg.ApexDomain, "localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(cfg.SSLCert), 0o700); err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(cfg.SSLCert, certPEM, 0o600); err != nil {
		return nil, err
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(cfg.SSLKey, keyPEM, 0o600); err != nil {
		return nil, err
	}

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	return &cert, nil
}
