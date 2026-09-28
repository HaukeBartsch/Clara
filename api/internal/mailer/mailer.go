// Package mailer sends transactional email through the standard library's
// net/smtp — currently only the two-factor login codes of REQ-AUTH-057. The
// API performs the send so SMTP credentials stay on the API side (Technology
// Stack design §3, no new dependencies). Nothing here logs message contents:
// a code must never reach a log line.
package mailer

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"csms/api/internal/config"
)

// dialTimeout bounds every network step of a send so an unreachable relay
// cannot hang a login request.
const dialTimeout = 10 * time.Second

// Mailer delivers messages through the configured SMTP relay (§3.11).
type Mailer struct {
	host     string
	port     int
	security string // starttls | tls | none
	username string
	password string
	from     string
}

// New builds a Mailer from configuration; Enabled reports whether one can be
// used at all. Callers must check Enabled before attempting a send and fail
// with a clear error otherwise (REQ-AUTH-057).
func New(cfg *config.Config) *Mailer {
	return &Mailer{
		host:     cfg.SMTPHost,
		port:     cfg.SMTPPort,
		security: cfg.SMTPSecurity,
		username: cfg.SMTPUsername,
		password: cfg.SMTPPassword,
		from:     cfg.OTPMailFrom,
	}
}

// Enabled reports whether an SMTP relay is configured.
func (m *Mailer) Enabled() bool { return m != nil && m.host != "" }

// Send delivers one plain-text message. Authentication is attempted only
// when SMTP_USERNAME is set; an anonymous relay is used otherwise.
func (m *Mailer) Send(to, subject, body string) error {
	if !m.Enabled() {
		return fmt.Errorf("mailer: no SMTP relay configured")
	}
	addr := net.JoinHostPort(m.host, fmt.Sprint(m.port))

	var conn net.Conn
	var err error
	switch m.security {
	case "tls":
		conn, err = tls.DialWithDialer(&net.Dialer{Timeout: dialTimeout}, "tcp", addr,
			&tls.Config{ServerName: m.host})
	default: // starttls and none connect in the clear first
		conn, err = net.DialTimeout("tcp", addr, dialTimeout)
	}
	if err != nil {
		return fmt.Errorf("mailer: dial %s: %w", addr, err)
	}

	client, err := smtp.NewClient(conn, m.host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("mailer: smtp client: %w", err)
	}
	defer client.Quit()

	if m.security == "starttls" {
		if err = client.StartTLS(&tls.Config{ServerName: m.host}); err != nil {
			return fmt.Errorf("mailer: starttls: %w", err)
		}
	}
	if m.username != "" {
		auth := smtp.PlainAuth("", m.username, m.password, m.host)
		if ok, _ := client.Extension("AUTH"); !ok {
			return fmt.Errorf("mailer: relay at %s does not advertise AUTH", addr)
		}
		if err = client.Auth(auth); err != nil {
			return fmt.Errorf("mailer: auth as %s: %w", m.username, err)
		}
	}

	msg := strings.Builder{}
	fmt.Fprintf(&msg, "From: %s\r\nTo: %s\r\nSubject: %s\r\n"+
		"Date: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s",
		m.from, to, subject, time.Now().UTC().Format(time.RFC1123Z), body)

	if err = client.Mail(m.from); err != nil {
		return fmt.Errorf("mailer: mail from: %w", err)
	}
	if err = client.Rcpt(to); err != nil {
		return fmt.Errorf("mailer: rcpt to: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("mailer: data: %w", err)
	}
	if _, err = w.Write([]byte(msg.String())); err != nil {
		return fmt.Errorf("mailer: write: %w", err)
	}
	if err = w.Close(); err != nil {
		return fmt.Errorf("mailer: close data: %w", err)
	}
	return nil
}
