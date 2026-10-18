package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile writes content to a temp file and returns its path.
func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// clearEnv unsets every SSH_ALERTD_* variable and pins SSH_ALERTD_ENV_FILE to
// the explicit envFile for the duration of the test, so the host environment
// and any /etc/ssh-alertd/ssh-alertd.env cannot leak into assertions.
func clearEnv(t *testing.T, envFile string) {
	t.Helper()
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(name, EnvPrefix) {
			continue
		}
		// Re-setting records the current value, so testing restores it.
		value, _ := os.LookupEnv(name)
		t.Setenv(name, value)
		os.Unsetenv(name)
	}
	if envFile != "" {
		t.Setenv(EnvPrefix+"ENV_FILE", envFile)
	}
}

const minimalJSON = `{
  "hostname": "from-json",
  "log_source": {"type": "file", "path": "/var/log/auth.log"},
  "notifiers": {
    "telegram": {"enabled": false, "bot_token": "json-token", "chat_id": "42"},
    "smtp": {
      "enabled": true, "host": "smtp.json", "port": 587, "encryption": "none",
      "username": "json-user", "password": "json-pass",
      "from": "alert@example.com", "to": ["a@example.com"]
    }
  }
}`

func TestLoadJSONOnlyUnchanged(t *testing.T) {
	clearEnv(t, "")
	path := writeFile(t, "config.json", minimalJSON)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Hostname != "from-json" {
		t.Errorf("hostname = %q", cfg.Hostname)
	}
	if cfg.Notifiers.Telegram.BotToken != "json-token" || cfg.Notifiers.Telegram.ChatID != "42" {
		t.Errorf("telegram creds changed: %+v", cfg.Notifiers.Telegram)
	}
	if got := cfg.Notifiers.SMTP; got.Host != "smtp.json" || got.Password != "json-pass" ||
		got.Port != 587 || len(got.To) != 1 || got.To[0] != "a@example.com" {
		t.Errorf("smtp changed: %+v", got)
	}
}

func TestEnvOverridesJSON(t *testing.T) {
	clearEnv(t, "")
	path := writeFile(t, "config.json", minimalJSON)

	t.Setenv(EnvPrefix+"HOSTNAME", "from-env")
	t.Setenv(EnvPrefix+"TELEGRAM_ENABLED", "true")
	t.Setenv(EnvPrefix+"TELEGRAM_BOT_TOKEN", "env-token")
	t.Setenv(EnvPrefix+"TELEGRAM_CHAT_ID", "-100")
	t.Setenv(EnvPrefix+"SMTP_HOST", "smtp.env")
	t.Setenv(EnvPrefix+"SMTP_PORT", "2525")
	t.Setenv(EnvPrefix+"SMTP_PASSWORD", "env-pass")
	t.Setenv(EnvPrefix+"SMTP_TO", "x@example.com;y@example.com")
	t.Setenv(EnvPrefix+"SMTP_HTML", "true")
	t.Setenv(EnvPrefix+"LOG_SOURCE_PATH", "/var/log/secure")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Hostname != "from-env" {
		t.Errorf("hostname = %q, want env value", cfg.Hostname)
	}
	tg := cfg.Notifiers.Telegram
	if !tg.Enabled || tg.BotToken != "env-token" || tg.ChatID != "-100" {
		t.Errorf("telegram not overridden: %+v", tg)
	}
	s := cfg.Notifiers.SMTP
	if s.Host != "smtp.env" || s.Port != 2525 || s.Password != "env-pass" || !s.HTML {
		t.Errorf("smtp not overridden: %+v", s)
	}
	if len(s.To) != 2 || s.To[0] != "x@example.com" || s.To[1] != "y@example.com" {
		t.Errorf("smtp to = %v", s.To)
	}
	// Untouched JSON values survive.
	if s.Username != "json-user" || s.From != "alert@example.com" {
		t.Errorf("smtp lost JSON values: %+v", s)
	}
	if cfg.LogSource.Path != "/var/log/secure" || cfg.LogSource.Type != SourceFile {
		t.Errorf("log source = %+v", cfg.LogSource)
	}
}

