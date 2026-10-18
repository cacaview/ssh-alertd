package monitor

import (
	"testing"
	"time"
)

func TestParseAccepted(t *testing.T) {
	m := New(nil, "myhost", nil)
	m.now = func() time.Time { return time.Unix(0, 0).UTC() }

	cases := []struct {
		name                   string
		line                   string
		ok                     bool
		user, ip, port, method string
	}{
		{
			name:   "journald cat publickey",
			line:   "Accepted publickey for alice from 203.0.113.5 port 50568 ssh2: ED25519 SHA256:abc",
			ok:     true,
			user:   "alice",
			ip:     "203.0.113.5",
			port:   "50568",
			method: "publickey",
		},
		{
			name:   "syslog prefixed password",
			line:   "Jun 26 09:49:01 host sshd[123]: Accepted password for bob from 10.0.0.2 port 41122 ssh2",
			ok:     true,
			user:   "bob",
			ip:     "10.0.0.2",
			port:   "41122",
			method: "password",
		},
		{
			name: "failed login is ignored",
			line: "Failed password for bob from 10.0.0.2 port 41122 ssh2",
			ok:   false,
		},
		{
			name: "unrelated line",
			line: "Jun 26 09:49:01 host sshd[123]: Server listening on 0.0.0.0 port 22.",
			ok:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, ok := m.parse(tc.line)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !tc.ok {
				return
			}
			if e.Username != tc.user || e.IP != tc.ip || e.Port != tc.port || e.Method != tc.method {
				t.Errorf("got user=%q ip=%q port=%q method=%q", e.Username, e.IP, e.Port, e.Method)
			}
			if e.Hostname != "myhost" {
				t.Errorf("hostname = %q, want myhost", e.Hostname)
			}
		})
	}
}

func TestParseKeyDetails(t *testing.T) {
	m := New(nil, "myhost", nil)
	m.now = func() time.Time { return time.Unix(0, 0).UTC() }

	const ed25519Fingerprint = "SHA256:flbXq7ZJ0j1lEALPYzbOGOVUwI2Yz0Y4yYPqbPZ8ZjQ"

	cases := []struct {
		name      string
		line      string
		algorithm string
		keyFP     string
	}{
		{
			name:      "ed25519 with fingerprint",
			line:      "Accepted publickey for alice from 203.0.113.5 port 50568 ssh2: ED25519 " + ed25519Fingerprint,
			algorithm: "ED25519",
			keyFP:     ed25519Fingerprint,
		},
		{
			name:      "rsa with trailing syslog prefix",
			line:      "Oct 18 10:00:03 davis sshd[104]: Accepted publickey for bob from 203.0.113.9 port 50570 ssh2: RSA SHA256:abc+/def [preauth]",
			algorithm: "RSA",
			keyFP:     "SHA256:abc+/def",
		},
		{
			name:      "ecdsa algorithm name with @-style suffix",
			line:      "Accepted publickey for carol from 192.0.2.10 port 50571 ssh2: ECDSA-CERT SHA256:zzz",
			algorithm: "ECDSA-CERT",
			keyFP:     "SHA256:zzz",
		},
		{
			name:      "security-key algorithm",
			line:      "Accepted publickey for dave from 192.0.2.11 port 50572 ssh2: sk-ssh-ed25519@openssh.com SHA256:qqq",
			algorithm: "sk-ssh-ed25519@openssh.com",
			keyFP:     "SHA256:qqq",
		},
		{
			name: "publickey without key details (LogLevel INFO)",
			line: "Accepted publickey for erin from 192.0.2.12 port 50573 ssh2",
		},
		{
			name: "password login never gets key fields",
			line: "Accepted password for frank from 192.0.2.13 port 50574 ssh2: ED25519 SHA256:should-not-appear",
		},
		{
			name: "keyboard-interactive never gets key fields",
			line: "Accepted keyboard-interactive/pam for gina from 192.0.2.14 port 50575 ssh2: RSA SHA256:should-not-appear",
		},
		{
			name: "fingerprint without algorithm is ignored",
			line: "Accepted publickey for hank from 192.0.2.15 port 50576 ssh2: " + ed25519Fingerprint,
		},
		{
			name: "algorithm without fingerprint is ignored",
			line: "Accepted publickey for ivy from 192.0.2.16 port 50577 ssh2: ED25519",
		},
		{
			name:      "ipv6 client",
			line:      "Accepted publickey for joe from 2001:db8::7 port 50578 ssh2: ED25519 " + ed25519Fingerprint,
			algorithm: "ED25519",
			keyFP:     ed25519Fingerprint,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, ok := m.parse(tc.line)
			if !ok {
				t.Fatalf("line not parsed: %s", tc.line)
			}
			if e.KeyAlgorithm != tc.algorithm || e.KeyFingerprint != tc.keyFP {
				t.Errorf("key = %q/%q, want %q/%q",
					e.KeyAlgorithm, e.KeyFingerprint, tc.algorithm, tc.keyFP)
			}
			if tc.algorithm == "" && (e.Algorithm() != "N/A" || e.Fingerprint() != "N/A") {
				t.Errorf("missing key should render N/A, got %q/%q", e.Algorithm(), e.Fingerprint())
			}
		})
	}
}
