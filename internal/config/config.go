// Package config loads and validates the daemon configuration from a JSON file.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// Config is the root configuration object.
type Config struct {
	// Hostname overrides the host name reported in alerts. Empty means the
	// daemon uses the OS hostname.
	Hostname string `json:"hostname"`

	// LogSource controls where SSH log lines are read from.
	LogSource LogSourceConfig `json:"log_source"`

	// Notifiers configures each supported notification backend.
	Notifiers NotifiersConfig `json:"notifiers"`
}

// SourceType enumerates the supported log sources.
type SourceType string

const (
	// SourceJournald reads sshd messages from the systemd journal.
	SourceJournald SourceType = "journald"
	// SourceFile tails a classic auth log file (e.g. /var/log/auth.log).
	SourceFile SourceType = "file"
)

// LogSourceConfig selects and parameterizes the log source.
type LogSourceConfig struct {
	Type SourceType `json:"type"`
	// Path is the log file to tail when Type is "file".
	Path string `json:"path"`
}

// NotifiersConfig groups all notifier backends. Telegram and SMTP are
// implemented today; the remaining fields are placeholders so the schema is
// stable as new backends land.
type NotifiersConfig struct {
	Telegram TelegramConfig `json:"telegram"`
	SMTP     SMTPConfig     `json:"smtp"`
	// Reserved for future backends.
	WhatsApp map[string]any `json:"whatsapp,omitempty"`
	WeCom    map[string]any `json:"wecom,omitempty"`
	DingTalk map[string]any `json:"dingtalk,omitempty"`
	Feishu   map[string]any `json:"feishu,omitempty"`
}

// TelegramConfig holds the Telegram Bot API credentials.
type TelegramConfig struct {
	Enabled  bool   `json:"enabled"`
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
	// APIBase optionally overrides the Telegram API endpoint, useful behind a
	// reverse proxy. Defaults to https://api.telegram.org when empty.
	APIBase string `json:"api_base"`
	// MessageTemplate is an optional Go template for the message text (fields
	// .Username .IP .Port .Method .Hostname .Time). Empty uses the built-in
	// HTML format.
	MessageTemplate string `json:"message_template"`
	// MessageTemplateFile, when set, is read as the message template and takes
	// precedence over MessageTemplate — convenient for multi-line messages.
	MessageTemplateFile string `json:"message_template_file"`
	// ParseMode is "HTML" (default), "MarkdownV2", "Markdown" or "none"; it
	// applies to a custom MessageTemplate. The built-in format is always HTML.
	ParseMode string `json:"parse_mode"`
}

// SMTPConfig holds the settings for sending alerts over SMTP (email).
type SMTPConfig struct {
	Enabled  bool   `json:"enabled"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	// From is the envelope/header sender address.
	From string `json:"from"`
	// To is the list of recipient addresses (at least one).
	To []string `json:"to"`
	// Encryption selects the transport security: "starttls" (default, typically
	// port 587), "tls" (implicit TLS / SMTPS, typically port 465) or "none".
	Encryption string `json:"encryption"`

	// SubjectTemplate is an optional Go text/template for the email subject.
	// Templates receive the login event with fields .Username .IP .Port .Method
	// .Hostname .Time (e.g. {{.Username}}, {{.Time.Format "2006-01-02 15:04:05"}}).
	// Empty uses the built-in subject.
	SubjectTemplate string `json:"subject_template"`
	// BodyTemplate is an optional Go template for the email body (same fields as
	// SubjectTemplate). Empty uses the built-in body.
	BodyTemplate string `json:"body_template"`
	// BodyTemplateFile, when set, is read as the body template and takes
	// precedence over BodyTemplate — convenient for multi-line bodies.
	BodyTemplateFile string `json:"body_template_file"`
	// HTML renders the body as text/html (using html/template for auto-escaping)
	// instead of text/plain. Pair it with a BodyTemplate.
	HTML bool `json:"html"`

	// RetryAttempts is the total number of delivery attempts per alert,
	// including the first; 0 or 1 sends once. Only connecting, the greeting and
	// the DATA command are retried — authentication, sender and recipient
	// rejections, and anything after the body was accepted are not.
	RetryAttempts int `json:"retry_attempts"`
	// RetryDelay is the first backoff before a retry, doubled on each further
	// attempt. 0 means 1s.
	RetryDelay Duration `json:"retry_delay"`
	// RetryMaxDelay caps the backoff. 0 means 5s.
	RetryMaxDelay Duration `json:"retry_max_delay"`
}

// Duration is a time.Duration in JSON. It accepts a string such as "2s" or
// "500ms", or a number of seconds.
type Duration time.Duration

// UnmarshalJSON implements json.Unmarshaler.
func (d *Duration) UnmarshalJSON(b []byte) error {
	text := strings.TrimSpace(string(b))
	if text == "null" {
		return nil
	}
	if strings.HasPrefix(text, `"`) {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		parsed, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("invalid duration %q (want e.g. \"2s\")", s)
		}
		*d = Duration(parsed)
		return nil
	}
	var seconds float64
	if err := json.Unmarshal(b, &seconds); err != nil {
		return fmt.Errorf("invalid duration %s (want a string like \"2s\" or seconds)", text)
	}
	*d = Duration(seconds * float64(time.Second))
	return nil
}

