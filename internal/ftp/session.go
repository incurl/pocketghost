package ftp

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"path"
	"strings"
	"time"

	"github.com/incurl/pocketghost/internal/vfs"
)

// session is one FTP control connection.
type session struct {
	srv    *Server
	conn   net.Conn
	r      *bufio.Reader
	w      *bufio.Writer
	fs     *vfs.FS
	cwd    string
	user   string
	authed bool
	tlsOK  bool
	protP  bool

	pasvLn  net.Listener
	rest    int64
	renFrom string
	quit    bool
}

func newSession(srv *Server, conn net.Conn) *session {
	return &session{
		srv:  srv,
		conn: conn,
		r:    bufio.NewReader(conn),
		w:    bufio.NewWriter(conn),
		cwd:  "/",
	}
}

func (s *session) reply(code int, msg string) error {
	if _, err := fmt.Fprintf(s.w, "%d %s\r\n", code, msg); err != nil {
		return err
	}
	return s.w.Flush()
}

func (s *session) replyMulti(code int, lines []string) error {
	for i, line := range lines {
		sep := "-"
		if i == len(lines)-1 {
			sep = " "
		}
		if _, err := fmt.Fprintf(s.w, "%d%s%s\r\n", code, sep, line); err != nil {
			return err
		}
	}
	return s.w.Flush()
}

func (s *session) run() {
	defer func() {
		s.closeData()
		_ = s.conn.Close()
	}()

	_ = s.conn.SetDeadline(time.Now().Add(s.srv.cfg.IdleTimeout))
	if err := s.reply(220, "PocketHost FTP ready"); err != nil {
		return
	}

	for !s.quit {
		line, err := s.r.ReadString('\n')
		if err != nil {
			return
		}
		_ = s.conn.SetDeadline(time.Now().Add(s.srv.cfg.IdleTimeout))
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}
		cmd, arg := splitCommand(line)
		if err := s.handle(cmd, arg); err != nil {
			s.srv.log.Debugf("ftp %s: %v", cmd, err)
			return
		}
	}
}

func splitCommand(line string) (string, string) {
	if i := strings.IndexByte(line, ' '); i >= 0 {
		return strings.ToUpper(line[:i]), strings.TrimSpace(line[i+1:])
	}
	return strings.ToUpper(line), ""
}

func (s *session) requireAuth() bool {
	if !s.authed {
		_ = s.reply(530, "Please login with USER and PASS.")
		return false
	}
	return true
}

func (s *session) resolve(p string) string {
	if p == "" {
		return s.cwd
	}
	if strings.HasPrefix(p, "/") {
		return path.Clean(p)
	}
	return path.Join(s.cwd, p)
}

func (s *session) closeData() {
	if s.pasvLn != nil {
		_ = s.pasvLn.Close()
		s.pasvLn = nil
	}
}

// openData accepts the passive data connection, wrapped in TLS when PROT P.
func (s *session) openData() (net.Conn, error) {
	if s.pasvLn == nil {
		return nil, errors.New("use PASV first")
	}
	type result struct {
		c   net.Conn
		err error
	}
	ch := make(chan result, 1)
	go func() {
		c, err := s.pasvLn.Accept()
		ch <- result{c, err}
	}()

	select {
	case res := <-ch:
		s.closeData()
		if res.err != nil {
			return nil, res.err
		}
		if s.protP && s.tlsOK {
			tlsConn := tls.Server(res.c, s.srv.cfg.TLSConfig)
			if err := tlsConn.Handshake(); err != nil {
				_ = res.c.Close()
				return nil, err
			}
			return tlsConn, nil
		}
		return res.c, nil
	case <-time.After(30 * time.Second):
		s.closeData()
		return nil, errors.New("data connection timed out")
	}
}
