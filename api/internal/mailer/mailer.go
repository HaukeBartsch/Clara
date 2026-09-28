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

// Timeouts bound one send so that an unreachable or stalling relay cannot hold
// a login request open (REQ-AUTH-057). They are variables rather than constants
// only so the tests can exercise them in milliseconds; production uses these
// values.
var (
	dialTimeout = 10 * time.Second // connect and TLS handshake
	ioTimeout   = 10 * time.Second // every read or write after that
	sendTimeout = 30 * time.Second // the whole send, QUIT included
)

// deadlineConn re-arms the I/O deadline on each read and write, so ioTimeout
// bounds one protocol step rather than only the handshake. notAfter caps the
// whole exchange besides: a relay that trickles bytes would otherwise satisfy
// the per-step deadline forever.
type deadlineConn struct {
	net.Conn
	step     time.Duration
	notAfter time.Time
}

func (c *deadlineConn) arm() {
	d := time.Now().Add(c.step)
	if c.notAfter.Before(d) {
		d = c.notAfter
	}
	// A SetDeadline error means the connection is gone already; the read or
	// write that follows reports it.
	c.Conn.SetDeadline(d)
}

func (c *deadlineConn) Read(b []byte) (int, error) {
	c.arm()
	return c.Conn.Read(b)
}

func (c *deadlineConn) Write(b []byte) (int, error) {
	c.arm()
	return c.Conn.Write(b)
}

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
// when SMTP_USERNAME is set; an anonymous relay is used otherwise. Header
// values must not contain a line break — see the check below. The body needs no
// escaping: net/smtp dot-stuffs it and normalizes its line endings (RFC 5321
// §4.5.2).
func (m *Mailer) Send(to, subject, body string) error {
	if !m.Enabled() {
		return fmt.Errorf("mailer: no SMTP relay configured")
	}
	// A line break inside a header value would let crafted text append headers of
	// its own to the message. net/smtp checks the two envelope addresses on its
	// own, but only after EHLO and MAIL FROM went out, so reject early here.
	for _, h := range []struct{ name, value string }{
		{"sender", m.from}, {"recipient", to}, {"subject", subject},
	} {
		if strings.ContainsAny(h.value, "\r\n") {
			return fmt.Errorf("mailer: %s must not contain a line break", h.name)
		}
	}

	addr := net.JoinHostPort(m.host, fmt.Sprint(m.port))
	notAfter := time.Now().Add(sendTimeout)

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
	conn = &deadlineConn{Conn: conn, step: ioTimeout, notAfter: notAfter}

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