func TestEnvFileOverridesJSON(t *testing.T) {
	cfgPath := writeFile(t, "config.json", minimalJSON)
	envPath := writeFile(t, "ssh-alertd.env", `
# SMTP credentials live here, not in config.json
SSH_ALERTD_SMTP_PASSWORD=from-file
SSH_ALERTD_SMTP_USERNAME="quoted user"
SSH_ALERTD_TELEGRAM_BOT_TOKEN='single quoted'
export SSH_ALERTD_HOSTNAME=file-host
`)
	clearEnv(t, envPath)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Notifiers.SMTP.Password != "from-file" {
		t.Errorf("password = %q", cfg.Notifiers.SMTP.Password)
	}
	if cfg.Notifiers.SMTP.Username != "quoted user" {
		t.Errorf("username = %q", cfg.Notifiers.SMTP.Username)
	}
	if cfg.Notifiers.Telegram.BotToken != "single quoted" {
		t.Errorf("bot token = %q", cfg.Notifiers.Telegram.BotToken)
	}
	if cfg.Hostname != "file-host" {
		t.Errorf("hostname = %q", cfg.Hostname)
	}
}

func TestRealEnvWinsOverEnvFile(t *testing.T) {
	cfgPath := writeFile(t, "config.json", minimalJSON)
	envPath := writeFile(t, "ssh-alertd.env", "SSH_ALERTD_SMTP_PASSWORD=from-file\n")
	clearEnv(t, envPath)

	t.Setenv(EnvPrefix+"SMTP_PASSWORD", "from-process")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Notifiers.SMTP.Password != "from-process" {
		t.Errorf("password = %q, want process env to win", cfg.Notifiers.SMTP.Password)
	}
}

func TestEnvFileOptionTakesPrecedence(t *testing.T) {
	cfgPath := writeFile(t, "config.json", minimalJSON)
	optPath := writeFile(t, "explicit.env", "SSH_ALERTD_HOSTNAME=explicit\n")
	fallback := writeFile(t, "fallback.env", "SSH_ALERTD_HOSTNAME=fallback\n")
	clearEnv(t, fallback)

	cfg, err := LoadWithOptions(cfgPath, Options{EnvFile: optPath})
	if err != nil {
		t.Fatalf("LoadWithOptions: %v", err)
	}
	if cfg.Hostname != "explicit" {
		t.Errorf("hostname = %q, want explicit env file", cfg.Hostname)
	}
}

func TestMissingEnvFile(t *testing.T) {
	cfgPath := writeFile(t, "config.json", minimalJSON)
	clearEnv(t, "")

	// No explicit env file: the conventional path is optional.
	if _, err := Load(cfgPath); err != nil {
		t.Fatalf("Load without env file: %v", err)
	}

	// Explicitly named but missing: an error, so a typo is not silent.
	missing := filepath.Join(t.TempDir(), "nope.env")
	clearEnv(t, missing)
	if _, err := Load(cfgPath); err == nil {
		t.Fatal("expected error for missing explicit env file")
	}
}

func TestInvalidEnvValues(t *testing.T) {
	cases := []struct {
		name, key, value, want string
	}{
		{"bad bool", EnvPrefix + "SMTP_ENABLED", "yes-please", "invalid boolean"},
		{"bad int", EnvPrefix + "SMTP_PORT", "587x", "invalid integer"},
		{"comma list", EnvPrefix + "SMTP_TO", "a@x,b@y", "invalid address list"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t, "")
			cfgPath := writeFile(t, "config.json", minimalJSON)
			t.Setenv(tc.key, tc.value)

			_, err := Load(cfgPath)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), tc.value) {
				t.Errorf("error leaks the value: %q", err)
			}
		})
	}
}

