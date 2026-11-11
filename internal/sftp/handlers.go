package sftp

import (
	"errors"
	"io"
	"os"
	"path"
)

func (s *session) handleReadDir(rw io.Writer, rd *reader) error {
	id, err := rd.str()
	if err != nil {
		return err
	}
	h, ok := s.getHandle(id)
	if !ok || h.entries == nil {
		return s.fail(rw, errors.New("invalid directory handle"))
	}
	if h.pos >= len(h.entries) {
		return writePacket(rw, fxpStatus, statusPacket(statusEOF, "EOF").buf)
	}
	end := h.pos + 64
	if end > len(h.entries) {
		end = len(h.entries)
	}
	batch := h.entries[h.pos:end]
	h.pos = end

	w := &writer{}
	w.u32(uint32(len(batch)))
	for _, e := range batch {
		name := e.Name()
		full := path.Join(h.vpath, name)
		w.str(name)
		w.str(longName(full, e))
		info, ierr := e.Info()
		if ierr != nil {
			encodeAttrsEmpty(w)
			continue
		}
		encodeAttrs(w, attrs{
			Size:  uint64(info.Size()),
			Perm:  uint32(info.Mode().Perm()),
			Atime: uint32(info.ModTime().Unix()),
			Mtime: uint32(info.ModTime().Unix()),
		})
	}
	return writePacket(rw, fxpName, w.buf)
}

func (s *session) handleRead(rw io.Writer, rd *reader) error {
	id, err := rd.str()
	if err != nil {
		return err
	}
	off, err := rd.u64()
	if err != nil {
		return err
	}
	length, err := rd.u32()
	if err != nil {
		return err
	}
	h, ok := s.getHandle(id)
	if !ok || h.file == nil {
		return s.fail(rw, errors.New("invalid file handle"))
	}
	if length > 1<<16 {
		length = 1 << 16
	}
	buf := make([]byte, length)
	n, rerr := h.file.ReadAt(buf, int64(off))
	if n == 0 && rerr != nil {
		return writePacket(rw, fxpStatus, statusPacket(statusEOF, "EOF").buf)
	}
	w := &writer{}
	w.bytes(buf[:n])
	return writePacket(rw, fxpData, w.buf)
}

func (s *session) handleWrite(rw io.Writer, rd *reader) error {
	id, err := rd.str()
	if err != nil {
		return err
	}
	off, err := rd.u64()
	if err != nil {
		return err
	}
	data, err := rd.str()
	if err != nil {
		return err
	}
	h, ok := s.getHandle(id)
	if !ok || h.file == nil {
		return s.fail(rw, errors.New("invalid file handle"))
	}
	if _, werr := h.file.WriteAt([]byte(data), int64(off)); werr != nil {
		return s.fail(rw, werr)
	}
	return s.ok(rw)
}

func (s *session) handleFstat(rw io.Writer, rd *reader) error {
	id, err := rd.str()
	if err != nil {
		return err
	}
	h, ok := s.getHandle(id)
	if !ok || h.file == nil {
		return s.fail(rw, errors.New("invalid file handle"))
	}
	info, serr := h.file.Stat()
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

func (s *session) handleSetstat(rw io.Writer, rd *reader) error {
	p, err := rd.str()
	if err != nil {
		return err
	}
	a, err := decodeAttrs(rd)
	if err != nil {
		return err
	}
	if a.Perm != 0 {
		if cerr := s.fs.Chmod(p, os.FileMode(a.Perm)); cerr != nil {
			return s.fail(rw, cerr)
		}
	}
	if a.Size > 0 {
		if terr := s.fs.Truncate(p, int64(a.Size)); terr != nil {
			return s.fail(rw, terr)
		}
	}
	return s.ok(rw)
}

func (s *session) handleRemove(rw io.Writer, rd *reader) error {
	p, err := rd.str()
	if err != nil {
		return err
	}
	if rerr := s.fs.Remove(p); rerr != nil {
		return s.fail(rw, rerr)
	}
	return s.ok(rw)
}

func (s *session) handleMkdir(rw io.Writer, rd *reader) error {
	p, err := rd.str()
	if err != nil {
		return err
	}
	a, _ := decodeAttrs(rd)
	mode := os.FileMode(0o755)
	if a.Perm != 0 {
		mode = os.FileMode(a.Perm)
	}
	if merr := s.fs.Mkdir(p, mode); merr != nil {
		return s.fail(rw, merr)
	}
	return s.ok(rw)
}

func (s *session) handleRmdir(rw io.Writer, rd *reader) error {
	p, err := rd.str()
	if err != nil {
		return err
	}
	if rerr := s.fs.Rmdir(p); rerr != nil {
		return s.fail(rw, rerr)
	}
	return s.ok(rw)
}

func (s *session) handleRename(rw io.Writer, rd *reader) error {
	from, err := rd.str()
	if err != nil {
		return err
	}
	to, err := rd.str()
	if err != nil {
		return err
	}
	if rerr := s.fs.Rename(from, to); rerr != nil {
		return s.fail(rw, rerr)
	}
	return s.ok(rw)
}

// longName renders an `ls -l`-style line for clients that display it.
func longName(vpath string, e os.DirEntry) string {
	info, err := e.Info()
	if err != nil {
		return e.Name()
	}
	return info.Mode().String() + " 1 owner group " + itoa(info.Size()) + " " +
		info.ModTime().Format("Jan _2 15:04") + " " + path.Base(vpath)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
