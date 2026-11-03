package helperipc

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// handshakeTimeout bounds the wait between launching the helper and its Hello.
const handshakeTimeout = 10 * time.Second

// Server is the per-launch listener the helper connects back to. The socket lives in a freshly
// created 0700 directory under the user's private temp dir and accepts exactly one helper: the one
// that presents Token (256 random bits, passed on its command line) and, on macOS, whose pid is
// the child we spawned (LOCAL_PEERPID). Nothing else on the machine can drive capture or input.
type Server struct {
	dir   string
	path  string
	token string
	ln    *net.UnixListener
}

// Listen creates the socket. Callers pass Path() and Token() to the helper and must Close().
func Listen() (*Server, error) {
	dir, err := os.MkdirTemp("", "solco-rd-")
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	path := filepath.Join(dir, "h.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	return &Server{dir: dir, path: path, token: hex.EncodeToString(raw), ln: ln}, nil
}

// Path is the socket path (short enough for sockaddr_un's 104 bytes on macOS).
func (s *Server) Path() string { return s.path }

// Token is the one-launch secret the helper must present.
func (s *Server) Token() string { return s.token }

// Close removes the socket and its directory.
func (s *Server) Close() error {
	err := s.ln.Close()
	_ = os.RemoveAll(s.dir)
	return err
}

// Accept waits for the helper, verifies it and returns the live Conn. expectedPID is the spawned
// child's pid (0 skips the peer-pid check, for tests with in-process fakes).
func (s *Server) Accept(expectedPID int) (*Conn, error) {
	_ = s.ln.SetDeadline(time.Now().Add(handshakeTimeout))
	uc, err := s.ln.AcceptUnix()
	if err != nil {
		return nil, fmt.Errorf("helper did not connect: %w", err)
	}
	if expectedPID != 0 {
		pid, err := peerPID(uc)
		if err != nil || (pid != 0 && pid != expectedPID) {
			_ = uc.Close()
			return nil, errors.New("helperipc: peer is not the launched helper")
		}
	}
	_ = uc.SetReadDeadline(time.Now().Add(handshakeTimeout))
	br := bufio.NewReader(uc)
	kind, payload, err := ReadFrame(br)
	if err != nil || kind != KindControl {
		_ = uc.Close()
		return nil, errors.New("helperipc: no hello")
	}
	var hello Hello
	if json.Unmarshal(payload, &hello) != nil || hello.T != "hello" {
		_ = uc.Close()
		return nil, errors.New("helperipc: malformed hello")
	}
	if subtle.ConstantTimeCompare([]byte(hello.Token), []byte(s.token)) != 1 {
		_ = uc.Close()
		return nil, errors.New("helperipc: bad token")
	}
	ack := HelloAck{T: "hello_ack", ProtocolVersion: ProtocolVersion, OK: hello.ProtocolVersion == ProtocolVersion}
	if !ack.OK {
		ack.Error = &ProtocolError{Code: CodeProtocolMismatch, Message: fmt.Sprintf("app speaks %d, helper %d", ProtocolVersion, hello.ProtocolVersion)}
	}
	ackBytes, _ := json.Marshal(ack)
	if err := WriteFrame(uc, KindControl, ackBytes); err != nil {
		_ = uc.Close()
		return nil, err
	}
	if !ack.OK {
		_ = uc.Close()
		return nil, fmt.Errorf("helper protocol mismatch: helper %d, app %d", hello.ProtocolVersion, ProtocolVersion)
	}
	_ = uc.SetReadDeadline(time.Time{})
	c := newConn(uc, hello)
	c.start(br)
	return c, nil
}
