package notifier

import (
	"bufio"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubServer is a minimal SMTP server for tests. handle returns the reply to a
// command ("" hangs up without a reply). When the reply to DATA starts with
// "354", the body is read up to the closing dot, recorded, and handle is then
// called with "." to produce the reply to the message.
type stubServer struct {
	ln       net.Listener
	mu       sync.Mutex
	conns    int
	messages []string
	handle   func(cmd string) string
}

func newStubServer(t *testing.T, handle func(cmd string) string) *stubServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &stubServer{ln: ln, handle: handle}
	go s.serve()
	t.Cleanup(func() { ln.Close() })
	return s
}

func (s *stubServer) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

func (s *stubServer) connectionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns
}

func (s *stubServer) delivered() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.messages...)
}

func (s *stubServer) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns++
		s.mu.Unlock()
		go s.handleConn(c)
	}
}

func (s *stubServer) handleConn(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	write := func(line string) bool {
		_, err := c.Write([]byte(line + "\r\n"))
		return err == nil
	}
	if !write("220 stub ready") {
		return
	}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.TrimRight(line, "\r\n")
		reply := s.handle(cmd)
		if reply == "" {
			return
		}
		if !write(reply) {
			return
		}

		if strings.EqualFold(cmd, "DATA") && strings.HasPrefix(reply, "354") {
			var body strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				body.WriteString(l)
			}
			s.mu.Lock()
			s.messages = append(s.messages, body.String())
			s.mu.Unlock()
			reply := s.handle(".")
			if reply == "" {
				return
			}
			if !write(reply) {
				return
			}
		}
		if strings.EqualFold(cmd, "QUIT") {
			return
		}
	}
}

func okServer(t *testing.T) *stubServer {
	t.Helper()
	return newStubServer(t, func(cmd string) string {
		up := strings.ToUpper(cmd)
		switch {
		case strings.HasPrefix(up, "EHLO"):
			return "250-stub\r\n250 8BITMIME"
		case up == "DATA":
			return "354 go ahead"
		case cmd == ".":
			return "250 queued"
		case strings.HasPrefix(up, "QUIT"):
			return "221 bye"
		default:
			return "250 ok"
		}
	})
}

func smtpTo(port int) *SMTP {
	s, err := NewSMTP(SMTPOptions{
		Host: "127.0.0.1", Port: port, Encryption: "none",
		From: "alert@example.com", To: []string{"admin@example.com"},
	})
	if err != nil {
		panic(err)
	}
	return s
}

func withRetry(s *SMTP, attempts int, delay time.Duration) *SMTP {
	s.retry = retryPolicy{Attempts: attempts, Delay: delay}
	return s
}
