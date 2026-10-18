// Package notifier defines the notification abstraction and a dispatcher that
// fans a single login event out to every configured backend.
//
// Each backend (Telegram, WeCom, DingTalk, Feishu, WhatsApp, SMTP, ...) lives
// in its own file and implements the Notifier interface, keeping the channels
// decoupled from the monitor and from each other.
package notifier

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"ssh-alertd/internal/event"
)

// Notifier is a single notification channel.
type Notifier interface {
	// Name identifies the backend in logs.
	Name() string
	// Send delivers one login event. It must honor ctx cancellation.
	Send(ctx context.Context, e event.LoginEvent) error
}

// Dispatcher delivers each event to all registered notifiers concurrently.
type Dispatcher struct {
	notifiers []Notifier
	timeout   time.Duration
	logger    *log.Logger
}

// NewDispatcher creates a dispatcher. perSendTimeout bounds how long any single
// notifier may take before its delivery is cancelled.
func NewDispatcher(logger *log.Logger, perSendTimeout time.Duration, notifiers ...Notifier) *Dispatcher {
	if logger == nil {
		logger = log.Default()
	}
	if perSendTimeout <= 0 {
		perSendTimeout = 10 * time.Second
	}
	return &Dispatcher{notifiers: notifiers, timeout: perSendTimeout, logger: logger}
}

// Len reports how many notifiers are registered.
func (d *Dispatcher) Len() int { return len(d.notifiers) }

// Dispatch sends e to every notifier and waits for all of them. A failure in
// one backend never blocks or fails the others; errors are logged.
func (d *Dispatcher) Dispatch(ctx context.Context, e event.LoginEvent) {
	var wg sync.WaitGroup
	for _, n := range d.notifiers {
		wg.Add(1)
		go func(n Notifier) {
			defer wg.Done()
			sendCtx, cancel := context.WithTimeout(ctx, d.timeout)
			defer cancel()
			if err := n.Send(sendCtx, e); err != nil {
				d.logger.Printf("notifier %s failed: %v", n.Name(), err)
				return
			}
			d.logger.Printf("notifier %s delivered alert for %s@%s", n.Name(), e.Username, e.IP)
		}(n)
	}
	wg.Wait()
}

// plainBody is a shared, format-agnostic rendering of an event that simple
// backends can reuse. Key lines appear only for publickey logins.
func plainBody(e event.LoginEvent) string {
	body := fmt.Sprintf(
		"🔐 SSH Login Alert\n"+
			"Host:         %s\n"+
			"User:         %s\n"+
			"From IP:      %s\n"+
			"Client Port:  %s\n"+
			"Method:       %s",
		e.Hostname, e.Username, e.IP, e.Port, e.Method,
	)
	if e.Method == "publickey" {
		body += fmt.Sprintf("\nKey:          %s\nFingerprint:  %s", e.Algorithm(), e.Fingerprint())
	}
	return body + fmt.Sprintf("\nTime:         %s", e.Time.Format("2006-01-02 15:04:05 MST"))
}
