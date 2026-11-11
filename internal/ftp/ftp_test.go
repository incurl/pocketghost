package ftp

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/incurl/pocketghost/internal/logging"
)

type staticAuth struct{ root string }

func (a staticAuth) Login(user, pass string) (string, bool) {
	if user == "owner@example.com" && pass == "secret" {
		return a.root, true
	}
	return "", false
}

func TestFTPControlAndPassiveList(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pb_public"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pb_public", "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv, err := Listen(Config{
		Addr:    "127.0.0.1:0",
		PasvIP:  "127.0.0.1",
		PasvMin: 34000,
		PasvMax: 34100,
		Auth:    staticAuth{root: root},
		Logger:  logging.Discard(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	go func() { _ = srv.Serve() }()

	conn, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReader(conn)

	expect := func(code string) string {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if !strings.HasPrefix(line, code) {
			t.Fatalf("expected %s, got %q", code, line)
		}
		return line
	}
	send := func(cmd string) {
		if _, err := conn.Write([]byte(cmd + "\r\n")); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	expect("220")
	send("USER owner@example.com")
	expect("331")
	send("PASS wrong")
	expect("530")
	send("PASS secret")
	expect("230")

	send("PWD")
	if line := expect("257"); !strings.Contains(line, "/") {
		t.Fatalf("pwd = %q", line)
	}

	send("CWD pb_public")
	expect("250")

	send("PASV")
	pasvLine := expect("227")
	start := strings.Index(pasvLine, "(")
	end := strings.Index(pasvLine, ")")
	if start < 0 || end < 0 {
		t.Fatalf("bad PASV reply: %q", pasvLine)
	}
	parts := strings.Split(pasvLine[start+1:end], ",")
	if len(parts) != 6 {
		t.Fatalf("bad PASV tuple: %q", pasvLine)
	}
	port := atoi(parts[4])*256 + atoi(parts[5])

	dataConn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer dataConn.Close()

	send("LIST")
	expect("150")
	data, err := bufio.NewReader(dataConn).ReadString('\n')
	if err != nil && data == "" {
		t.Fatalf("list read: %v", err)
	}
	if !strings.Contains(data, "hello.txt") {
		t.Fatalf("listing = %q", data)
	}
	expect("226")

	send("SIZE hello.txt")
	expect("213")

	send("QUIT")
	expect("221")
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
