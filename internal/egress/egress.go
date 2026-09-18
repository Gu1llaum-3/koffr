// Package egress is the single way out of the process for operational traffic:
// SMTP, webhooks, the uplink to the central server, and tool downloads.
//
// It is not the way out for backups. Writing an archive to a destination is
// what koffr is for, and that traffic goes through internal/store and
// internal/engine, which really write, in development as in production. What
// passes through here is the traffic that must not leave a development
// machine: a mail to an operator, a webhook to a chat room, a push to a fleet
// server (ADR-0008).
package egress

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
)

// errNoNetwork is what a dialer returns when nothing is meant to dial.
var errNoNetwork = errors.New("egress: no network in this mode")

// Mode says whether the gate really sends.
type Mode int

const (
	// Sink logs what would have been sent and sends nothing. It is the zero
	// value on purpose: a gate nobody configured must be harmless, never in
	// error and never pointing at a real destination (ADR-0008).
	Sink Mode = iota

	// Live really sends. Production asks for it explicitly.
	Live
)

func (m Mode) String() string {
	if m == Live {
		return "live"
	}

	return "sink"
}

// Kind is what a message is. The four are the operational outputs ADR-0008
// lists; there is no fifth without an ADR.
type Kind string

// The four operational outputs of ADR-0008.
const (
	Mail     Kind = "mail"
	Webhook  Kind = "webhook"
	Uplink   Kind = "uplink"
	Download Kind = "download"
)

// Message is one thing to send. It carries no host, no token and no recipient
// of its own beyond what the caller read from the validated configuration:
// nothing in this package has a default that points anywhere real.
type Message struct {
	Kind    Kind
	Target  string
	Summary string

	// Secret travels with the message and is never logged.
	Secret string
}

// DialContext is how the gate reaches the network. Injecting it is what lets a
// test assert that nothing dialled.
type DialContext func(ctx context.Context, network, address string) (net.Conn, error)

// Options configures the gate. The zero value is a sink.
type Options struct {
	Mode        Mode
	Logger      *slog.Logger
	DialContext DialContext
}

// Gate is the door. Every operational output goes through one.
type Gate struct {
	mode   Mode
	logger *slog.Logger
	dial   DialContext
}

// New builds the gate.
func New(options Options) *Gate {
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}

	dial := options.DialContext
	if dial == nil {
		dial = func(context.Context, string, string) (net.Conn, error) { return nil, errNoNetwork }
	}

	return &Gate{mode: options.Mode, logger: logger, dial: dial}
}

// Mode reports whether this gate really sends.
func (g *Gate) Mode() Mode {
	return g.mode
}

// Send delivers the message, or records that it would have.
func (g *Gate) Send(ctx context.Context, message Message) error {
	if message.Target == "" {
		return fmt.Errorf("egress: a %s message has no target, and koffr does not invent one", message.Kind)
	}

	if g.mode == Sink {
		// Logged, not sent. The line says enough to debug a rule without
		// carrying what must not be written down.
		g.logger.LogAttrs(ctx, slog.LevelInfo, "egress suppressed",
			slog.String("mode", g.mode.String()),
			slog.String("kind", string(message.Kind)),
			slog.String("target", message.Target),
			slog.String("summary", message.Summary),
		)

		return nil
	}

	return g.send(ctx, message)
}

// Dial opens an operational connection. It is the only place in koffr that
// does: the lint rules of ADR-0010 keep net, net/http and net/smtp out of every
// package but store, engine and this one, and a live sender written later has
// to come through here rather than build its own client.
//
// In sink mode it refuses without reaching the network at all.
func (g *Gate) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	if g.mode == Sink {
		return nil, fmt.Errorf("egress: %w (would have dialled %s)", errNoNetwork, address)
	}

	connection, err := g.dial(ctx, network, address)
	if err != nil {
		return nil, fmt.Errorf("egress: dial %s: %w", address, err)
	}

	return connection, nil
}

// send is where live delivery will be implemented, one kind at a time: mail at
// lot 6, the uplink at lot 8, downloads at lot 1. Until then, saying so is
// better than a silent success.
func (g *Gate) send(_ context.Context, message Message) error {
	return fmt.Errorf("egress: sending a %s message is not implemented yet", message.Kind)
}
