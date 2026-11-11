// Package sftp implements a native SFTP (SSH file transfer) server on top of
// golang.org/x/crypto/ssh. It replaces the `ssh2` based SftpService and keeps the
// same per-user file view.
package sftp

import (
	"encoding/binary"
	"errors"
	"io"
)

// SFTP packet types (version 3).
const (
	fxpInit     = 1
	fxpVersion  = 2
	fxpOpen     = 3
	fxpClose    = 4
	fxpRead     = 5
	fxpWrite    = 6
	fxpLstat    = 7
	fxpFstat    = 8
	fxpSetstat  = 9
	fxpFsetstat = 10
	fxpOpendir  = 11
	fxpReaddir  = 12
	fxpRemove   = 13
	fxpMkdir    = 14
	fxpRmdir    = 15
	fxpRealpath = 16
	fxpStat     = 17
	fxpRename   = 18
	fxpReadlink = 19
	fxpSymlink  = 20

	fxpStatus = 101
	fxpHandle = 102
	fxpData   = 103
	fxpName   = 104
	fxpAttrs  = 105
)

// SFTP status codes.
const (
	statusOK            = 0
	statusEOF           = 1
	statusNoSuchFile    = 2
	statusPermission    = 3
	statusFailure       = 4
	statusBadMessage    = 5
	statusOpUnsupported = 8
)

// open flags.
const (
	fxfRead   = 0x00000001
	fxfWrite  = 0x00000002
	fxfAppend = 0x00000004
	fxfCreat  = 0x00000008
	fxfTrunc  = 0x00000010
	fxfExcl   = 0x00000020
)

// attr flags.
const (
	attrSize        = 0x00000001
	attrUIDGID      = 0x00000002
	attrPermissions = 0x00000004
	attrACModTime   = 0x00000008
	attrExtended    = 0x80000000
)

var errShortPacket = errors.New("sftp: short packet")

// reader is a minimal binary reader for SFTP payloads.
type reader struct {
	buf []byte
	off int
}

func newReader(b []byte) *reader { return &reader{buf: b} }

func (r *reader) remaining() int { return len(r.buf) - r.off }

func (r *reader) u8() (byte, error) {
	if r.remaining() < 1 {
		return 0, errShortPacket
	}
	v := r.buf[r.off]
	r.off++
	return v, nil
}

func (r *reader) u32() (uint32, error) {
	if r.remaining() < 4 {
		return 0, errShortPacket
	}
	v := binary.BigEndian.Uint32(r.buf[r.off:])
	r.off += 4
	return v, nil
}

func (r *reader) u64() (uint64, error) {
	if r.remaining() < 8 {
		return 0, errShortPacket
	}
	v := binary.BigEndian.Uint64(r.buf[r.off:])
	r.off += 8
	return v, nil
}

func (r *reader) str() (string, error) {
	n, err := r.u32()
	if err != nil {
		return "", err
	}
	if int(n) > r.remaining() {
		return "", errShortPacket
	}
	s := string(r.buf[r.off : r.off+int(n)])
	r.off += int(n)
	return s, nil
}

// writer builds SFTP payloads.
type writer struct{ buf []byte }

func (w *writer) u8(v byte) { w.buf = append(w.buf, v) }
func (w *writer) u32(v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	w.buf = append(w.buf, b[:]...)
}
func (w *writer) u64(v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	w.buf = append(w.buf, b[:]...)
}
func (w *writer) str(s string) {
	w.u32(uint32(len(s)))
	w.buf = append(w.buf, s...)
}
func (w *writer) bytes(b []byte) {
	w.u32(uint32(len(b)))
	w.buf = append(w.buf, b...)
}

// readPacket reads one length-prefixed SFTP packet from r.
func readPacket(r io.Reader) (byte, []byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n == 0 || n > 1<<20 {
		return 0, nil, errors.New("sftp: invalid packet length")
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return payload[0], payload[1:], nil
}

// writePacket writes one length-prefixed SFTP packet to w.
func writePacket(w io.Writer, typ byte, payload []byte) error {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)+1))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	if _, err := w.Write([]byte{typ}); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// attrs describes file attributes in SFTP v3 format.
type attrs struct {
	Size  uint64
	Perm  uint32
	Atime uint32
	Mtime uint32
}

func encodeAttrs(w *writer, a attrs) {
	flags := uint32(attrSize | attrPermissions | attrACModTime)
	w.u32(flags)
	w.u64(a.Size)
	w.u32(a.Perm)
	w.u32(a.Atime)
	w.u32(a.Mtime)
}

// decodeAttrs reads an ATTRS structure.
func decodeAttrs(r *reader) (attrs, error) {
	var a attrs
	flags, err := r.u32()
	if err != nil {
		return a, err
	}
	if flags&attrSize != 0 {
		if a.Size, err = r.u64(); err != nil {
			return a, err
		}
	}
	if flags&attrUIDGID != 0 {
		if _, err = r.u32(); err != nil {
			return a, err
		}
		if _, err = r.u32(); err != nil {
			return a, err
		}
	}
	if flags&attrPermissions != 0 {
		if a.Perm, err = r.u32(); err != nil {
			return a, err
		}
	}
	if flags&attrACModTime != 0 {
		if a.Atime, err = r.u32(); err != nil {
			return a, err
		}
		if a.Mtime, err = r.u32(); err != nil {
			return a, err
		}
	}
	if flags&attrExtended != 0 {
		count, err := r.u32()
		if err != nil {
			return a, err
		}
		for i := uint32(0); i < count; i++ {
			if _, err := r.str(); err != nil {
				return a, err
			}
			if _, err := r.str(); err != nil {
				return a, err
			}
		}
	}
	return a, nil
}
