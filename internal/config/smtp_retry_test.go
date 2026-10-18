package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestSMTPRetryDefaults(t *testing.T) {
	// Without retry_attempts the SMTP notifier behaves exactly as before.
	cfg, err := Load(writeFile(t, "config.json", `{
  "notifiers": {"smtp": {"enabled": true, "host": "h", "from": "f@x", "to": ["t@x"]}}
}`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := cfg.Notifiers.SMTP
	if s.RetryAttempts != 0 || s.RetryDelay != 0 || s.RetryMaxDelay != 0 {
		t.Errorf("retry settings changed without opt-in: %+v", s)
	}
}

func TestSMTPRetryDurations(t *testing.T) {
	cfg, err := Load(writeFile(t, "config.json", `{
  "notifiers": {"smtp": {
    "enabled": true, "host": "h", "from": "f@x", "to": ["t@x"],
    "retry_attempts": 3, "retry_delay": "500ms", "retry_max_delay": 2
  }}
}`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := cfg.Notifiers.SMTP
	if s.RetryAttempts != 3 {
		t.Errorf("attempts = %d", s.RetryAttempts)
	}
	if time.Duration(s.RetryDelay) != 500*time.Millisecond {
		t.Errorf("delay = %s", time.Duration(s.RetryDelay))
	}
	// A bare number is seconds.
	if time.Duration(s.RetryMaxDelay) != 2*time.Second {
		t.Errorf("max delay = %s", time.Duration(s.RetryMaxDelay))
	}
}

func TestSMTPRetryDefaultsFillMissing(t *testing.T) {
	cfg, err := Load(writeFile(t, "config.json", `{
  "notifiers": {"smtp": {
    "enabled": true, "host": "h", "from": "f@x", "to": ["t@x"], "retry_attempts": 2
  }}
}`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := cfg.Notifiers.SMTP
	if time.Duration(s.RetryDelay) != time.Second {
		t.Errorf("delay = %s, want 1s", time.Duration(s.RetryDelay))
	}
	if time.Duration(s.RetryMaxDelay) != 5*time.Second {
		t.Errorf("max delay = %s, want 5s", time.Duration(s.RetryMaxDelay))
	}
}

func TestSMTPRetryInvalid(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"negative attempts", `"retry_attempts": -1`, "retry_attempts"},
		{"bad duration", `"retry_delay": "soon"`, "invalid duration"},
		{"negative delay", `"retry_delay": "-1s"`, "must not be negative"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeFile(t, "config.json", `{
  "notifiers": {"smtp": {"enabled": true, "host": "h", "from": "f@x", "to": ["t@x"], `+tc.body+`}}
}`))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}
