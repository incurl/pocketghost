package sftp

import (
	"io"
)

// dispatch handles one SFTP request.
func (s *session) dispatch(rw io.ReadWriter, typ byte, rd *reader) error {
	switch typ {
	case fxpRealpath:
		return s.handleRealpath(rw, rd)
	case fxpStat, fxpLstat:
		return s.handleStat(rw, rd)
	case fxpOpen:
		return s.handleOpen(rw, rd)
	case fxpOpendir:
		return s.handleOpenDir(rw, rd)
	case fxpReaddir:
		return s.handleReadDir(rw, rd)
	case fxpRead:
		return s.handleRead(rw, rd)
	case fxpWrite:
		return s.handleWrite(rw, rd)
	case fxpFstat:
		return s.handleFstat(rw, rd)
	case fxpSetstat, fxpFsetstat:
		return s.handleSetstat(rw, rd)
	case fxpRemove:
		return s.handleRemove(rw, rd)
	case fxpMkdir:
		return s.handleMkdir(rw, rd)
	case fxpRmdir:
		return s.handleRmdir(rw, rd)
	case fxpRename:
		return s.handleRename(rw, rd)
	case fxpClose:
		id, err := rd.str()
		if err != nil {
			return err
		}
		s.dropHandle(id)
		return s.ok(rw)
	case fxpSymlink, fxpReadlink:
		return writePacket(rw, fxpStatus, statusPacket(statusOpUnsupported, "unsupported").buf)
	default:
		return writePacket(rw, fxpStatus, statusPacket(statusOpUnsupported, "unsupported operation").buf)
	}
}

func (s *session) handleRealpath(rw io.Writer, rd *reader) error {
	p, err := rd.str()
	if err != nil {
		return err
	}
	v, verr := normalize(p)
	if verr != nil {
		return s.fail(rw, verr)
	}
	w := &writer{}
	w.u32(1)
	w.str(v)
	w.str(v)
	encodeAttrsEmpty(w)
	return writePacket(rw, fxpName, w.buf)
}

func (s *session) handleStat(rw io.Writer, rd *reader) error {
	p, err := rd.str()
	if err != nil {
		return err
	}
	info, serr := s.fs.Stat(p)
	if serr != nil {
		return s.fail(rw, serr)
	}
	w := &writer{}
	encodeAttrs(w, attrs{
		Size:  uint64(info.Size()),
		Perm:  uint32(info.Mode().Perm()),
		Atime: uint32(info.ModTime().Unix()),
		Mtime: uint32(info.ModTime().Unix()),
	})
	return writePacket(rw, fxpAttrs, w.buf)
}

func (s *session) handleOpen(rw io.Writer, rd *reader) error {
	p, err := rd.str()
	if err != nil {
		return err
	}
	flags32, err := rd.u32()
	if err != nil {
		return err
	}
	_, _ = decodeAttrs(rd)

	f, ferr := s.openFile(p, flags32)
	if ferr != nil {
		return s.fail(rw, ferr)
	}
	id := s.addHandle(&handle{file: f, vpath: p})
	w := &writer{}
	w.str(id)
	return writePacket(rw, fxpHandle, w.buf)
}

func (s *session) handleOpenDir(rw io.Writer, rd *reader) error {
	p, err := rd.str()
	if err != nil {
		return err
	}
	entries, derr := s.fs.ReadDir(p)
	if derr != nil {
		return s.fail(rw, derr)
	}
	id := s.addHandle(&handle{entries: entries, vpath: p})
	w := &writer{}
	w.str(id)
	return writePacket(rw, fxpHandle, w.buf)
}
