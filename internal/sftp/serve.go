package sftp

import (
	"context"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/incurl/pocketghost/internal/logging"
	"github.com/incurl/pocketghost/internal/vfs"
)

// Serve accepts connections until Close is called.
func (s *Server) Serve(ctx context.Context) error {
	s.log.Infof("SFTP listening on %s", s.ln.Addr())
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return nil
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return err
		}
		go s.handleConn(ctx, conn)
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(s.cfg.IdleTimeout))
	sshConn, chans, reqs, err := ssh.NewServerConn(conn, s.sshCfg)
	if err != nil {
		s.log.Debugf("handshake failed from %s: %v", conn.RemoteAddr(), err)
		return
	}
	defer sshConn.Close()

	root := sshConn.Permissions.Extensions["root"]
	user := sshConn.User()
	s.log.Infof("SFTP session for %s from %s", user, conn.RemoteAddr())

	go ssh.DiscardRequests(reqs)

	fsys, err := vfs.New(root)
	if err != nil {
		s.log.Warnf("SFTP root %q unusable: %v", root, err)
		return
	}
	sess := &session{fs: fsys, log: s.log, user: user, handles: map[string]*handle{}}

	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			_ = newChan.Reject(ssh.UnknownChannelType, "only session channels are supported")
			continue
		}
		ch, requests, err := newChan.Accept()
		if err != nil {
			return
		}
		go s.handleSessionChannel(ch, requests, sess)
	}
}

func (s *Server) handleSessionChannel(ch ssh.Channel, requests <-chan *ssh.Request, sess *session) {
	defer ch.Close()
	for req := range requests {
		switch req.Type {
		case "subsystem":
			if parseSubsystem(req.Payload) != "sftp" {
				_ = req.Reply(false, nil)
				continue
			}
			_ = req.Reply(true, nil)
			sess.serve(ch)
			return
		case "env", "window-change":
			_ = req.Reply(true, nil)
		default:
			_ = req.Reply(false, nil)
		}
	}
}

func parseSubsystem(payload []byte) string {
	if len(payload) < 4 {
		return ""
	}
	n := int(payload[0])<<24 | int(payload[1])<<16 | int(payload[2])<<8 | int(payload[3])
	if len(payload) < 4+n {
		return ""
	}
	return string(payload[4 : 4+n])
}

// session is one open SFTP protocol conversation.
type session struct {
	fs   *vfs.FS
	log  *logging.Logger
	user string

	mu      sync.Mutex
	handles map[string]*handle
	nextID  int
}
