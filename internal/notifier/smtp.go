package notifier

import (
	"context"
	"crypto/tls"
	"fmt"
	htmltemplate "html/template"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	texttemplate "text/template"
	"time"

	"ssh-alertd/internal/event"
)

// SMTP encryption modes.
const (
	smtpStartTLS = "starttls" // upgrade a plaintext connection (usually port 587)
	smtpImplicit = "tls"      // implicit TLS / SMTPS (usually port 465)
	smtpNone     = "none"     // no transport security (usually port 25)
)

// renderFunc turns a login event into a rendered string (subject or body).
type renderFunc func(event.LoginEvent) (string, error)

// SMTPOptions configures an SMTP notifier. BodyTemplate is the already-resolved
// template text (the caller reads any template file first).
type SMTPOptions struct {
	Host            string
	Port            int
	Username        string
	Password        string
	From            string
	To              []string
	Encryption      string
	SubjectTemplate string
	BodyTemplate    string
	HTML            bool
}

// SMTP delivers alerts as email over SMTP, with optional custom templates.
type SMTP struct {
	host       string
	port       int
	username   string
	password   string
	from       string
	to         []string
	encryption string
	html       bool

	renderSubject renderFunc
	renderBody    renderFunc
}

// NewSMTP builds an SMTP notifier and compiles any custom templates up front so
// template errors surface at startup rather than on the first login. encryption
// is one of "starttls", "tls" or "none"; when empty it is inferred from the
// port (465 → tls, else starttls).
func NewSMTP(o SMTPOptions) (*SMTP, error) {
	enc := o.Encryption
	if enc == "" {
		if o.Port == 465 {
			enc = smtpImplicit
		} else {
			enc = smtpStartTLS
		}
	}

	subject, err := buildSubjectRenderer(o.SubjectTemplate)
	if err != nil {
		return nil, fmt.Errorf("subject_template: %w", err)
	}
	body, err := buildBodyRenderer(o.BodyTemplate, o.HTML)
	if err != nil {
		return nil, fmt.Errorf("body_template: %w", err)
	}

	return &SMTP{
		host:          o.Host,
		port:          o.Port,
		username:      o.Username,
		password:      o.Password,
		from:          o.From,
		to:            append([]string(nil), o.To...),
		encryption:    enc,
		html:          o.HTML,
		renderSubject: subject,
		renderBody:    body,
	}, nil
}

// Name implements Notifier.
func (s *SMTP) Name() string { return "smtp" }

// buildSubjectRenderer returns a renderer for the subject. The subject is always
// plain text, so it uses text/template; an empty template uses the built-in.
func buildSubjectRenderer(tmpl string) (renderFunc, error) {
	if tmpl == "" {
		return func(e event.LoginEvent) (string, error) {
			return fmt.Sprintf("[ssh-alertd] SSH login: %s@%s from %s",
				e.Username, e.Hostname, e.IP), nil
		}, nil
	}
	t, err := texttemplate.New("subject").Parse(tmpl)
	if err != nil {
		return nil, err
	}
	return func(e event.LoginEvent) (string, error) {
		var b strings.Builder
		if err := t.Execute(&b, e); err != nil {
			return "", err
		}
		return b.String(), nil
	}, nil
}

// buildBodyRenderer returns a renderer for the body. An empty template uses the
// built-in plain body; html selects html/template (auto-escaping) over text.
func buildBodyRenderer(tmpl string, html bool) (renderFunc, error) {
	if tmpl == "" {
		return func(e event.LoginEvent) (string, error) {
			return plainBody(e), nil
		}, nil
	}
	if html {
		t, err := htmltemplate.New("body").Parse(tmpl)
		if err != nil {
			return nil, err
		}
		return func(e event.LoginEvent) (string, error) {
			var b strings.Builder
			if err := t.Execute(&b, e); err != nil {
				return "", err
			}
			return b.String(), nil
		}, nil
	}
	t, err := texttemplate.New("body").Parse(tmpl)
	if err != nil {
		return nil, err
	}
	return func(e event.LoginEvent) (string, error) {
		var b strings.Builder
		if err := t.Execute(&b, e); err != nil {
			return "", err
		}
		return b.String(), nil
	}, nil
}

