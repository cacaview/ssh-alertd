// Package event defines the domain model shared between the log monitor
// (producer) and the notifiers (consumers).
package event

import (
	"fmt"
	"time"
)

// UnknownKey marks a field that is not available. A missing public-key
// fingerprint must never be guessed or derived from other data, so every
// "cannot tell" case renders as this value.
const UnknownKey = "N/A"

// LoginEvent describes a successful SSH login detected by the monitor.
type LoginEvent struct {
	// Username is the account that was logged into.
	Username string
	// IP is the remote source address of the connection.
	IP string
	// Port is the client's source (ephemeral) port of the connection, as logged
	// by sshd in "... from <IP> port <Port>". It is NOT the server's listening
	// port (e.g. 22).
	Port string
	// Method is the authentication method, e.g. "password" or "publickey".
	Method string
	// KeyAlgorithm is the public-key algorithm reported by sshd (ED25519, RSA,
	// ECDSA, ...). It is UnknownKey unless the login used publickey and the log
	// line carried the key details, which requires LogLevel VERBOSE or higher.
	KeyAlgorithm string
	// KeyFingerprint is the SHA256 fingerprint of the key (as in
	// "SHA256:..."), or UnknownKey when the log did not include it.
	KeyFingerprint string
	// Hostname is the host that received the login (the machine we run on).
	Hostname string
	// Time is when the login was observed.
	Time time.Time
}

// Algorithm returns KeyAlgorithm, or UnknownKey when it was not recovered.
func (e LoginEvent) Algorithm() string {
	if e.KeyAlgorithm == "" {
		return UnknownKey
	}
	return e.KeyAlgorithm
}

// Fingerprint returns KeyFingerprint, or UnknownKey when it was not recovered.
func (e LoginEvent) Fingerprint() string {
	if e.KeyFingerprint == "" {
		return UnknownKey
	}
	return e.KeyFingerprint
}

// String renders a compact, human-readable one-line summary. Notifiers that
// support rich formatting build their own representation; this is the plain
// fallback and is handy for logging.
func (e LoginEvent) String() string {
	return fmt.Sprintf("SSH login: user=%s ip=%s client_port=%s method=%s key=%s fingerprint=%s host=%s time=%s",
		e.Username, e.IP, e.Port, e.Method, e.Algorithm(), e.Fingerprint(), e.Hostname, e.Time.Format(time.RFC3339))
}
