package notifier

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSMTPDelivers(t *testing.T) {
	srv := okServer(t)
	if err := smtpTo(srv.port()).Send(context.Background(), testEvent()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	msgs := srv.delivered()
	if len(msgs) != 1 {
		t.Fatalf("delivered %d messages, want 1", len(msgs))
	}
	if !strings.Contains(msgs[0], "alice") {
		t.Errorf("message body missing username:\n%s", msgs[0])
	}
}

func TestSMTPRetriesTemporaryRejectionAndRecovers(t *testing.T) {
	var once sync.Once
	srv := newStubServer(t, func(cmd string) string {
		up := strings.ToUpper(cmd)
		switch {
		case strings.HasPrefix(up, "EHLO"):
			return "250-stub\r\n250 8BITMIME"
		case up == "DATA":
			temporary := false
			once.Do(func() { temporary = true })
			if temporary {
				return "451 4.3.0 try again later"
			}
			return "354 go ahead"
		case cmd == ".":
			return "250 queued"
		case strings.HasPrefix(up, "QUIT"):
			return "221 bye"
		default:
			return "250 ok"
		}
	})

	s := withRetry(smtpTo(srv.port()), 3, time.Millisecond)
	if err := s.Send(context.Background(), testEvent()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := srv.connectionCount(); got != 2 {
		t.Errorf("connections = %d, want 2 (one rejected, one delivered)", got)
	}
	if got := srv.delivered(); len(got) != 1 {
		t.Errorf("delivered %d messages, want exactly 1", len(got))
	}
}

func TestSMTPNoRetryOnPermanentRejections(t *testing.T) {
	cases := []struct {
		name    string
		command string // failing command prefix
		reply   string
	}{
		{"MAIL FROM 550", "MAIL FROM", "550 5.7.1 sender rejected"},
		{"RCPT TO 550", "RCPT TO", "550 5.1.1 no such user"},
		{"DATA 554", "DATA", "554 5.5.1 transaction failed"},
		{"recipient 450 is final too", "RCPT TO", "450 4.2.0 mailbox busy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newStubServer(t, func(cmd string) string {
				up := strings.ToUpper(cmd)
				switch {
				case strings.HasPrefix(up, "EHLO"):
					return "250-stub\r\n250 8BITMIME"
				case strings.HasPrefix(up, tc.command):
					return tc.reply
				case up == "DATA":
					return "354 go"
				case cmd == ".":
					return "250 queued"
				default:
					return "250 ok"
				}
			})
			s := withRetry(smtpTo(srv.port()), 3, time.Millisecond)
			if err := s.Send(context.Background(), testEvent()); err == nil {
				t.Fatal("expected error")
			}
			if got := srv.connectionCount(); got != 1 {
				t.Errorf("connections = %d, want 1 (no retry)", got)
			}
			if len(srv.delivered()) != 0 {
				t.Error("message should not be delivered")
			}
		})
	}
}

func TestSMTPNoRetryAfterBodyAccepted(t *testing.T) {
	// The server takes the body but rejects the closing dot: the client cannot
	// know whether the message was delivered, so it must not resend.
	srv := newStubServer(t, func(cmd string) string {
		up := strings.ToUpper(cmd)
		switch {
		case strings.HasPrefix(up, "EHLO"):
			return "250-stub\r\n250 8BITMIME"
		case up == "DATA":
			return "354 go"
		case cmd == ".":
			return "451 4.3.0 temporary failure after accepting"
		default:
			return "250 ok"
		}
	})
	s := withRetry(smtpTo(srv.port()), 5, time.Millisecond)
	err := s.Send(context.Background(), testEvent())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not retried") {
		t.Errorf("error should say the outcome is unknown: %v", err)
	}
	if got := srv.connectionCount(); got != 1 {
		t.Errorf("connections = %d, want 1 (never resend after commit)", got)
	}
}

func TestSMTPQuitFailureDoesNotResend(t *testing.T) {
	// Accepted with 250, then the server hangs up instead of answering QUIT.
	srv := newStubServer(t, func(cmd string) string {
		up := strings.ToUpper(cmd)
		switch {
		case strings.HasPrefix(up, "EHLO"):
			return "250-stub\r\n250 8BITMIME"
		case up == "DATA":
			return "354 go"
		case cmd == ".":
			return "250 queued"
		case strings.HasPrefix(up, "QUIT"):
			return ""
		default:
			return "250 ok"
		}
	})
	s := withRetry(smtpTo(srv.port()), 3, time.Millisecond)
	err := s.Send(context.Background(), testEvent())
	if err == nil || !strings.Contains(err.Error(), "QUIT") {
		t.Fatalf("expected QUIT error, got %v", err)
	}
	if got := srv.connectionCount(); got != 1 {
		t.Errorf("connections = %d, want 1 (no resend after acceptance)", got)
	}
	if got := len(srv.delivered()); got != 1 {
		t.Errorf("delivered %d, want 1", got)
	}
}

func TestSMTPExhaustsRetries(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := newStubServer(t, func(cmd string) string {
		mu.Lock()
		seen = append(seen, cmd)
		mu.Unlock()
		t.Logf("server <- %q", cmd)
		up := strings.ToUpper(cmd)
		if strings.HasPrefix(up, "EHLO") {
			return "250-stub\r\n250 8BITMIME"
		}
		if up == "DATA" {
			return "451 try later"
		}
		if up == "QUIT" {
			return "221 bye"
		}
		return "250 ok"
	})
	s := withRetry(smtpTo(srv.port()), 3, time.Millisecond)
	err := s.Send(context.Background(), testEvent())
	if err == nil || !strings.Contains(err.Error(), "gave up after 3 attempt") {
		t.Fatalf("expected give-up error, got %v", err)
	}
	if got := srv.connectionCount(); got != 3 {
		t.Errorf("connections = %d, want 3", got)
	}
}

