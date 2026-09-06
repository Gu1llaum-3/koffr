package notify

import "net/smtp"

// SetSendForTest replaces the SMTP transport, so the suite exercises the
// message Koffr composes rather than a mail server's behaviour.
func SetSendForTest(
	cfg *EmailConfig,
	send func(addr string, a smtp.Auth, from string, to []string, msg []byte) error,
) {
	cfg.send = send
}
