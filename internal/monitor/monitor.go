// Package monitor reads sshd log output, parses successful-login lines into
// LoginEvents, and hands them to a callback.
package monitor

import (
	"context"
	"log"
	"regexp"
	"time"

	"ssh-alertd/internal/event"
)

// acceptedRE matches the sshd "Accepted" line emitted on a successful login,
// regardless of any leading syslog timestamp/host/pid prefix. The captured
// "from <ip> port <port>" describe the remote (client) endpoint, so the port is
// the client's source port, not the server's listening port. Example:
//
//	Accepted password for bob from 10.0.0.2 port 41122 ssh2
//
// Public-key logins append the key details when sshd runs with
// LogLevel VERBOSE or higher:
//
//	Accepted publickey for alice from 203.0.113.5 port 50568 ssh2: ED25519 SHA256:...
var acceptedRE = regexp.MustCompile(
	`Accepted (\S+) for (?:invalid user )?(\S+) from (\S+) port (\d+)`,
)

// publickeyRE matches the key details at the end of an accepted public-key
// line. Anything after "ssh2:" may be appended by sshd ("... [preauth]") or by
// the journal/syslog, and is ignored: only the text immediately after the
// prefix is parsed. A line without these details (LogLevel INFO) does not match
// and the key fields stay unknown.
var publickeyRE = regexp.MustCompile(
	` ssh2: ([A-Za-z0-9@._+-]+) (SHA256:[A-Za-z0-9+/=_-]+)`,
)

// Handler consumes parsed login events.
type Handler func(ctx context.Context, e event.LoginEvent)

// Monitor wires a Source to a Handler.
type Monitor struct {
	source   Source
	hostname string
	logger   *log.Logger
	// now is injectable for testing; defaults to time.Now.
	now func() time.Time
}

// New creates a Monitor.
func New(source Source, hostname string, logger *log.Logger) *Monitor {
	if logger == nil {
		logger = log.Default()
	}
	return &Monitor{source: source, hostname: hostname, logger: logger, now: time.Now}
}

// Run streams from the source until ctx is cancelled, invoking handler for each
// successful login. It returns when the source's line channel closes.
func (m *Monitor) Run(ctx context.Context, handler Handler) error {
	lines, err := m.source.Lines(ctx)
	if err != nil {
		return err
	}
	m.logger.Printf("monitor started on source %s (host=%s)", m.source.Name(), m.hostname)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line, ok := <-lines:
			if !ok {
				m.logger.Printf("monitor: source %s closed", m.source.Name())
				return nil
			}
			if e, ok := m.parse(line); ok {
				m.logger.Printf("detected %s", e)
				handler(ctx, e)
			}
		}
	}
}

// parse extracts a LoginEvent from a raw log line. The second return value is
// false when the line is not a successful-login line.
func (m *Monitor) parse(line string) (event.LoginEvent, bool) {
	match := acceptedRE.FindStringSubmatch(line)
	if match == nil {
		return event.LoginEvent{}, false
	}
	e := event.LoginEvent{
		Method:   match[1],
		Username: match[2],
		IP:       match[3],
		Port:     match[4],
		Hostname: m.hostname,
		Time:     m.now(),
	}
	// Only a publickey login can carry key details, and only the server's own
	// report is trusted; there is no fallback to local key files.
	if e.Method == "publickey" {
		if key := publickeyRE.FindStringSubmatch(line); key != nil {
			e.KeyAlgorithm = key[1]
			e.KeyFingerprint = key[2]
		}
	}
	return e, true
}
