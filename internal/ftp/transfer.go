package ftp

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

func (s *session) doList(arg string, namesOnly bool) error {
	target := s.resolve(strings.TrimSpace(arg))
	entries, err := s.fs.ReadDir(target)
	if err != nil {
		return s.reply(550, "Cannot list directory")
	}

	conn, err := s.openData()
	if err != nil {
		return s.reply(425, "Use PASV first")
	}
	defer conn.Close()

	_ = s.reply(150, "Opening data connection")
	bw := bufio.NewWriter(conn)
	for _, e := range entries {
		if namesOnly {
			fmt.Fprintf(bw, "%s\r\n", e.Name())
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		fmt.Fprintf(bw, "%s 1 owner group %12d %s %s\r\n",
			info.Mode().String(), info.Size(),
			info.ModTime().Format("Jan _2 15:04"), e.Name())
	}
	if err := bw.Flush(); err != nil {
		return s.reply(426, "Transfer aborted")
	}
	return s.reply(226, "Transfer complete")
}

func (s *session) doRetr(name string) error {
	f, err := s.fs.Open(s.resolve(name))
	if err != nil {
		return s.reply(550, "File not found")
	}
	defer f.Close()

	conn, err := s.openData()
	if err != nil {
		return s.reply(425, "Use PASV first")
	}
	defer conn.Close()

	if s.rest > 0 {
		if _, err := f.Seek(s.rest, io.SeekStart); err != nil {
			return s.reply(550, "Cannot seek")
		}
		s.rest = 0
	}

	_ = s.reply(150, "Opening binary mode data connection")
	if _, err := io.Copy(conn, f); err != nil {
		return s.reply(426, "Transfer aborted")
	}
	return s.reply(226, "Transfer complete")
}

func (s *session) doStor(name string, appendMode bool) error {
	target := s.resolve(name)
	real, err := s.fs.Real(target)
	if err != nil {
		return s.reply(550, err.Error())
	}
	flags := os.O_CREATE | os.O_WRONLY
	if appendMode || s.rest > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(real, flags, 0o644)
	if err != nil {
		return s.reply(550, err.Error())
	}
	defer f.Close()

	conn, err := s.openData()
	if err != nil {
		return s.reply(425, "Use PASV first")
	}
	defer conn.Close()

	_ = s.reply(150, "Opening binary mode data connection")
	if _, err := io.Copy(f, conn); err != nil {
		return s.reply(426, "Transfer aborted")
	}
	return s.reply(226, "Transfer complete")
}
