package ftp

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
)

func (s *session) handle(cmd, arg string) error {
	switch cmd {
	case "USER":
		s.user = arg
		return s.reply(331, "Password required")
	case "PASS":
		return s.doLogin(arg)
	case "AUTH":
		return s.doAuth(arg)
	case "PBSZ":
		return s.reply(200, "PBSZ=0")
	case "PROT":
		s.protP = strings.EqualFold(arg, "P")
		return s.reply(200, "Protection level set")
	case "FEAT":
		return s.replyMulti(211, []string{
			"Extensions supported:",
			" AUTH TLS",
			" PBSZ",
			" PROT",
			" EPSV",
			" PASV",
			" SIZE",
			" MDTM",
			" UTF8",
			"End",
		})
	case "SYST":
		return s.reply(215, "UNIX Type: L8")
	case "OPTS":
		return s.reply(200, "OK")
	case "NOOP":
		return s.reply(200, "OK")
	case "QUIT":
		s.quit = true
		return s.reply(221, "Goodbye")
	case "TYPE":
		return s.reply(200, "Type set")
	case "PWD", "XPWD":
		if !s.requireAuth() {
			return nil
		}
		return s.reply(257, `"`+s.cwd+`" is the current directory`)
	case "CWD":
		if !s.requireAuth() {
			return nil
		}
		return s.doCwd(arg)
	case "CDUP":
		if !s.requireAuth() {
			return nil
		}
		return s.doCwd("..")
	case "PASV":
		if !s.requireAuth() {
			return nil
		}
		return s.doPasv(false)
	case "EPSV":
		if !s.requireAuth() {
			return nil
		}
		return s.doPasv(true)
	case "PORT", "EPRT":
		return s.reply(502, "Active mode is disabled; use PASV.")
	case "LIST":
		if !s.requireAuth() {
			return nil
		}
		return s.doList(arg, false)
	case "NLST":
		if !s.requireAuth() {
			return nil
		}
		return s.doList(arg, true)
	case "RETR":
		if !s.requireAuth() {
			return nil
		}
		return s.doRetr(arg)
	case "STOR":
		if !s.requireAuth() {
			return nil
		}
		return s.doStor(arg, false)
	case "APPE":
		if !s.requireAuth() {
			return nil
		}
		return s.doStor(arg, true)
	case "DELE":
		if !s.requireAuth() {
			return nil
		}
		if err := s.fs.Remove(s.resolve(arg)); err != nil {
			return s.reply(550, err.Error())
		}
		return s.reply(250, "Deleted")
	case "MKD", "XMKD":
		if !s.requireAuth() {
			return nil
		}
		if err := s.fs.Mkdir(s.resolve(arg), 0o755); err != nil {
			return s.reply(550, err.Error())
		}
		return s.reply(257, `"`+s.resolve(arg)+`" created`)
	case "RMD", "XRMD":
		if !s.requireAuth() {
			return nil
		}
		if err := s.fs.Rmdir(s.resolve(arg)); err != nil {
			return s.reply(550, err.Error())
		}
		return s.reply(250, "Removed")
	case "RNFR":
		if !s.requireAuth() {
			return nil
		}
		if _, err := s.fs.Stat(s.resolve(arg)); err != nil {
			return s.reply(550, "File not found")
		}
		s.renFrom = s.resolve(arg)
		return s.reply(350, "Ready for RNTO")
	case "RNTO":
		if !s.requireAuth() {
			return nil
		}
		if s.renFrom == "" {
			return s.reply(503, "RNFR required first")
		}
		if err := s.fs.Rename(s.renFrom, s.resolve(arg)); err != nil {
			return s.reply(550, err.Error())
		}
		s.renFrom = ""
		return s.reply(250, "Renamed")
	case "SIZE":
		if !s.requireAuth() {
			return nil
		}
		info, err := s.fs.Stat(s.resolve(arg))
		if err != nil {
			return s.reply(550, "File not found")
		}
		return s.reply(213, fmt.Sprintf("%d", info.Size()))
	case "MDTM":
		if !s.requireAuth() {
			return nil
		}
		info, err := s.fs.Stat(s.resolve(arg))
		if err != nil {
			return s.reply(550, "File not found")
		}
		return s.reply(213, info.ModTime().UTC().Format("20060102150405"))
	case "REST":
		var n int64
		if _, err := fmt.Sscanf(arg, "%d", &n); err != nil {
			return s.reply(501, "Invalid offset")
		}
		s.rest = n
		return s.reply(350, "Restarting at offset")
	case "ALLO", "STRU", "MODE":
		return s.reply(200, "OK")
	default:
		return s.reply(502, "Command not implemented")
	}
}

func (s *session) doLogin(pass string) error {
	if s.srv.cfg.Auth == nil {
		return s.reply(530, "Login disabled")
	}
	if s.srv.cfg.TLSConfig != nil && !s.tlsOK {
		return s.reply(530, "TLS required: issue AUTH TLS first")
	}
	root, ok := s.srv.cfg.Auth.Login(s.user, pass)
	if !ok {
		s.srv.log.Infof("FTP login failed for %q", s.user)
		return s.reply(530, "Login incorrect")
	}
	fsys, err := rootFS(root)
	if err != nil {
		return s.reply(530, "Cannot open home directory")
	}
	s.fs = fsys
	s.authed = true
	s.srv.log.Infof("FTP login for %q", s.user)
	return s.reply(230, "Logged in")
}

func (s *session) doAuth(mech string) error {
	if s.srv.cfg.TLSConfig == nil {
		return s.reply(502, "TLS not configured")
	}
	if !strings.EqualFold(mech, "TLS") && !strings.EqualFold(mech, "SSL") {
		return s.reply(504, "Unsupported AUTH mechanism")
	}
	if err := s.reply(234, "AUTH TLS OK"); err != nil {
		return err
	}
	tlsConn := tls.Server(s.conn, s.srv.cfg.TLSConfig)
	if err := tlsConn.Handshake(); err != nil {
		return err
	}
	s.conn = tlsConn
	s.r = bufio.NewReader(tlsConn)
	s.w = bufio.NewWriter(tlsConn)
	s.tlsOK = true
	return nil
}

func (s *session) doCwd(dir string) error {
	target := s.resolve(dir)
	info, err := s.fs.Stat(target)
	if err != nil || !info.IsDir() {
		return s.reply(550, "No such directory")
	}
	s.cwd = target
	return s.reply(250, "Directory changed")
}

func (s *session) doPasv(extended bool) error {
	s.closeData()
	ln, port, err := s.srv.pasvListener()
	if err != nil {
		return s.reply(425, err.Error())
	}
	s.pasvLn = ln

	if extended {
		return s.reply(229, fmt.Sprintf("Entering Extended Passive Mode (|||%d|)", port))
	}
	ip := s.srv.cfg.PasvIP
	parsed := net.ParseIP(ip)
	if parsed == nil || parsed.To4() == nil {
		parsed = net.ParseIP("127.0.0.1")
	}
	v4 := parsed.To4()
	return s.reply(227, fmt.Sprintf("Entering Passive Mode (%d,%d,%d,%d,%d,%d)",
		v4[0], v4[1], v4[2], v4[3], port/256, port%256))
}
