package notifier

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"mime"
	"net"
	"net/smtp"
	"net/textproto"
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

	// RetryAttempts is the total number of attempts per alert, including the
	// first (0 or 1 disables retrying). RetryDelay is the first backoff, doubled
	// on each retry up to RetryMaxDelay.
	RetryAttempts int
	RetryDelay    time.Duration
	RetryMaxDelay time.Duration

	// Logf receives retry notices; nil discards them.
	Logf func(format string, args ...any)
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

	retry retryPolicy
	logf  func(format string, args ...any)

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
		retry:         retryPolicy{Attempts: o.RetryAttempts, Delay: o.RetryDelay, MaxDelay: o.RetryMaxDelay},
		logf:          o.Logf,
		renderSubject: subject,
		renderBody:    body,
	}, nil
}

// logRetry reports a retry decision when a logger is configured.
func (s *SMTP) logRetry(attempt int, wait time.Duration, err error) {
	if s.logf == nil {
		return
	}
	s.logf("smtp attempt %d failed, retrying in %s: %v", attempt, wait, err)
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
// bounds the whole attempt. Transient failures are retried according to
// SMTPOptions.Retry; a failure after the message body was committed to the
// server is returned as-is and never retried, because the server may already
// have accepted and delivered the message.
func (s *SMTP) Send(ctx context.Context, e event.LoginEvent) error {
	msg, err := s.message(e)
	if err != nil {
		return err
	}

	retry := s.retry.withDefaults()
	for attempt := 1; ; attempt++ {
		err := s.deliver(ctx, msg)
		if err == nil {
			return nil
		}

		var te *transientError
		if !errors.As(err, &te) {
			return err
		}
		if attempt >= retry.Attempts {
			return fmt.Errorf("%w (gave up after %d attempt(s))", err, attempt)
		}
		if ctx.Err() != nil {
			return err
		}

		wait := retry.delay(attempt)
		if dl, ok := ctx.Deadline(); ok && time.Until(dl) <= wait {
			if attempt == 1 {
				// The first attempt was all the time budget allowed; waiting is
				// pointless, but the failure itself is still the result.
				return err
			}
			return fmt.Errorf("%w (no time left for attempt %d)", err, attempt+1)
		}
		s.logRetry(attempt, wait, err)
		select {
		case <-ctx.Done():
			return err
		case <-time.After(wait):
		}
	}
}

// deliver makes one SMTP attempt. Errors that are worth retrying are wrapped in
// transientError; everything else is final for this event.
func (s *SMTP) deliver(ctx context.Context, msg string) error {
	addr := net.JoinHostPort(s.host, strconv.Itoa(s.port))

	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		// Connecting is retryable, with one exception: a name that does not
		// resolve will not start resolving because we waited a second.
		if dnsErr := (*net.DNSError)(nil); errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return fmt.Errorf("dial %s: %w", addr, err)
		}
		return transientf("dial %s: %w", addr, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	tlsConfig := &tls.Config{ServerName: s.host}
	if s.encryption == smtpImplicit {
		conn = tls.Client(conn, tlsConfig)
	}

	// The greeting the server sends on connect: a temporary rejection (4xx) is
	// worth retrying, anything else is not.
	client, err := smtp.NewClient(conn, s.host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp client: %w", retryable(err))
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

	// Authentication, sender and recipient rejections are configuration or
	// policy problems (wrong password, relay denied, unknown user); retrying
	// cannot fix them, so they are never retried.
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
		return fmt.Errorf("DATA: %w", retryable(err))
	}

	// commit publishes the message body. From the closing dot on the outcome is
	// unknown: the server may accept (250), reject, or die after storing it. A
	// retry from here could deliver the alert twice, so every later failure —
	// including the QUIT below — is final.
	commit := func() error {
		if _, err := w.Write([]byte(msg)); err != nil {
			return fmt.Errorf("write body: %w", err)
		}
		if err := w.Close(); err != nil {
			return fmt.Errorf("deliver message (outcome unknown, not retried): %w", err)
		}
		if err := client.Quit(); err != nil {
			return fmt.Errorf("message accepted but QUIT failed: %w", err)
		}
		return nil
	}
	return commit()
}

// retryPolicy is the resolved retry configuration used by Send.
type retryPolicy struct {
	Attempts int           // total attempts, including the first
	Delay    time.Duration // first backoff, doubled each retry, capped at MaxDelay
	MaxDelay time.Duration
}

// withDefaults fills unset limits. The defaults keep one alert inside the
// dispatcher's per-send timeout while still riding out a brief network blip.
func (r retryPolicy) withDefaults() retryPolicy {
	if r.Attempts <= 0 {
		r.Attempts = 1
	}
	if r.Delay <= 0 {
		r.Delay = time.Second
	}
	if r.MaxDelay <= 0 {
		r.MaxDelay = 5 * time.Second
	}
	return r
}

// delay returns the backoff before retry number n (1-based). The exponential
// growth is capped so a large attempt count cannot overflow the duration.
func (r retryPolicy) delay(n int) time.Duration {
	d := r.Delay
	for i := 1; i < n; i++ {
		if d >= r.MaxDelay/2 {
			return r.MaxDelay
		}
		d *= 2
	}
	if d > r.MaxDelay {
		return r.MaxDelay
	}
	return d
}

// transientError marks a failure worth retrying.
type transientError struct{ err error }

func (t *transientError) Error() string { return t.err.Error() }
func (t *transientError) Unwrap() error { return t.err }

func transientf(format string, args ...any) error {
	return &transientError{err: fmt.Errorf(format, args...)}
}

// retryable wraps err in a transientError when err looks like a temporary SMTP
// rejection (4xx). Network failures surface as *net.OpError or a TLS error with
// no SMTP status and are left final: reconnecting may help, but treating every
// I/O error as retryable would also mask configuration mistakes such as a
// hostname that does not resolve.
func retryable(err error) error {
	var tp *textproto.Error
	if errors.As(err, &tp) && tp.Code >= 400 && tp.Code < 500 {
		return &transientError{err: err}
	}
	return err
}

// message renders an RFC 5322 message with CRLF line endings. The subject is
// MIME word-encoded so non-ASCII (e.g. a custom template in Chinese) is safe in
// the header; the body carries the rest, as text/plain or text/html.
func (s *SMTP) message(e event.LoginEvent) (string, error) {
	subject, err := s.renderSubject(e)
	if err != nil {
		return "", fmt.Errorf("render subject: %w", err)
	}
	// A subject is a single header line.
	subject = strings.TrimSpace(strings.ReplaceAll(subject, "\n", " "))

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
		"From: " + s.from,
		"To: " + strings.Join(s.to, ", "),
		"Subject: " + mime.QEncoding.Encode("utf-8", subject),
		"MIME-Version: 1.0",
		"Content-Type: " + contentType,
		"Content-Transfer-Encoding: 8bit",
	}

	// Normalise body line endings to CRLF.
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\n", "\r\n")
	return strings.Join(headers, "\r\n") + "\r\n\r\n" + body + "\r\n", nil
}
