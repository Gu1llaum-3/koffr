package notify

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

// EmailConfig is EF-130's SMTP channel.
type EmailConfig struct {
	Host string
	Port int
	From string
	To   []string

	Username string
	Password string

	// There is no TLS switch, and that is deliberate. net/smtp upgrades with
	// STARTTLS whenever the server advertises it, so the secure path is the
	// default and not a setting anyone can forget. Implicit TLS on port 465 is
	// not supported; saying so is better than a field that does nothing, which
	// is what the first version of this had.

	// send is injected by tests, so the suite exercises the message this
	// builds rather than a mail server's behaviour.
	send func(addr string, a smtp.Auth, from string, to []string, msg []byte) error
}

type email struct{ cfg EmailConfig }

// NewEmail builds the notifier, refusing a configuration that cannot deliver.
func NewEmail(cfg EmailConfig) (Notifier, error) {
	switch {
	case cfg.Host == "":
		return nil, errors.New("notify: the email channel needs a host")
	case cfg.From == "":
		return nil, errors.New("notify: the email channel needs a from address")
	case len(cfg.To) == 0:
		return nil, errors.New("notify: the email channel needs at least one recipient")
	}
	if cfg.Port == 0 {
		cfg.Port = 587
	}

	for _, addr := range append([]string{cfg.From}, cfg.To...) {
		if _, err := mail.ParseAddress(addr); err != nil {
			return nil, fmt.Errorf("notify: %q is not an email address: %w", addr, err)
		}
	}
	// A username with no password is the shape a missing environment variable
	// leaves behind, and it fails at the server with an unhelpful message.
	if cfg.Username != "" && cfg.Password == "" {
		return nil, errors.New("notify: the email channel has a username and no password")
	}
	return &email{cfg: cfg}, nil
}

func (e *email) Name() string { return "email to " + strings.Join(e.cfg.To, ", ") }

func (e *email) Notify(ctx context.Context, ev Event) error {
	msg := e.compose(ev)
	addr := net.JoinHostPort(e.cfg.Host, fmt.Sprint(e.cfg.Port))

	var auth smtp.Auth
	if e.cfg.Username != "" {
		auth = smtp.PlainAuth("", e.cfg.Username, e.cfg.Password, e.cfg.Host)
	}

	send := e.cfg.send
	if send == nil {
		send = smtp.SendMail
	}

	// SendMail has no context, so the deadline is honoured by racing it. The
	// goroutine finishes on its own afterwards; it holds nothing but a socket
	// the server will close.
	done := make(chan error, 1)
	go func() { done <- send(addr, auth, e.cfg.From, e.cfg.To, msg) }()

	select {
	case err := <-done:
		if err != nil {
			// The message body is never included: it names sources and hosts,
			// and an SMTP error is quoted verbatim into logs.
			return fmt.Errorf("notify: send mail through %s: %w", addr, err)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("notify: sending mail through %s did not finish: %w", addr, ctx.Err())
	}
}

// compose builds the message.
//
// Plain text, because an alert has to be readable in a phone's preview at three
// in the morning, and because HTML mail is a way to be filtered as spam by a
// server nobody controls.
func (e *email) compose(ev Event) []byte {
	subject := fmt.Sprintf("[koffr] %s: %s", strings.ToUpper(string(ev.Severity)), ev.Kind)
	if ev.SourceID != "" {
		subject = fmt.Sprintf("[koffr] %s: %s on %s",
			strings.ToUpper(string(ev.Severity)), ev.Kind, ev.SourceID)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", e.cfg.From)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(e.cfg.To, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	fmt.Fprintf(&b, "Date: %s\r\n", ev.OccurredAt.UTC().Format(time.RFC1123Z))
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")

	fmt.Fprintf(&b, "%s\n\n", ev.Message)
	fmt.Fprintf(&b, "event:    %s\n", ev.Kind)
	fmt.Fprintf(&b, "severity: %s\n", ev.Severity)
	if ev.SourceID != "" {
		fmt.Fprintf(&b, "source:   %s\n", ev.SourceID)
	}
	if ev.BackupID != "" {
		fmt.Fprintf(&b, "backup:   %s\n", ev.BackupID)
	}
	fmt.Fprintf(&b, "at:       %s\n", ev.OccurredAt.UTC().Format(time.RFC3339))
	for k, v := range ev.Details {
		fmt.Fprintf(&b, "%-9s %s\n", k+":", v)
	}
	return []byte(b.String())
}
