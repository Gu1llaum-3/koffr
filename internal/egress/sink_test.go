package egress

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"strings"
	"testing"
)

// ADR-0008 — outside production the gate logs and does not send. A mail, a
// webhook or a fleet push must never leave a development machine, and a
// missing configuration puts the integration in that mode rather than in
// error: an agent that refuses to start because nobody configured SMTP is
// worse than one that says it would have sent.
func TestTheGateInSinkModeSendsNothingAndSaysSo(t *testing.T) {
	var logged bytes.Buffer

	gate := New(Options{Mode: Sink, Logger: logger(&logged)})

	err := gate.Send(t.Context(), Message{
		Kind:    Webhook,
		Target:  "https://hooks.exemple.fr/koffr",
		Summary: "backup_failed on shop",
	})
	if err != nil {
		t.Fatalf("the sink returned an error instead of swallowing the message: %v", err)
	}

	for _, want := range []string{"webhook", "hooks.exemple.fr", "backup_failed on shop"} {
		if !strings.Contains(logged.String(), want) {
			t.Errorf("the log does not say what would have been sent (%q):\n%s", want, logged.String())
		}
	}
}

// The guard of ADR-0008: with the development configuration, nothing dials.
func TestNoOutgoingConnectionIsOpenedWithTheDevelopmentConfiguration(t *testing.T) {
	var dialed []string

	gate := New(Options{
		Logger: logger(&bytes.Buffer{}),
		// The gate is handed the only way it has to reach the network. If it
		// ever calls this, the test says where it was going.
		DialContext: func(_ context.Context, _, address string) (net.Conn, error) {
			dialed = append(dialed, address)

			return nil, errNoNetwork
		},
	})

	messages := []Message{
		{Kind: Webhook, Target: "https://hooks.exemple.fr/koffr", Summary: "backup_failed"},
		{Kind: Mail, Target: "smtp.exemple.fr:587", Summary: "backup_missed"},
		{Kind: Uplink, Target: "https://koffr.interne.exemple.fr", Summary: "state push"},
		{Kind: Download, Target: "https://tools.exemple.fr/pg_dump-16.tar.zst", Summary: "managed tool"},
	}
	for _, message := range messages {
		if err := gate.Send(t.Context(), message); err != nil {
			t.Fatalf("%s: %v", message.Kind, err)
		}
	}

	if len(dialed) != 0 {
		t.Fatalf("the development configuration opened connections to %v", dialed)
	}
}

// A gate nobody configured is a sink, not an error and not a real destination.
func TestTheZeroConfigurationIsASink(t *testing.T) {
	gate := New(Options{Logger: logger(&bytes.Buffer{})})

	if gate.Mode() != Sink {
		t.Errorf("mode = %v, want %v", gate.Mode(), Sink)
	}
}

// Live mode is something an operator asks for explicitly, and it is what
// production runs. Nothing else in the repository may open such a connection.
func TestLiveModeIsExplicit(t *testing.T) {
	gate := New(Options{Mode: Live, Logger: logger(&bytes.Buffer{})})

	if gate.Mode() != Live {
		t.Errorf("mode = %v, want %v", gate.Mode(), Live)
	}
}

// A message with no target is refused rather than sent somewhere invented:
// ADR-0008 forbids falling back to a real default value.
func TestAMessageWithNoTargetIsRefused(t *testing.T) {
	gate := New(Options{Mode: Sink, Logger: logger(&bytes.Buffer{})})

	if err := gate.Send(t.Context(), Message{Kind: Webhook}); err == nil {
		t.Fatal("a message with no target was accepted")
	}
}

// The gate never writes a secret into its log either.
func TestTheGateDoesNotLogASecret(t *testing.T) {
	const token = "hunter2-do-not-log-me"

	var logged bytes.Buffer

	gate := New(Options{Mode: Sink, Logger: logger(&logged)})
	if err := gate.Send(t.Context(), Message{
		Kind:    Uplink,
		Target:  "https://koffr.interne.exemple.fr",
		Summary: "state push",
		Secret:  token,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if strings.Contains(logged.String(), token) {
		t.Errorf("the gate logged the token:\n%s", logged.String())
	}
}

func logger(out *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(out, nil))
}

// Dial is the only way anything in koffr opens an operational connection. In
// sink mode it refuses without touching the network at all, which is what makes
// the guard above worth something: the day a live sender is written, it has to
// come through here.
func TestDialRefusesInSinkModeWithoutTouchingTheNetwork(t *testing.T) {
	var dialed []string

	gate := New(Options{
		Mode: Sink, Logger: logger(&bytes.Buffer{}),
		DialContext: func(_ context.Context, _, address string) (net.Conn, error) {
			dialed = append(dialed, address)

			return nil, errNoNetwork
		},
	})

	if _, err := gate.Dial(t.Context(), "tcp", "smtp.exemple.fr:587"); err == nil {
		t.Fatal("the sink opened a connection")
	}
	if len(dialed) != 0 {
		t.Errorf("the sink reached the dialer for %v", dialed)
	}
}

// In live mode it goes through the dialer it was given, and through no other.
func TestDialGoesThroughTheInjectedDialerInLiveMode(t *testing.T) {
	var dialed []string

	gate := New(Options{
		Mode: Live, Logger: logger(&bytes.Buffer{}),
		DialContext: func(_ context.Context, _, address string) (net.Conn, error) {
			dialed = append(dialed, address)

			return nil, errNoNetwork
		},
	})

	if _, err := gate.Dial(t.Context(), "tcp", "smtp.exemple.fr:587"); err == nil {
		t.Fatal("the injected dialer was bypassed")
	}
	if len(dialed) != 1 || dialed[0] != "smtp.exemple.fr:587" {
		t.Errorf("dialed %v, want one call to smtp.exemple.fr:587", dialed)
	}
}
