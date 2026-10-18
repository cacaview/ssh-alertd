// Package config loads and validates the daemon configuration from a JSON file,
// optionally overridden by SSH_ALERTD_* environment variables.
//
// Precedence, lowest to highest: built-in defaults < JSON file < environment.
// The file is always required; environment variables only override individual
// settings, which keeps credentials (bot token, SMTP password) out of the file.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// EnvPrefix is prepended to every environment variable that overrides config.
// See EnvVars for the supported names.
const EnvPrefix = "SSH_ALERTD_"

// DefaultEnvFile is read when it exists and no env file was named explicitly.
// It holds the same SSH_ALERTD_* names as the process environment.
const DefaultEnvFile = "/etc/ssh-alertd/ssh-alertd.env"

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
}

// Options controls how the configuration is loaded.
type Options struct {
	// EnvFile is an optional path to a KEY=VALUE file. When empty, the file
	// named by SSH_ALERTD_ENV_FILE is read if set. Missing files are an error
	// when the path is explicit; the implicit default is only read when it
	// exists. Variables already present in the environment always win.
	EnvFile string
	// Getenv looks up an environment variable; nil means os.Getenv.
	Getenv func(string) string
}

// Load reads, parses and validates the config file at path.
func Load(path string) (*Config, error) {
	return LoadWithOptions(path, Options{})
}

// LoadWithOptions is Load plus environment support: it applies the optional
// env file and then the SSH_ALERTD_* variable overrides before defaults and
// validation run, so an overridden value is validated like any other.
func LoadWithOptions(path string, o Options) (*Config, error) {
	getenv := o.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}

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

	env, err := loadEnvFile(o.EnvFile, getenv)
	if err != nil {
		return nil, err
	}
	if err := cfg.applyEnv(env, o.Getenv); err != nil {
		return nil, err
	}

	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// loadEnvFile resolves the KEY=VALUE file to read. A path given as an option or
// in SSH_ALERTD_ENV_FILE must exist; the conventional DefaultEnvFile is
// optional so installations that do not use one keep working.
func loadEnvFile(path string, getenv func(string) string) (map[string]string, error) {
	explicit := path != ""
	if !explicit {
		if _, ok := os.LookupEnv(EnvPrefix + "ENV_FILE"); ok {
			path, explicit = os.Getenv(EnvPrefix+"ENV_FILE"), true
		} else if v := getenv(EnvPrefix + "ENV_FILE"); v != "" {
			path, explicit = v, true
		}
	}
	if path == "" {
		path = DefaultEnvFile
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if !explicit && os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read env file %s: %w", path, err)
	}
	vars, err := parseEnvFile(string(data))
	if err != nil {
		return nil, fmt.Errorf("parse env file %s: %w", path, err)
	}
	return vars, nil
}

