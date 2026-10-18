package notifier

import (
	"mime"
	"strings"
	"testing"
)

// headerLines splits a rendered message into its header lines.
func headerLines(msg string) []string {
	head, _, _ := strings.Cut(msg, "\r\n\r\n")
	return strings.Split(head, "\r\n")
}

func TestSMTPSubjectCannotInjectHeaders(t *testing.T) {
	cases := map[string]string{
		"LF":          "login {{.Username}}",
		"CR":          "login {{.Username}}",
		"CRLF":        "login {{.Username}}",
		"nul and esc": "login {{.Username}}",
	}
	payloads := map[string]string{
		"LF":          "alice\nBcc: attacker@example.com",
		"CR":          "alice\rBcc: attacker@example.com",
		"CRLF":        "alice\r\nBcc: attacker@example.com",
		"nul and esc": "alice\x00\x1bBcc: attacker@example.com",
	}
	for name, tmpl := range cases {
		t.Run(name, func(t *testing.T) {
			s := mustSMTP(t, SMTPOptions{Port: 25, Encryption: "none", SubjectTemplate: tmpl})
			e := testEvent()
			e.Username = payloads[name]
			msg, err := s.message(e)
			if err != nil {
				t.Fatalf("message: %v", err)
			}
			for _, line := range headerLines(msg) {
				if strings.HasPrefix(strings.ToLower(line), "bcc:") {
					t.Fatalf("injected header line: %q\n%s", line, msg)
				}
			}
			if got := len(headerLines(msg)); got != 7 {
				t.Errorf("header lines = %d, want 7\n%s", got, msg)
			}
		})
	}
}

func TestSMTPAddressHeadersAreSingleLine(t *testing.T) {
	s := mustSMTP(t, SMTPOptions{
		Port: 25, Encryption: "none",
		From: "a@example.com\r\nBcc: evil@example.com",
		To:   []string{"b@example.com", "c@example.com\nBcc: evil2@example.com"},
	})
	msg, err := s.message(testEvent())
	if err != nil {
		t.Fatalf("message: %v", err)
	}
	for _, line := range headerLines(msg) {
		if strings.HasPrefix(strings.ToLower(line), "bcc:") {
			t.Fatalf("injected header line: %q\n%s", line, msg)
		}
	}
}

func TestSMTPPlainASCIISubjectIsNotEncoded(t *testing.T) {
	// Plain subjects stay readable, as before.
	s := mustSMTP(t, SMTPOptions{Port: 25, Encryption: "none", SubjectTemplate: "Login {{.Username}}"})
	msg, _ := s.message(testEvent())
	if !strings.Contains(msg, "Subject: Login alice\r\n") {
		t.Errorf("plain subject was encoded\n%s", msg)
	}
}

func TestSMTPNonASCIISubjectRoundTrips(t *testing.T) {
	subject := "登录告警 alice@主机 — ok"
	encoded := encodeHeader(subject)
	if !strings.HasPrefix(encoded, "=?utf-8?") {
		t.Fatalf("not encoded: %q", encoded)
	}
	decoded, err := new(mime.WordDecoder).DecodeHeader(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded != subject {
		t.Errorf("round trip = %q, want %q", decoded, subject)
	}
}

func TestSMTPInvalidUTF8SubjectStillSends(t *testing.T) {
	// A template file with invalid UTF-8 must not make alerts fail.
	bad := "alert \xff\xfe alice"
	header := encodeHeader(bad)
	if strings.ContainsAny(header, "\r\n") {
		t.Fatalf("newline in header: %q", header)
	}
	if strings.ContainsRune(header, 0xff) {
		t.Errorf("invalid bytes left in header: %q", header)
	}

	s := mustSMTP(t, SMTPOptions{Port: 25, Encryption: "none", SubjectTemplate: "{{.Username}}"})
	e := testEvent()
	e.Username = bad
	msg, err := s.message(e)
	if err != nil {
		t.Fatalf("message: %v", err)
	}
	if !strings.Contains(msg, "Subject: ") {
		t.Fatalf("missing subject\n%s", msg)
	}
}

func TestSingleLine(t *testing.T) {
	cases := map[string]string{
		"  a \n b  ":    "a b",
		"a\r\nb":        "a b",
		"a\t\tb":        "a b",
		"a\x00b\x7fc":   "abc",
		"a\x1b[31mred":  "a[31mred",
		"already clean": "already clean",
		"":              "",
		"  \n\r\n  ":    "",
		"ü 名":           "ü 名",
	}
	for in, want := range cases {
		if got := singleLine(in); got != want {
			t.Errorf("singleLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBodyLineEndingsAreCRLF(t *testing.T) {
	s := mustSMTP(t, SMTPOptions{Port: 25, Encryption: "none", BodyTemplate: "a\rb\r\nc\nd"})
	msg, err := s.message(testEvent())
	if err != nil {
		t.Fatalf("message: %v", err)
	}
	body := msg[strings.Index(msg, "\r\n\r\n")+4:]
	if want := "a\r\nb\r\nc\r\nd\r\n"; body != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
	// No bare CR or LF may survive anywhere in the message.
	if strings.Contains(strings.ReplaceAll(msg, "\r\n", ""), "\r") ||
		strings.Contains(strings.ReplaceAll(strings.ReplaceAll(msg, "\r\n", ""), "\r", ""), "\n") {
		t.Fatalf("message has a bare CR or LF:\n%q", msg)
	}
}
