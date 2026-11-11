package sftp

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/incurl/pocketghost/internal/logging"
)

type staticAuth struct{ root string }

func (a staticAuth) Password(user, pass string) (string, bool) {
	if user == "owner@example.com" && pass == "secret" {
		return a.root, true
	}
	return "", false
}

func (a staticAuth) PublicKey(string, ssh.PublicKey) (string, bool) { return "", false }

func TestPacketRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	w := &writer{}
	w.u32(7)
	w.str("hello")
	encodeAttrs(w, attrs{Size: 12, Perm: 0o644, Atime: 1, Mtime: 2})

	if err := writePacket(&buf, fxpName, w.buf); err != nil {
		t.Fatal(err)
	}

	typ, payload, err := readPacket(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if typ != fxpName {
		t.Fatalf("type = %d", typ)
	}

	r := newReader(payload)
	n, err := r.u32()
	if err != nil || n != 7 {
		t.Fatalf("n = %d err=%v", n, err)
	}
	s, err := r.str()
	if err != nil || s != "hello" {
		t.Fatalf("str = %q err=%v", s, err)
	}
	a, err := decodeAttrs(r)
	if err != nil {
		t.Fatal(err)
	}
	if a.Size != 12 || a.Perm != 0o644 {
		t.Fatalf("attrs = %+v", a)
	}
}

func TestShortPacket(t *testing.T) {
	if _, _, err := readPacket(bytes.NewReader([]byte{0, 0, 0})); err == nil {
		t.Fatal("expected length error")
	}
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], 100)
	if _, _, err := readPacket(bytes.NewReader(buf[:])); err == nil {
		t.Fatal("expected truncated packet error")
	}
}

func TestSSHSessionAndRealpath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pb_public"), 0o755); err != nil {
		t.Fatal(err)
	}

	srv, err := Listen(Config{
		Addr:        "127.0.0.1:0",
		HostKeyPath: filepath.Join(t.TempDir(), "host_key"),
		Auth:        staticAuth{root: root},
		Logger:      logging.Discard(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	go func() {
		ctx, cancel := contextWithTimeout(10 * time.Second)
		defer cancel()
		_ = srv.Serve(ctx)
	}()

	client, err := ssh.Dial("tcp", srv.Addr().String(), &ssh.ClientConfig{
		User:            "owner@example.com",
		Auth:            []ssh.AuthMethod{ssh.Password("secret")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.RequestSubsystem("sftp"); err != nil {
		t.Fatalf("subsystem: %v", err)
	}

	// INIT
	initPayload := &writer{}
	initPayload.u32(3)
	if err := writePacket(stdin, fxpInit, initPayload.buf); err != nil {
		t.Fatal(err)
	}
	typ, payload, err := readPacket(stdout)
	if err != nil {
		t.Fatalf("read version: %v", err)
	}
	if typ != fxpVersion {
		t.Fatalf("type = %d", typ)
	}
	r := newReader(payload)
	version, _ := r.u32()
	if version != 3 {
		t.Fatalf("version = %d", version)
	}

	// REALPATH
	rp := &writer{}
	rp.str("pb_public")
	if err := writePacket(stdin, fxpRealpath, rp.buf); err != nil {
		t.Fatal(err)
	}
	typ, payload, err = readPacket(stdout)
	if err != nil {
		t.Fatalf("read realpath: %v", err)
	}
	if typ != fxpName {
		t.Fatalf("realpath type = %d", typ)
	}
	r = newReader(payload)
	count, _ := r.u32()
	if count != 1 {
		t.Fatalf("names = %d", count)
	}
	name, _ := r.str()
	if name != "/pb_public" {
		t.Fatalf("realpath = %q", name)
	}

	// OPENDIR + READDIR
	od := &writer{}
	od.str("/pb_public")
	if err := writePacket(stdin, fxpOpendir, od.buf); err != nil {
		t.Fatal(err)
	}
	typ, payload, err = readPacket(stdout)
	if err != nil || typ != fxpHandle {
		t.Fatalf("opendir type=%d err=%v", typ, err)
	}
	r = newReader(payload)
	handle, _ := r.str()

	rd := &writer{}
	rd.str(handle)
	if err := writePacket(stdin, fxpReaddir, rd.buf); err != nil {
		t.Fatal(err)
	}
	typ, _, err = readPacket(stdout)
	if err != nil {
		t.Fatal(err)
	}
	if typ != fxpStatus && typ != fxpName {
		t.Fatalf("readdir type = %d", typ)
	}

	_ = io.Discard
}

// TestHostKeyPersistence ensures the generated host key is stable across restarts.
func TestHostKeyPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host_key")
	if _, err := hostKey(path); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hostKey(path); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if !bytes.Equal(first, second) {
		t.Fatal("host key was regenerated")
	}
}

func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