func TestSMTPContextCancellationStopsRetry(t *testing.T) {
	srv := newStubServer(t, func(cmd string) string {
		if strings.HasPrefix(strings.ToUpper(cmd), "EHLO") {
			return "250-stub\r\n250 8BITMIME"
		}
		if strings.ToUpper(cmd) == "DATA" {
			return "451 try later"
		}
		return "250 ok"
	})
	s := withRetry(smtpTo(srv.port()), 10, time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	if err := s.Send(ctx, testEvent()); err == nil {
		t.Fatal("expected error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Send blocked %s; the backoff must honor ctx", elapsed)
	}
	if got := srv.connectionCount(); got != 1 {
		t.Errorf("connections = %d, want 1 (waiting retry was cancelled)", got)
	}
}

func TestSMTPNoTimeLeftSkipsRetry(t *testing.T) {
	// DATA is rejected temporarily every time, but the backoff is longer than
	// the caller's whole budget: the client must give up after the first
	// attempt with the server's rejection instead of sleeping past the deadline.
	srv := newStubServer(t, func(cmd string) string {
		up := strings.ToUpper(cmd)
		switch {
		case strings.HasPrefix(up, "EHLO"):
			return "250-stub\r\n250 8BITMIME"
		case up == "DATA":
			return "451 try later"
		case up == "QUIT":
			return "221 bye"
		default:
			return "250 ok"
		}
	})
	s := withRetry(smtpTo(srv.port()), 5, time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := s.Send(ctx, testEvent())
	if err == nil || !strings.Contains(err.Error(), "451") {
		t.Fatalf("expected the SMTP rejection, got %v", err)
	}
	if got := srv.connectionCount(); got != 1 {
		t.Errorf("connections = %d, want 1", got)
	}
}

func TestSMTPDoesNotRetryUnknownHost(t *testing.T) {
	s := smtpTo(2525)
	s.host = "does-not-exist.invalid"
	withRetry(s, 5, time.Millisecond)
	start := time.Now()
	if err := s.Send(context.Background(), testEvent()); err == nil {
		t.Fatal("expected error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("unknown host took %s; it should fail fast", elapsed)
	}
}

func TestRetryPolicyDelay(t *testing.T) {
	r := retryPolicy{Attempts: 10, Delay: time.Second, MaxDelay: 5 * time.Second}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second}
	for i, w := range want {
		if got := r.delay(i + 1); got != w {
			t.Errorf("delay(%d) = %s, want %s", i+1, got, w)
		}
	}
	// A large delay must saturate, never overflow into a negative duration.
	big := retryPolicy{Delay: time.Hour, MaxDelay: time.Hour}
	if got := big.delay(1000); got != time.Hour {
		t.Errorf("saturated delay = %s, want 1h", got)
	}
}

func TestRetryPolicyDefaults(t *testing.T) {
	r := retryPolicy{}.withDefaults()
	if r.Attempts != 1 || r.Delay != time.Second || r.MaxDelay != 5*time.Second {
		t.Errorf("defaults = %+v", r)
	}
}

func TestSMTPLogfReportsRetry(t *testing.T) {
	srv := newStubServer(t, func(cmd string) string {
		up := strings.ToUpper(cmd)
		if strings.HasPrefix(up, "EHLO") {
			return "250-stub\r\n250 8BITMIME"
		}
		if up == "DATA" {
			return "451 try later"
		}
		if up == "QUIT" {
			return "221 bye"
		}
		return "250 ok"
	})
	var logged []string
	s := withRetry(smtpTo(srv.port()), 2, time.Millisecond)
	s.logf = func(format string, args ...any) {
		logged = append(logged, fmt.Sprintf(format, args...))
	}
	if err := s.Send(context.Background(), testEvent()); err == nil {
		t.Fatal("expected error")
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "smtp attempt 1 failed") {
		t.Errorf("retry log = %q", logged)
	}
}

func TestSMTPAuthFailureIsFinal(t *testing.T) {
	srv := newStubServer(t, func(cmd string) string {
		up := strings.ToUpper(cmd)
		switch {
		case strings.HasPrefix(up, "EHLO"):
			return "250-stub\r\n250 AUTH PLAIN"
		case strings.HasPrefix(up, "AUTH"):
			return "535 5.7.8 authentication failed"
		default:
			return "250 ok"
		}
	})
	s, err := NewSMTP(SMTPOptions{
		Host: "127.0.0.1", Port: srv.port(), Encryption: "none",
		Username: "u", Password: "wrong", From: "a@example.com", To: []string{"b@example.com"},
	})
	if err != nil {
		t.Fatalf("NewSMTP: %v", err)
	}
	withRetry(s, 3, time.Millisecond)
	if err := s.Send(context.Background(), testEvent()); err == nil {
		t.Fatal("expected auth error")
	}
	if got := srv.connectionCount(); got != 1 {
		t.Errorf("connections = %d, want 1", got)
	}
}

func TestSMTPDoesNotRetryConnectionRefused(t *testing.T) {
	// Grab a free port and close it so nothing listens there.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	s := withRetry(smtpTo(port), 3, time.Millisecond)
	if err := s.Send(context.Background(), testEvent()); err == nil {
		t.Fatal("expected dial error")
	}
}