// parseEnvFile parses dotenv-style content: one KEY=VALUE per line, blank
// lines and # comments ignored, an optional "export " prefix, and single or
// double quotes around the value (double quotes understand \n and \\). Unlike
// a shell it never expands variables or executes anything.
func parseEnvFile(data string) (map[string]string, error) {
	vars := make(map[string]string)
	for i, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("line %d: want KEY=VALUE", i+1)
		}
		val = strings.TrimSpace(val)
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			inner := val[1 : len(val)-1]
			if val[0] == '"' {
				inner = strings.NewReplacer(`\n`, "\n", `\\`, `\`).Replace(inner)
			}
			val = inner
		} else if k := strings.Index(val, " #"); k >= 0 {
			val = strings.TrimSpace(val[:k])
		}
		vars[key] = val
	}
	return vars, nil
}

// lookupEnv returns name from the real process environment first, then the env
// file. A set-but-empty variable counts as a value, so SSH_ALERTD_SMTP_PASSWORD=
// deliberately clears a password that came from JSON.
func lookupEnv(name string, file map[string]string, getenv func(string) string) (string, bool) {
	if v, ok := os.LookupEnv(name); ok {
		return v, true
	}
	if v, ok := file[name]; ok {
		return v, true
	}
	if getenv != nil {
		if v := getenv(name); v != "" {
			return v, true
		}
	}
	return "", false
}

// applyEnv overrides fields from SSH_ALERTD_* variables. Only the first error
// is reported; values are validated afterwards, so the messages here stay
// about parsing and never include variable values (they may hold secrets).
func (c *Config) applyEnv(file map[string]string, getenv func(string) string) error {
	var err error
	str := func(dst *string, name string) {
		if v, ok := lookupEnv(name, file, getenv); ok && err == nil {
			*dst = v
		}
	}
	boolean := func(dst *bool, name string) {
		v, ok := lookupEnv(name, file, getenv)
		if !ok || err != nil {
			return
		}
		b, perr := strconv.ParseBool(strings.TrimSpace(v))
		if perr != nil {
			err = fmt.Errorf("%s: invalid boolean", name)
			return
		}
		*dst = b
	}
	integer := func(dst *int, name string) {
		v, ok := lookupEnv(name, file, getenv)
		if !ok || err != nil {
			return
		}
		n, perr := strconv.Atoi(strings.TrimSpace(v))
		if perr != nil {
			err = fmt.Errorf("%s: invalid integer", name)
			return
		}
		*dst = n
	}

	str(&c.Hostname, EnvPrefix+"HOSTNAME")
	str((*string)(&c.LogSource.Type), EnvPrefix+"LOG_SOURCE_TYPE")
	str(&c.LogSource.Path, EnvPrefix+"LOG_SOURCE_PATH")

	boolean(&c.Notifiers.Telegram.Enabled, EnvPrefix+"TELEGRAM_ENABLED")
	str(&c.Notifiers.Telegram.BotToken, EnvPrefix+"TELEGRAM_BOT_TOKEN")
	str(&c.Notifiers.Telegram.ChatID, EnvPrefix+"TELEGRAM_CHAT_ID")
	str(&c.Notifiers.Telegram.APIBase, EnvPrefix+"TELEGRAM_API_BASE")
	str(&c.Notifiers.Telegram.MessageTemplate, EnvPrefix+"TELEGRAM_MESSAGE_TEMPLATE")
	str(&c.Notifiers.Telegram.MessageTemplateFile, EnvPrefix+"TELEGRAM_MESSAGE_TEMPLATE_FILE")
	str(&c.Notifiers.Telegram.ParseMode, EnvPrefix+"TELEGRAM_PARSE_MODE")

	boolean(&c.Notifiers.SMTP.Enabled, EnvPrefix+"SMTP_ENABLED")
	str(&c.Notifiers.SMTP.Host, EnvPrefix+"SMTP_HOST")
	integer(&c.Notifiers.SMTP.Port, EnvPrefix+"SMTP_PORT")
	str(&c.Notifiers.SMTP.Username, EnvPrefix+"SMTP_USERNAME")
	str(&c.Notifiers.SMTP.Password, EnvPrefix+"SMTP_PASSWORD")
	str(&c.Notifiers.SMTP.From, EnvPrefix+"SMTP_FROM")
	str(&c.Notifiers.SMTP.Encryption, EnvPrefix+"SMTP_ENCRYPTION")
	str(&c.Notifiers.SMTP.SubjectTemplate, EnvPrefix+"SMTP_SUBJECT_TEMPLATE")
	str(&c.Notifiers.SMTP.BodyTemplate, EnvPrefix+"SMTP_BODY_TEMPLATE")
	str(&c.Notifiers.SMTP.BodyTemplateFile, EnvPrefix+"SMTP_BODY_TEMPLATE_FILE")
	boolean(&c.Notifiers.SMTP.HTML, EnvPrefix+"SMTP_HTML")

	// The list has no single-value syntax that can express "clear", so a
	// separator other than a comma is used: SMTP_TO=a@x,b@y is a single
	// address, SMTP_TO="a@x;b@y" or "a@x b@y" is a list.
	if v, ok := lookupEnv(EnvPrefix+"SMTP_TO", file, getenv); ok && err == nil {
		to, perr := splitList(v)
		if perr != nil {
			err = fmt.Errorf("%s: invalid address list", EnvPrefix+"SMTP_TO")
		} else {
			c.Notifiers.SMTP.To = to
		}
	}

	return err
}

// splitList splits recipients on ";" or whitespace, never on "," — a comma is
// valid inside a display name such as "Doe, John <j@x>" and must not silently
// turn one recipient into two.
func splitList(v string) ([]string, error) {
	if strings.Contains(v, ",") && !strings.ContainsAny(v, "; \t") {
		return nil, fmt.Errorf("comma is not a list separator")
	}
	fields := strings.FieldsFunc(v, func(r rune) bool {
		return r == ';' || r == ' ' || r == '\t' || r == '\n'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty list")
	}
	return out, nil
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

// Redacted returns a copy with credentials replaced by a placeholder, for
// logging a configuration without leaking the bot token or SMTP password. An
// empty secret stays empty so "not configured" is still visible.
func (c Config) Redacted() Config {
	mask := func(s string) string {
		if s == "" {
			return ""
		}
		return "***"
	}
	c.Notifiers.Telegram.BotToken = mask(c.Notifiers.Telegram.BotToken)
	c.Notifiers.SMTP.Password = mask(c.Notifiers.SMTP.Password)
	return c
}
