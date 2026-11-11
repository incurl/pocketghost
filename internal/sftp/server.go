package sftp

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/incurl/pocketghost/internal/logging"
)

// Auth resolves a login into a rooted filesystem.
type Auth interface {
	// Password authenticates a user and returns their file root.
	Password(username, password string) (root string, ok bool)
	// PublicKey authenticates an SSH public key and returns the file root.
	PublicKey(username string, key ssh.PublicKey) (root string, ok bool)
}

// Config configures the SFTP service.
type Config struct {
	Addr        string
	HostKeyPath string
	Auth        Auth
	Logger      *logging.Logger
	// IdleTimeout bounds a session with no traffic.
	IdleTimeout time.Duration
}

// Server is a running SFTP service.
type Server struct {
	cfg    Config
	log    *logging.Logger
	sshCfg *ssh.ServerConfig
	ln     net.Listener
	mu     sync.Mutex
	closed bool
}

// hostKey loads or creates the SSH host key.
func hostKey(path string) (ssh.Signer, error) {
	if raw, err := os.ReadFile(path); err == nil {
		return ssh.ParsePrivateKey(raw)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}

	// Ed25519 is small, fast and accepted by every modern client.
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(pemBytes)
}

// Listen binds the SFTP listener and prepares authentication.
func Listen(cfg Config) (*Server, error) {
	if cfg.Logger == nil {
		cfg.Logger = logging.Discard()
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 5 * time.Minute
	}
	if cfg.HostKeyPath == "" {
		return nil, errors.New("sftp: host key path is required")
	}

	signer, err := hostKey(cfg.HostKeyPath)
	if err != nil {
		return nil, fmt.Errorf("sftp: host key: %w", err)
	}

	sshCfg := &ssh.ServerConfig{
		ServerVersion: "SSH-2.0-PocketHost",
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if cfg.Auth == nil {
				return nil, errors.New("sftp: password auth disabled")
			}
			root, ok := cfg.Auth.Password(c.User(), string(pass))
			if !ok {
				return nil, errors.New("sftp: invalid credentials")
			}
			return &ssh.Permissions{Extensions: map[string]string{"root": root}}, nil
		},
		PublicKeyCallback: func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if cfg.Auth == nil {
				return nil, errors.New("sftp: public key auth disabled")
			}
			root, ok := cfg.Auth.PublicKey(c.User(), key)
			if !ok {
				return nil, errors.New("sftp: unknown key")
			}
			return &ssh.Permissions{Extensions: map[string]string{"root": root}}, nil
		},
	}
	sshCfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return nil, fmt.Errorf("sftp: listen %s: %w", cfg.Addr, err)
	}
	return &Server{cfg: cfg, log: cfg.Logger.Create("sftp"), sshCfg: sshCfg, ln: ln}, nil
}

// Addr returns the bound address.
func (s *Server) Addr() net.Addr { return s.ln.Addr() }

// Close stops the listener.
func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return s.ln.Close()
}