// Send implements Notifier by delivering one email per event. The context
// bounds the initial connection and the overall exchange via a deadline.
func (s *SMTP) Send(ctx context.Context, e event.LoginEvent) error {
	msg, err := s.message(e)
	if err != nil {
		return err
	}

	addr := net.JoinHostPort(s.host, strconv.Itoa(s.port))

	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	tlsConfig := &tls.Config{ServerName: s.host}
	if s.encryption == smtpImplicit {
		conn = tls.Client(conn, tlsConfig)
	}

	client, err := smtp.NewClient(conn, s.host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp client: %w", err)
	}
	defer client.Close()

	if s.encryption == smtpStartTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("server %s does not advertise STARTTLS", s.host)
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}

	if s.username != "" {
		auth := smtp.PlainAuth("", s.username, s.password, s.host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	}

	if err := client.Mail(s.from); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	for _, rcpt := range s.to {
		if err := client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("RCPT TO %s: %w", rcpt, err)
		}
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return fmt.Errorf("write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close body: %w", err)
	}
	return client.Quit()
}

// message renders an RFC 5322 message with CRLF line endings. The subject is
// MIME word-encoded so non-ASCII (e.g. a custom template in Chinese) is safe in
// the header; the body carries the rest, as text/plain or text/html.
func (s *SMTP) message(e event.LoginEvent) (string, error) {
	subject, err := s.renderSubject(e)
	if err != nil {
		return "", fmt.Errorf("render subject: %w", err)
	}
	// A subject is a single header line: strip every control character (a CR or
	// LF from an event value such as a username would otherwise start a new
	// header) and fold the whitespace left behind.
	subject = singleLine(subject)

	body, err := s.renderBody(e)
	if err != nil {
		return "", fmt.Errorf("render body: %w", err)
	}

	contentType := "text/plain; charset=UTF-8"
	if s.html {
		contentType = "text/html; charset=UTF-8"
	}

	headers := []string{
		"Date: " + e.Time.Format(time.RFC1123Z),
		"From: " + headerValue(s.from),
		"To: " + headerValue(strings.Join(s.to, ", ")),
		"Subject: " + encodeHeader(subject),
		"MIME-Version: 1.0",
		"Content-Type: " + contentType,
		"Content-Transfer-Encoding: 8bit",
	}

	// Normalise body line endings to CRLF.
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\n", "\r\n")
	return strings.Join(headers, "\r\n") + "\r\n\r\n" + body + "\r\n", nil
}

// headerValue makes a configured address header safe on one line. Addresses
// come from the config file, which an administrator controls, but a stray
// newline in one must not be able to add headers to the message.
func headerValue(s string) string {
	return singleLine(s)
}

// singleLine removes control characters (including CR/LF from a rendered
// field) and collapses the runs of whitespace that leaves behind.
func singleLine(s string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case '\r', '\n', '\t', '\v', '\f':
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	return strings.Join(strings.Fields(cleaned), " ")
}

// encodeHeader MIME word-encodes a header value only when it is not plain
// ASCII, or when encoding is not lossless. Q-encoding cannot carry arbitrary
// bytes faithfully, so a template file with invalid UTF-8 is sent as a
// sanitized raw header instead of failing the alert.
func encodeHeader(s string) string {
	if isPlainHeader(s) {
		return s
	}
	decoded, err := new(mime.WordDecoder).DecodeHeader(mime.QEncoding.Encode("utf-8", s))
	if err != nil || decoded != s {
		return singleLine(strings.ToValidUTF8(s, "?"))
	}
	return mime.QEncoding.Encode("utf-8", s)
}

// isPlainHeader reports whether s is printable ASCII and therefore needs no
// MIME encoding.
func isPlainHeader(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}
