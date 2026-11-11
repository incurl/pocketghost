// Package ftp implements an FTP/FTPS server for tenant files. It replaces the
// `ftp-srv` dependency and keeps the same PASV range configuration.
package ftp

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/incurl/pocketghost/internal/logging"
	"github.com/incurl/pocketghost/internal/vfs"
)

// Auth authenticates an FTP login and returns the rooted filesystem for it.
type Auth interface {
	Login(user, pass string) (root string, ok bool)
}

// Config configures the FTP service.
type Config struct {
	Addr      string
	PasvIP    string
	PasvMin   int
	PasvMax   int
	TLSConfig *tls.Config // enables explicit FTPS (AUTH TLS) when non-nil
	Auth      Auth
	Logger    *logging.Logger
	// IdleTimeout closes idle control connections.
	IdleTimeout time.Duration
}

// Server is a running FTP service.
type Server struct {
	cfg    Config
	log    *logging.Logger
	ln     net.Listener
	mu     sync.Mutex
	closed bool
}

// Listen binds the FTP control listener.
func Listen(cfg Config) (*Server, error) {
	if cfg.Logger == nil {
		cfg.Logger = logging.Discard()
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 5 * time.Minute
	}
	if cfg.PasvMin == 0 {
		cfg.PasvMin = 10000
	}
	if cfg.PasvMax == 0 {
		cfg.PasvMax = 20000
	}
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return nil, fmt.Errorf("ftp: listen %s: %w", cfg.Addr, err)
	}
	return &Server{cfg: cfg, log: cfg.Logger.Create("ftp"), ln: ln}, nil
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

// Serve accepts connections.
func (s *Server) Serve() error {
	s.log.Infof("FTP listening on %s (PASV %d-%d)", s.ln.Addr(), s.cfg.PasvMin, s.cfg.PasvMax)
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed || errors.Is(err, net.ErrClosed) {
				return nil
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return err
		}
		go newSession(s, conn).run()
	}
}

// pasvListener opens a passive data listener inside the configured range.
func (s *Server) pasvListener() (net.Listener, int, error) {
	addr := s.cfg.PasvIP
	if addr == "" {
		addr = "127.0.0.1"
	}
	for attempt := 0; attempt < 64; attempt++ {
		port := s.cfg.PasvMin + int(time.Now().UnixNano()%int64(s.cfg.PasvMax-s.cfg.PasvMin+1))
		ln, err := net.Listen("tcp", net.JoinHostPort(addr, strconv.Itoa(port)))
		if err == nil {
			return ln, port, nil
		}
	}
	return nil, 0, errors.New("ftp: no free passive port")
}

// rootFS builds a jailed filesystem.
func rootFS(root string) (*vfs.FS, error) { return vfs.New(root) }
