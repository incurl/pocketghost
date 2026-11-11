package sftp

import (
	"errors"
	"io"
	"os"
	"path"
	"strconv"

	"github.com/incurl/pocketghost/internal/vfs"
)

// handle is an open file or directory listing.
type handle struct {
	file    *os.File
	entries []os.DirEntry
	vpath   string
	pos     int
}

func (s *session) addHandle(h *handle) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	id := "h" + strconv.Itoa(s.nextID)
	s.handles[id] = h
	return id
}

func (s *session) getHandle(id string) (*handle, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.handles[id]
	return h, ok
}

func (s *session) dropHandle(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h, ok := s.handles[id]; ok {
		if h.file != nil {
			_ = h.file.Close()
		}
		delete(s.handles, id)
	}
}

func (s *session) ok(rw io.Writer) error {
	return writePacket(rw, fxpStatus, statusPacket(statusOK, "OK").buf)
}

func (s *session) fail(rw io.Writer, err error) error {
	code := uint32(statusFailure)
	switch {
	case errors.Is(err, os.ErrNotExist):
		code = statusNoSuchFile
	case errors.Is(err, os.ErrPermission):
		code = statusPermission
	case errors.Is(err, vfs.ErrTraversal):
		code = statusPermission
	}
	return writePacket(rw, fxpStatus, statusPacket(code, err.Error()).buf)
}

func statusPacket(code uint32, msg string) *writer {
	w := &writer{}
	w.u32(code)
	w.str(msg)
	w.str("")
	return w
}

func encodeAttrsEmpty(w *writer) { w.u32(0) }

// normalize cleans a client path, rejecting traversal.
func normalize(p string) (string, error) {
	return vfs.Virtual(p)
}

// serve runs the SFTP protocol loop for one channel.
func (s *session) serve(rw io.ReadWriter) {
	s.handles = map[string]*handle{}
	initialised := false

	for {
		typ, payload, err := readPacket(rw)
		if err != nil {
			return
		}
		rd := newReader(payload)

		if !initialised {
			if typ != fxpInit {
				return
			}
			version, verr := rd.u32()
			if verr != nil || version < 3 {
				return
			}
			initialised = true
			w := &writer{}
			w.u32(3)
			if werr := writePacket(rw, fxpVersion, w.buf); werr != nil {
				return
			}
			continue
		}

		if err := s.dispatch(rw, typ, rd); err != nil {
			return
		}
	}
}

func (s *session) openFile(p string, flags uint32) (*os.File, error) {
	real, err := s.fs.Real(p)
	if err != nil {
		return nil, err
	}
	read := flags&fxfRead != 0
	write := flags&fxfWrite != 0
	create := flags&fxfCreat != 0
	trunc := flags&fxfTrunc != 0
	appendMode := flags&fxfAppend != 0

	switch {
	case read && !write:
		return os.Open(real)
	case create:
		perm := os.O_CREATE
		if write {
			perm |= os.O_WRONLY
		} else {
			perm |= os.O_RDWR
		}
		if trunc {
			perm |= os.O_TRUNC
		}
		if appendMode {
			perm |= os.O_APPEND
		}
		return os.OpenFile(real, perm, 0o644)
	case write && !read:
		return os.OpenFile(real, os.O_WRONLY, 0o644)
	default:
		return os.OpenFile(real, os.O_RDWR, 0o644)
	}
}

var _ = path.Base
