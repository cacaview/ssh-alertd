package notifier

import (
	"strings"
	"testing"

	"ssh-alertd/internal/event"
)

func TestTelegramDefaultMessage(t *testing.T) {
	tg, err := NewTelegram(TelegramOptions{BotToken: "x", ChatID: "1"})
	if err != nil {
		t.Fatalf("NewTelegram: %v", err)
	}
	if tg.parseMode != "HTML" {
		t.Errorf("default parseMode = %q, want HTML", tg.parseMode)
	}
	msg, err := tg.render(testEvent())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"<b>SSH Login Alert</b>", "<code>alice</code>", "<code>203.0.113.5</code>"} {
		if !strings.Contains(msg, want) {
			t.Errorf("default message missing %q\n%s", want, msg)
		}
	}
}

func TestTelegramCustomTemplateHTML(t *testing.T) {
	tg, err := NewTelegram(TelegramOptions{
		BotToken:        "x",
		ChatID:          "1",
		MessageTemplate: "<b>{{.Username}}</b> from {{.IP}}",
	})
	if err != nil {
		t.Fatalf("NewTelegram: %v", err)
	}
	if tg.parseMode != "HTML" {
		t.Errorf("parseMode = %q, want HTML", tg.parseMode)
	}
	msg, err := tg.render(testEvent())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if msg != "<b>alice</b> from 203.0.113.5" {
		t.Errorf("rendered = %q", msg)
	}
}

func TestTelegramHTMLTemplateEscapesFields(t *testing.T) {
	// A username with markup must not break the HTML; html/template escapes it.
	tg, err := NewTelegram(TelegramOptions{
		BotToken:        "x",
		ChatID:          "1",
		MessageTemplate: "<b>{{.Username}}</b>",
	})
	if err != nil {
		t.Fatalf("NewTelegram: %v", err)
	}
	e := testEvent()
	e.Username = "<script>"
	msg, err := tg.render(e)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(msg, "<script>") {
		t.Errorf("field not escaped: %q", msg)
	}
	if !strings.Contains(msg, "&lt;script&gt;") {
		t.Errorf("expected escaped field, got %q", msg)
	}
}

func TestTelegramPlainParseModeNone(t *testing.T) {
	tg, err := NewTelegram(TelegramOptions{
		BotToken:        "x",
		ChatID:          "1",
		MessageTemplate: "{{.Username}} logged in",
		ParseMode:       "none",
	})
	if err != nil {
		t.Fatalf("NewTelegram: %v", err)
	}
	if tg.parseMode != "" {
		t.Errorf("parseMode for none = %q, want empty (plain)", tg.parseMode)
	}
	msg, _ := tg.render(event.LoginEvent{Username: "bob"})
	if msg != "bob logged in" {
		t.Errorf("rendered = %q", msg)
	}
}

func TestTelegramRejectsBadTemplate(t *testing.T) {
	if _, err := NewTelegram(TelegramOptions{
		BotToken: "x", ChatID: "1", MessageTemplate: "{{.Username",
	}); err == nil {
		t.Fatal("expected error for malformed template, got nil")
	}
}

func TestTelegramDefaultMessageShowsKeyForPublickey(t *testing.T) {
	tg, err := NewTelegram(TelegramOptions{BotToken: "x", ChatID: "1"})
	if err != nil {
		t.Fatalf("NewTelegram: %v", err)
	}
	e := testEvent()
	e.KeyAlgorithm = "ED25519"
	e.KeyFingerprint = "SHA256:flbXq7ZJ0j1lEALPYzbOGOVUwI2Yz0Y4yYPqbPZ8ZjQ"
	msg, err := tg.render(e)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		"<b>Key:</b> <code>ED25519</code>",
		"<b>Fingerprint:</b> <code>SHA256:flbXq7ZJ0j1lEALPYzbOGOVUwI2Yz0Y4yYPqbPZ8ZjQ</code>",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q\n%s", want, msg)
		}
	}
	if strings.Index(msg, "Fingerprint") > strings.Index(msg, "Time") {
		t.Errorf("key lines should precede Time\n%s", msg)
	}
}

func TestTelegramDefaultMessageKeyUnknown(t *testing.T) {
	tg, _ := NewTelegram(TelegramOptions{BotToken: "x", ChatID: "1"})
	// publickey login without key details in the log (LogLevel INFO).
	e := testEvent()
	msg, err := tg.render(e)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(msg, "<b>Key:</b> <code>N/A</code>") ||
		!strings.Contains(msg, "<b>Fingerprint:</b> <code>N/A</code>") {
		t.Errorf("expected N/A key fields\n%s", msg)
	}
	if strings.Contains(msg, "SHA256") {
		t.Errorf("message must not invent a fingerprint\n%s", msg)
	}
}

func TestTelegramDefaultMessageHidesKeyForPassword(t *testing.T) {
	tg, _ := NewTelegram(TelegramOptions{BotToken: "x", ChatID: "1"})
	e := testEvent()
	e.Method = "password"
	e.KeyAlgorithm = "ED25519"
	e.KeyFingerprint = "SHA256:should-not-appear"
	msg, _ := tg.render(e)
	if strings.Contains(msg, "Key:") || strings.Contains(msg, "SHA256") {
		t.Errorf("password login must not show key lines\n%s", msg)
	}
}

func TestTelegramKeyFieldsAreEscaped(t *testing.T) {
	tg, _ := NewTelegram(TelegramOptions{BotToken: "x", ChatID: "1"})
	e := testEvent()
	e.KeyAlgorithm = "<script>"
	e.KeyFingerprint = "SHA256:&"
	msg, _ := tg.render(e)
	if strings.Contains(msg, "<script>") || !strings.Contains(msg, "&lt;script&gt;") {
		t.Errorf("key algorithm not escaped: %s", msg)
	}
}