// MarshalJSON implements json.Marshaler.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// Load reads, parses and validates the config file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.LogSource.Type == "" {
		c.LogSource.Type = SourceJournald
	}
	if c.LogSource.Type == SourceFile && c.LogSource.Path == "" {
		c.LogSource.Path = "/var/log/auth.log"
	}
	if c.Notifiers.Telegram.APIBase == "" {
		c.Notifiers.Telegram.APIBase = "https://api.telegram.org"
	}
	if c.Notifiers.Telegram.ParseMode == "" {
		c.Notifiers.Telegram.ParseMode = "HTML"
	}

	smtp := &c.Notifiers.SMTP
	if smtp.Encryption == "" {
		if smtp.Port == 465 {
			smtp.Encryption = "tls"
		} else {
			smtp.Encryption = "starttls"
		}
	}
	if smtp.Port == 0 {
		if smtp.Encryption == "tls" {
			smtp.Port = 465
		} else {
			smtp.Port = 587
		}
	}
	if smtp.RetryAttempts > 0 && smtp.RetryDelay == 0 {
		smtp.RetryDelay = Duration(time.Second)
	}
	if smtp.RetryAttempts > 0 && smtp.RetryMaxDelay == 0 {
		smtp.RetryMaxDelay = Duration(5 * time.Second)
	}
}

func (c *Config) validate() error {
	switch c.LogSource.Type {
	case SourceJournald, SourceFile:
	default:
		return fmt.Errorf("invalid log_source.type %q (want %q or %q)",
			c.LogSource.Type, SourceJournald, SourceFile)
	}

	if c.Notifiers.Telegram.Enabled {
		if c.Notifiers.Telegram.BotToken == "" {
			return fmt.Errorf("telegram enabled but bot_token is empty")
		}
		if c.Notifiers.Telegram.ChatID == "" {
			return fmt.Errorf("telegram enabled but chat_id is empty")
		}
		switch c.Notifiers.Telegram.ParseMode {
		case "HTML", "MarkdownV2", "Markdown", "none":
		default:
			return fmt.Errorf("telegram parse_mode %q is invalid (want HTML, MarkdownV2, Markdown or none)",
				c.Notifiers.Telegram.ParseMode)
		}
	}

	if c.Notifiers.SMTP.Enabled {
		s := c.Notifiers.SMTP
		switch s.Encryption {
		case "starttls", "tls", "none":
		default:
			return fmt.Errorf("smtp enabled but encryption %q is invalid (want starttls, tls or none)", s.Encryption)
		}
		if s.RetryAttempts < 0 {
			return fmt.Errorf("smtp retry_attempts must not be negative")
		}
		if s.RetryDelay < 0 || s.RetryMaxDelay < 0 {
			return fmt.Errorf("smtp retry_delay and retry_max_delay must not be negative")
		}
		if s.Host == "" {
			return fmt.Errorf("smtp enabled but host is empty")
		}
		if s.From == "" {
			return fmt.Errorf("smtp enabled but from is empty")
		}
		if len(s.To) == 0 {
			return fmt.Errorf("smtp enabled but to is empty")
		}
	}
	return nil
}