func TestEnvIsValidatedAfterOverride(t *testing.T) {
	clearEnv(t, "")
	cfgPath := writeFile(t, "config.json", minimalJSON)

	// The JSON password is valid, but the env override is not: the normal
	// validation path must run on the overridden value.
	t.Setenv(EnvPrefix+"SMTP_ENCRYPTION", "plaintext")
	_, err := Load(cfgPath)
	if err == nil || !strings.Contains(err.Error(), "encryption") {
		t.Fatalf("expected encryption validation error, got %v", err)
	}
}

func TestEmptyEnvVarOverridesJSON(t *testing.T) {
	clearEnv(t, "")
	cfgPath := writeFile(t, "config.json", minimalJSON)

	t.Setenv(EnvPrefix+"SMTP_PASSWORD", "")
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Notifiers.SMTP.Password != "" {
		t.Errorf("password = %q, want explicitly cleared", cfg.Notifiers.SMTP.Password)
	}
}

func TestEnvEnablesNotifierAndValidationFails(t *testing.T) {
	clearEnv(t, "")
	// SMTP disabled in JSON; enabled by env without credentials.
	cfgPath := writeFile(t, "config.json", `{
  "log_source": {"type": "journald"},
  "notifiers": {"smtp": {"enabled": false, "host": "", "from": "", "to": []}}
}`)
	t.Setenv(EnvPrefix+"SMTP_ENABLED", "true")
	_, err := Load(cfgPath)
	if err == nil || !strings.Contains(err.Error(), "smtp enabled but host is empty") {
		t.Fatalf("expected SMTP validation error, got %v", err)
	}
}

func TestRedacted(t *testing.T) {
	clearEnv(t, "")
	cfgPath := writeFile(t, "config.json", minimalJSON)
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	red := cfg.Redacted()
	if red.Notifiers.Telegram.BotToken != "***" || red.Notifiers.SMTP.Password != "***" {
		t.Errorf("secrets not masked: %+v", red.Notifiers)
	}
	if red.Notifiers.Telegram.ChatID != "42" || red.Notifiers.SMTP.Username != "json-user" {
		t.Errorf("non-secret fields changed: %+v", red.Notifiers)
	}
	// The original is untouched (value receiver copy semantics).
	if cfg.Notifiers.SMTP.Password != "json-pass" {
		t.Errorf("Redacted mutated the original: %q", cfg.Notifiers.SMTP.Password)
	}

	empty := Config{}
	if got := empty.Redacted().Notifiers.SMTP.Password; got != "" {
		t.Errorf("empty secret = %q, want empty", got)
	}
}

func TestRedactedHidesValuesFromValidationErrors(t *testing.T) {
	// Validation messages name the field, never its value; guard against a
	// future message accidentally interpolating the password.
	clearEnv(t, "")
	cfgPath := writeFile(t, "config.json", `{
  "log_source": {"type": "journald"},
  "notifiers": {"smtp": {"enabled": true, "host": "h", "from": "f@x", "to": ["t@x"],
    "encryption": "none", "password": "super-secret-value"}}
}`)
	t.Setenv(EnvPrefix+"LOG_SOURCE_TYPE", "./logs/file")
	if _, err := Load(cfgPath); err == nil || strings.Contains(err.Error(), "super-secret-value") {
		t.Fatalf("unexpected or leaking error: %v", err)
	}
}

func TestParseEnvFile(t *testing.T) {
	vars, err := parseEnvFile(`
# comment
PLAIN=value
SPACED = value with spaces
DQUOTED="line\nnext"
SQUOTED='no \n expansion'
INLINE=value # trailing comment
export EXPORTED=x=y
`)
	if err != nil {
		t.Fatalf("parseEnvFile: %v", err)
	}
	want := map[string]string{
		"PLAIN":    "value",
		"SPACED":   "value with spaces",
		"DQUOTED":  "line\nnext",
		"SQUOTED":  `no \n expansion`,
		"INLINE":   "value",
		"EXPORTED": "x=y",
	}
	for k, v := range want {
		if vars[k] != v {
			t.Errorf("%s = %q, want %q", k, vars[k], v)
		}
	}

	if _, err := parseEnvFile("NOT A PAIR\n"); err == nil {
		t.Error("expected error for a line without =")
	}
}
